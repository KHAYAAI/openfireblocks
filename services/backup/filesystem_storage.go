package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// FilesystemBackupStorage implements BackupStorage on local disk. Real and
// fully testable without any cloud credentials, which S3 (what a
// production deployment should actually use -- BackupStorage is defined
// as an interface specifically so swapping in an S3 implementation later
// doesn't touch BackupManager/DisasterRecoveryCoordinator at all) can't be
// in this environment. See docs/PHASE3-BACKUP-RECOVERY-PROCEDURES.md's
// reality-check header: no S3 bucket for this purpose is provisioned in
// Terraform yet.
type FilesystemBackupStorage struct {
	Dir string
}

func (f *FilesystemBackupStorage) metaPath(id string) string {
	return filepath.Join(f.Dir, id+".meta.json")
}

func (f *FilesystemBackupStorage) dataPath(id string) string {
	return filepath.Join(f.Dir, id+".data")
}

// StoreBackup and GetBackup handle the metadata+data pairing the
// BackupManager doesn't otherwise persist anywhere -- ExecuteFullBackup
// builds a BackupMetadata in memory and returns it to its caller, but
// nothing about that flow writes it to durable storage on its own; that's
// this type's job when the HTTP layer (main.go) calls SaveMetadata after a
// successful backup.
func (f *FilesystemBackupStorage) SaveMetadata(meta *BackupMetadata) error {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	return os.WriteFile(f.metaPath(meta.ID), raw, 0o600)
}

func (f *FilesystemBackupStorage) StoreBackup(ctx context.Context, id string, data io.Reader) error {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}
	out, err := os.Create(f.dataPath(id))
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, data); err != nil {
		return fmt.Errorf("failed to write backup data: %w", err)
	}
	return nil
}

func (f *FilesystemBackupStorage) GetBackup(ctx context.Context, id string) (io.Reader, error) {
	data, err := os.ReadFile(f.dataPath(id))
	if err != nil {
		return nil, fmt.Errorf("backup %s not found: %w", id, err)
	}
	return &byteReader{data: data}, nil
}

func (f *FilesystemBackupStorage) DeleteBackup(ctx context.Context, id string) error {
	_ = os.Remove(f.dataPath(id))
	return os.Remove(f.metaPath(id))
}

func (f *FilesystemBackupStorage) ListBackups(ctx context.Context) ([]BackupMetadata, error) {
	entries, err := os.ReadDir(f.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list storage directory: %w", err)
	}

	var out []BackupMetadata
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(f.Dir, e.Name()))
		if err != nil {
			continue
		}
		var meta BackupMetadata
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartTime.Before(out[j].StartTime) })
	return out, nil
}

func (f *FilesystemBackupStorage) GetBackupChain(ctx context.Context, backupID string) ([]BackupMetadata, error) {
	all, err := f.ListBackups(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]BackupMetadata, len(all))
	for _, m := range all {
		byID[m.ID] = m
	}

	target, ok := byID[backupID]
	if !ok {
		return nil, fmt.Errorf("backup %s not found", backupID)
	}

	chain := []BackupMetadata{target}
	for _, parentID := range target.BackupChain {
		if parent, ok := byID[parentID]; ok {
			chain = append(chain, parent)
		}
	}
	return chain, nil
}

type byteReader struct {
	data []byte
	pos  int
}

func (b *byteReader) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}
