package backup

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// BackupType represents the type of backup
type BackupType string

const (
	BackupTypeFull         BackupType = "full"
	BackupTypeIncremental  BackupType = "incremental"
	BackupTypeDifferential BackupType = "differential"
)

// BackupStatus represents the status of a backup
type BackupStatus string

const (
	BackupStatusPending    BackupStatus = "pending"
	BackupStatusInProgress BackupStatus = "in_progress"
	BackupStatusCompleted  BackupStatus = "completed"
	BackupStatusFailed     BackupStatus = "failed"
	BackupStatusVerifying  BackupStatus = "verifying"
)

// BackupMetadata contains metadata about a backup
type BackupMetadata struct {
	ID            string            `json:"id"`
	Type          BackupType        `json:"type"`
	Status        BackupStatus      `json:"status"`
	Source        string            `json:"source"`      // e.g., "postgres-primary", "vault"
	Destination   string            `json:"destination"` // S3 path or backup location
	Size          int64             `json:"size"`        // Bytes
	Checksum      string            `json:"checksum"`    // MD5 hash
	StartTime     time.Time         `json:"start_time"`
	EndTime       time.Time         `json:"end_time"`
	Duration      time.Duration     `json:"duration"`
	ErrorMessage  string            `json:"error_message,omitempty"`
	RetentionDays int               `json:"retention_days"`
	ExpiresAt     time.Time         `json:"expires_at"`
	VerifiedAt    time.Time         `json:"verified_at,omitempty"`
	Tags          map[string]string `json:"tags"`
	BackupChain   []string          `json:"backup_chain,omitempty"` // For incremental/differential
}

// BackupSchedule defines a backup schedule
type BackupSchedule struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Source        string     `json:"source"`
	BackupType    BackupType `json:"backup_type"`
	Schedule      string     `json:"schedule"` // Cron expression
	RetentionDays int        `json:"retention_days"`
	Enabled       bool       `json:"enabled"`
	LastRun       time.Time  `json:"last_run"`
	NextRun       time.Time  `json:"next_run"`
	SuccessCount  int        `json:"success_count"`
	FailureCount  int        `json:"failure_count"`
}

// RestorePoint represents a point-in-time restore capability
type RestorePoint struct {
	ID            string           `json:"id"`
	Timestamp     time.Time        `json:"timestamp"`
	Source        string           `json:"source"`
	Type          BackupType       `json:"type"`
	RecoveryPoint time.Time        `json:"recovery_point"` // What time this restores to
	BackupChain   []BackupMetadata `json:"backup_chain"`   // Full/incremental/differential chain
	Verified      bool             `json:"verified"`
	EstimatedRTO  time.Duration    `json:"estimated_rto"` // Estimated Recovery Time Objective
}

// BackupManager manages backup operations
type BackupManager struct {
	storage  BackupStorage
	vault    VaultBackup
	postgres PostgreSQLBackup
}

// NewBackupManager creates a new backup manager
func NewBackupManager(storage BackupStorage, vault VaultBackup, postgres PostgreSQLBackup) *BackupManager {
	return &BackupManager{
		storage:  storage,
		vault:    vault,
		postgres: postgres,
	}
}

