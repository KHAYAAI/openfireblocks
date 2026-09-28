package backup

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// RealPostgreSQLBackup implements PostgreSQLBackup using pg_dump/pg_restore
// (custom format, -Fc) shelled out to the real binaries -- not a mock, not
// a stub. Full backups are real logical dumps of the whole database.
//
// Incremental backups are honestly NOT true WAL-based incrementals: a real
// incremental Postgres backup needs continuous WAL archiving
// (archive_mode=on + an archive_command + a base backup via
// pg_basebackup), which requires control over the server's own
// postgresql.conf and a restart -- appropriate for a dedicated backup
// service to configure once, not something this client-side struct should
// silently reach into a running server to change. Until that's wired up
// (see docs/security/audit-checklist.md), BackupIncremental here produces
// another full pg_dump -- correct data, honestly not space/time-efficient
// the way true incrementals would be.
type RealPostgreSQLBackup struct {
	// ConnURI is a full postgres:// connection string, e.g.
	// "postgres://app_admin:...@localhost:5432/openfireblocks?sslmode=disable" --
	// both pg_dump and pg_restore accept a connection URI as their
	// positional dbname argument. A role with read access to every table
	// being backed up is required (app_admin, given RLS -- see migration 011).
	ConnURI string
	// RestoreURI is the connection URI to restore INTO -- deliberately a
	// separate field from ConnURI rather than reusing it, so a restore
	// drill can target a different (e.g. freshly created) database without
	// risking overwriting the one just backed up by accident.
	RestoreURI string
	// DumpDir is where backup files are written locally. A production
	// deployment would follow this with an upload to S3 (BackupStorage's
	// job, not this type's) -- see docs/PHASE3-BACKUP-RECOVERY-PROCEDURES.md's
	// reality-check header for what's provisioned vs. not.
	DumpDir string
}

func (r *RealPostgreSQLBackup) dumpPath(id string) string {
	return filepath.Join(r.DumpDir, id+".pgdump")
}

func (r *RealPostgreSQLBackup) run(ctx context.Context, name string, args []string) (stdout, stderr bytes.Buffer, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return stdout, stderr, err
}

// BackupFull runs a real pg_dump -Fc (custom format: compressed,
// selectively restorable, the format pg_restore expects).
func (r *RealPostgreSQLBackup) BackupFull(ctx context.Context, destination string) (*BackupMetadata, error) {
	if err := os.MkdirAll(r.DumpDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create dump directory: %w", err)
	}

	id := generateBackupID()
	path := r.dumpPath(id)
	start := time.Now()

	_, stderr, err := r.run(ctx, "pg_dump", []string{r.ConnURI, "-Fc", "-f", path})
	if err != nil {
		return nil, fmt.Errorf("pg_dump failed: %w: %s", err, stderr.String())
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("dump file missing after pg_dump reported success: %w", err)
	}

	checksum, err := md5File(path)
	if err != nil {
		return nil, fmt.Errorf("failed to checksum dump: %w", err)
	}

	return &BackupMetadata{
		ID:          id,
		Type:        BackupTypeFull,
		Status:      BackupStatusCompleted,
		Source:      "postgres",
		Destination: path,
		Size:        info.Size(),
		Checksum:    checksum,
		StartTime:   start,
		EndTime:     time.Now(),
		Duration:    time.Since(start),
		Tags:        map[string]string{"method": "pg_dump -Fc"},
	}, nil
}

// BackupIncremental produces another full pg_dump -- see the type doc
// comment for why this isn't a true WAL-based incremental yet.
func (r *RealPostgreSQLBackup) BackupIncremental(ctx context.Context, destination string, since time.Time) (*BackupMetadata, error) {
	meta, err := r.BackupFull(ctx, destination)
	if err != nil {
		return nil, err
	}
	meta.Type = BackupTypeIncremental
	meta.Tags["note"] = "full logical dump, not a true WAL-based incremental -- see RealPostgreSQLBackup's doc comment"
	return meta, nil
}

// Restore runs a real pg_restore --clean --if-exists against the target
// database named in ConnArgs.
func (r *RealPostgreSQLBackup) Restore(ctx context.Context, backupID string) error {
	path := r.dumpPath(backupID)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("backup file for %s not found: %w", backupID, err)
	}

	// --no-owner / --no-privileges: skip ownership and GRANT/ALTER DEFAULT
	// PRIVILEGES statements captured in the dump. Found by actually running
	// a restore drill (see docs/security/audit-checklist.md): pg_dump
	// captures the source role's (app_admin's) default-privilege grants,
	// and replaying "ALTER DEFAULT PRIVILEGES ... GRANT ALL ON TABLES TO
	// app_admin" during restore requires being the schema owner
	// (postgres), which the restoring role legitimately isn't in a
	// least-privilege setup -- pg_restore treats it as a non-fatal warning
	// and continues, but without --no-privileges it still exits non-zero,
	// which this method would otherwise (correctly, in general) treat as
	// a hard failure.
	args := []string{"-d", r.RestoreURI, "--clean", "--if-exists", "--no-owner", "--no-privileges", path}
	_, stderr, err := r.run(ctx, "pg_restore", args)
	if err != nil {
		// pg_restore exits non-zero on warnings (e.g. "role does not
		// exist" for ownership it can't restore) as well as real
		// failures; a caller that needs to distinguish those should
		// inspect stderr. Returning the error is still correct here --
		// silently swallowing pg_restore's exit code would hide a
		// genuine restore failure.
		return fmt.Errorf("pg_restore failed: %w: %s", err, stderr.String())
	}
	return nil
}

// RestoreIncremental restores an incremental backup file the same way as
// a full one, since BackupIncremental currently produces a full dump.
func (r *RealPostgreSQLBackup) RestoreIncremental(ctx context.Context, backupID string) error {
	return r.Restore(ctx, backupID)
}

func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
