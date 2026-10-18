package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The checksums of a backup's files, stored beside them.
//
// FetchOffsite refuses to restore anything it cannot check, and it checks
// against the SHA-256 of each file taken when the backup was made. Those used
// to be recorded in one place only: the backup volume inside the cluster.
// After losing the whole cluster, which is the case an off-site copy exists
// for, they were gone with it, and the bucket's contents could not be fetched
// through the service unless someone had saved the checksums of that
// particular run elsewhere. The nightly job saves them nowhere. The first
// rebuild test (docs/evidence/2026-10-18-dr-rebuild-fresh-server.md) worked
// only because a manual backup's checksums had been copied off the server.
//
// So the receipts go into the bucket too, as a fourth object encrypted with
// the same key and the same authenticated format as the files. What that adds
// and what it does not:
//   - Anyone without the key cannot read or forge it, so a restore still
//     needs the key and nothing the bucket's owner could change.
//   - It still catches an object swapped for a different (validly encrypted)
//     one from another backup, which GCM alone would accept.
//   - It does not stop someone who holds both the key and write access from
//     replacing the whole set with an older one. The bucket's object lock is
//     what stands against that, and the key is not kept in the bucket.

// checksumsName is the object stored beside postgres, vault and manifest.
const checksumsName = "checksums"

// ChecksumRecord is what is stored: one receipt per file of one backup.
type ChecksumRecord struct {
	BackupID string                    `json:"backup_id"`
	Files    map[string]OffsiteReceipt `json:"files"`
}

// backupFileNames are the files a restore needs, in the order they are fetched.
var backupFileNames = []string{"postgres", "vault", "manifest"}

// PutChecksums encrypts the record and stores it beside the backup's files.
func (o *OffsiteStore) PutChecksums(ctx context.Context, rec ChecksumRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "checksums-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_, err = o.Put(ctx, rec.BackupID, checksumsName, tmp.Name())
	return err
}

// GetChecksums reads the record back. It fails if the object is missing,
// altered, truncated, encrypted under another key, belongs to another backup,
// or does not cover every file a restore needs.
func (o *OffsiteStore) GetChecksums(ctx context.Context, backupID string) (*ChecksumRecord, error) {
	dir, err := os.MkdirTemp("", "checksums-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, checksumsName)
	if err := o.Get(ctx, backupID, checksumsName, dest, nil); err != nil {
		return nil, fmt.Errorf("reading the stored checksums for %s: %w", backupID, err)
	}
	raw, err := os.ReadFile(dest)
	if err != nil {
		return nil, err
	}
	var rec ChecksumRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("the stored checksums for %s are not valid: %w", backupID, err)
	}
	if rec.BackupID != backupID {
		return nil, fmt.Errorf("the stored checksums are for backup %q, not %q", rec.BackupID, backupID)
	}
	for _, name := range backupFileNames {
		if rec.Files[name].PlaintextSHA256 == "" {
			return nil, errors.New("the stored checksums do not cover " + name)
		}
	}
	return &rec, nil
}