// ExecuteFullBackup executes a full backup of all systems
func (b *BackupManager) ExecuteFullBackup(ctx context.Context, destination string) (*BackupMetadata, error) {
	metadata := &BackupMetadata{
		ID:        generateBackupID(),
		Type:      BackupTypeFull,
		Status:    BackupStatusInProgress,
		StartTime: time.Now(),
		Tags:      make(map[string]string),
	}

	// Backup PostgreSQL
	pgMetadata, err := b.postgres.BackupFull(ctx, destination)
	if err != nil {
		metadata.Status = BackupStatusFailed
		metadata.ErrorMessage = fmt.Sprintf("PostgreSQL backup failed: %v", err)
		return metadata, err
	}
	metadata.Size += pgMetadata.Size

	// Backup Vault
	vaultMetadata, err := b.vault.BackupFull(ctx, destination)
	if err != nil {
		metadata.Status = BackupStatusFailed
		metadata.ErrorMessage = fmt.Sprintf("Vault backup failed: %v", err)
		return metadata, err
	}
	metadata.Size += vaultMetadata.Size

	// metadata.ID is this combined record's own identity, distinct from
	// pgMetadata.ID/vaultMetadata.ID (each component backend generates its
	// own ID for where IT stored its data -- see RealPostgreSQLBackup and
	// RealVaultBackup, which write to their own local directories directly
	// rather than through the BackupStorage interface, since that
	// interface's job is durable long-term storage of the resulting
	// artifact, e.g. S3, not where a backend stages its own dump files).
	// VerifyBackup below reads metadata.ID back out of storage to confirm
	// round-trip integrity -- nothing had written anything under that ID
	// until this manifest, so record the actual pointers to the real data
	// (component IDs + Destination paths) as that manifest, rather than
	// have VerifyBackup unconditionally fail every real backup with
	// "not found."
	metadata.BackupChain = []string{pgMetadata.ID, vaultMetadata.ID}
	manifest, err := json.Marshal(map[string]*BackupMetadata{
		"postgres": pgMetadata,
		"vault":    vaultMetadata,
	})
	if err != nil {
		metadata.Status = BackupStatusFailed
		metadata.ErrorMessage = fmt.Sprintf("failed to build backup manifest: %v", err)
		return metadata, err
	}
	if err := b.storage.StoreBackup(ctx, metadata.ID, bytes.NewReader(manifest)); err != nil {
		metadata.Status = BackupStatusFailed
		metadata.ErrorMessage = fmt.Sprintf("failed to store backup manifest: %v", err)
		return metadata, err
	}

	// Mark as completed
	metadata.Status = BackupStatusCompleted
	metadata.EndTime = time.Now()
	metadata.Duration = metadata.EndTime.Sub(metadata.StartTime)
	metadata.RetentionDays = 30

	// Verify backup integrity
	if err := b.VerifyBackup(ctx, metadata); err != nil {
		metadata.Status = BackupStatusFailed
		metadata.ErrorMessage = fmt.Sprintf("Backup verification failed: %v", err)
		return metadata, err
	}

	return metadata, nil
}

// ExecuteIncrementalBackup executes an incremental backup
func (b *BackupManager) ExecuteIncrementalBackup(ctx context.Context, destination string, previousBackup *BackupMetadata) (*BackupMetadata, error) {
	metadata := &BackupMetadata{
		ID:          generateBackupID(),
		Type:        BackupTypeIncremental,
		Status:      BackupStatusInProgress,
		StartTime:   time.Now(),
		Tags:        make(map[string]string),
		BackupChain: []string{previousBackup.ID},
	}

	// Incremental PostgreSQL backup
	pgMetadata, err := b.postgres.BackupIncremental(ctx, destination, previousBackup.StartTime)
	if err != nil {
		metadata.Status = BackupStatusFailed
		metadata.ErrorMessage = fmt.Sprintf("Incremental PostgreSQL backup failed: %v", err)
		return metadata, err
	}
	metadata.Size += pgMetadata.Size

	// Incremental Vault backup
	vaultMetadata, err := b.vault.BackupIncremental(ctx, destination, previousBackup.StartTime)
	if err != nil {
		metadata.Status = BackupStatusFailed
		metadata.ErrorMessage = fmt.Sprintf("Incremental Vault backup failed: %v", err)
		return metadata, err
	}
	metadata.Size += vaultMetadata.Size

	metadata.Status = BackupStatusCompleted
	metadata.EndTime = time.Now()
	metadata.Duration = metadata.EndTime.Sub(metadata.StartTime)
	metadata.RetentionDays = 7 // Incremental backups retained 7 days

	return metadata, nil
}

// VerifyBackup verifies the integrity of a backup
func (b *BackupManager) VerifyBackup(ctx context.Context, metadata *BackupMetadata) error {
	metadata.Status = BackupStatusVerifying

	// Retrieve backup from storage
	data, err := b.storage.GetBackup(ctx, metadata.ID)
	if err != nil {
		return fmt.Errorf("failed to retrieve backup: %w", err)
	}

	// Compute checksum
	hash := md5.New()
	if _, err := io.Copy(hash, data); err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}
	checksum := fmt.Sprintf("%x", hash.Sum(nil))

	if metadata.Checksum != "" && metadata.Checksum != checksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", metadata.Checksum, checksum)
	}

	metadata.Checksum = checksum
	metadata.VerifiedAt = time.Now()
	metadata.Status = BackupStatusCompleted

	return nil
}

