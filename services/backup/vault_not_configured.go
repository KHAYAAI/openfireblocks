package backup

import (
	"context"
	"errors"
	"time"
)

// ErrVaultNotConfigured is returned by every method of
// vaultNotConfiguredBackup -- used in place of a nil VaultBackup so a
// missing VAULT_ADDR fails with a clear, specific error instead of a nil
// pointer panic the first time BackupManager tries to call the Vault
// half of a full backup.
var ErrVaultNotConfigured = errors.New("vault backup not configured: VAULT_ADDR is not set")

type vaultNotConfiguredBackup struct{}

// NewVaultNotConfiguredBackup returns a VaultBackup that fails closed with
// ErrVaultNotConfigured on every call. Use this instead of a nil
// VaultBackup when Vault genuinely isn't configured for a given run.
func NewVaultNotConfiguredBackup() VaultBackup { return vaultNotConfiguredBackup{} }

func (vaultNotConfiguredBackup) BackupFull(ctx context.Context, destination string) (*BackupMetadata, error) {
	return nil, ErrVaultNotConfigured
}
func (vaultNotConfiguredBackup) BackupIncremental(ctx context.Context, destination string, since time.Time) (*BackupMetadata, error) {
	return nil, ErrVaultNotConfigured
}
func (vaultNotConfiguredBackup) Restore(ctx context.Context, backupID string) error {
	return ErrVaultNotConfigured
}
func (vaultNotConfiguredBackup) RestoreIncremental(ctx context.Context, backupID string) error {
	return ErrVaultNotConfigured
}