// ListRestorePoints lists available restore points
func (b *BackupManager) ListRestorePoints(ctx context.Context) ([]RestorePoint, error) {
	backups, err := b.storage.ListBackups(ctx)
	if err != nil {
		return nil, err
	}

	var restorePoints []RestorePoint
	for _, backup := range backups {
		// Build restore points from backup chains
		point := RestorePoint{
			ID:            backup.ID,
			Timestamp:     backup.StartTime,
			Source:        backup.Source,
			Type:          backup.Type,
			RecoveryPoint: backup.EndTime,
			Verified:      !backup.VerifiedAt.IsZero(),
			EstimatedRTO:  15 * time.Minute, // Typical RTO for restore
		}
		restorePoints = append(restorePoints, point)
	}

	return restorePoints, nil
}

// RestoreFromPoint restores from a specific point-in-time
func (b *BackupManager) RestoreFromPoint(ctx context.Context, restorePoint RestorePoint) error {
	// Get backup chain
	backups, err := b.storage.GetBackupChain(ctx, restorePoint.ID)
	if err != nil {
		return fmt.Errorf("failed to retrieve backup chain: %w", err)
	}

	// Restore full backup first
	for _, backup := range backups {
		if backup.Type == BackupTypeFull {
			// backup.ID is the combined manifest record's own identity
			// (see the matching comment in ExecuteFullBackup) -- it is
			// NOT a valid ID to hand to either component backend, since
			// each one generated and returned its own ID for where it
			// actually stored its data. BackupChain[0]/[1] carry those
			// real component IDs, in the same order ExecuteFullBackup
			// set them (postgres, then vault).
			if len(backup.BackupChain) != 2 {
				return fmt.Errorf("backup %s has no recorded component backup IDs (expected postgres+vault in BackupChain, got %d entries) -- was it created by ExecuteFullBackup?", backup.ID, len(backup.BackupChain))
			}
			postgresBackupID, vaultBackupID := backup.BackupChain[0], backup.BackupChain[1]

			if err := b.postgres.Restore(ctx, postgresBackupID); err != nil {
				return fmt.Errorf("failed to restore PostgreSQL: %w", err)
			}

			if err := b.vault.Restore(ctx, vaultBackupID); err != nil {
				return fmt.Errorf("failed to restore Vault: %w", err)
			}

			break
		}
	}

	// Apply incremental/differential backups in order
	for _, backup := range backups {
		if backup.Type == BackupTypeIncremental || backup.Type == BackupTypeDifferential {
			if err := b.postgres.RestoreIncremental(ctx, backup.ID); err != nil {
				return fmt.Errorf("failed to restore incremental backup: %w", err)
			}
		}
	}

	return nil
}

// BackupStorage interface for backup storage backend
type BackupStorage interface {
	StoreBackup(ctx context.Context, id string, data io.Reader) error
	GetBackup(ctx context.Context, id string) (io.Reader, error)
	DeleteBackup(ctx context.Context, id string) error
	ListBackups(ctx context.Context) ([]BackupMetadata, error)
	GetBackupChain(ctx context.Context, backupID string) ([]BackupMetadata, error)
}

// VaultBackup interface for Vault backup operations
type VaultBackup interface {
	BackupFull(ctx context.Context, destination string) (*BackupMetadata, error)
	BackupIncremental(ctx context.Context, destination string, since time.Time) (*BackupMetadata, error)
	Restore(ctx context.Context, backupID string) error
	RestoreIncremental(ctx context.Context, backupID string) error
}

// PostgreSQLBackup interface for PostgreSQL backup operations
type PostgreSQLBackup interface {
	BackupFull(ctx context.Context, destination string) (*BackupMetadata, error)
	BackupIncremental(ctx context.Context, destination string, since time.Time) (*BackupMetadata, error)
	Restore(ctx context.Context, backupID string) error
	RestoreIncremental(ctx context.Context, backupID string) error
}

// Utility functions

// generateBackupID generates a unique backup ID
func generateBackupID() string {
	return fmt.Sprintf("backup-%d", time.Now().UnixNano())
}
