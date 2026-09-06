package backup

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"testing"

	_ "github.com/lib/pq"

	vault "github.com/hashicorp/vault/api"
)

// TestFullBackupAndRestoreDrill is the real DR drill this package's
// backends were built to run, automated: real pg_dump, real pg_restore
// into an isolated database, real Vault KV export/import, with the
// restored data verified against the original -- not just "no error was
// returned." Requires real Postgres and Vault; skipped otherwise (see
// requireDrillEnv). This is what produced the measured numbers in
// docs/PHASE3-BACKUP-RECOVERY-PROCEDURES.md's reality-check header and
// docs/security/audit-checklist.md's DR section -- run it again to
// reproduce or update them, rather than trusting stale figures.
func TestFullBackupAndRestoreDrill(t *testing.T) {
	sourceDSN, restoreDSN, vaultAddr, vaultToken := requireDrillEnv(t)

	// app_admin lacks CREATEDB, so the isolated restore-target database is
	// created/dropped as the postgres superuser via sudo -- matching how
	// this was done by hand during the manual version of this drill.
	restoreDBName := "openfireblocks_drill_test"
	mustExec(t, "sudo", []string{"-u", "postgres", "psql", "-c", "DROP DATABASE IF EXISTS " + restoreDBName})
	mustExec(t, "sudo", []string{"-u", "postgres", "psql", "-c", "CREATE DATABASE " + restoreDBName + " OWNER app_admin"})
	t.Cleanup(func() {
		mustExec(t, "sudo", []string{"-u", "postgres", "psql", "-c", "DROP DATABASE IF EXISTS " + restoreDBName})
	})

	ctx := context.Background()

	pg := &RealPostgreSQLBackup{ConnURI: sourceDSN, RestoreURI: restoreDSN, DumpDir: t.TempDir()}

	client, err := vault.NewClient(&vault.Config{Address: vaultAddr})
	if err != nil {
		t.Fatalf("failed to create vault client: %v", err)
	}
	client.SetToken(vaultToken)
	vb := &RealVaultBackup{Client: client, Mount: "secret", DumpDir: t.TempDir()}

	// Seed a canary secret so the drill has something concrete to verify
	// survived the Vault backup/restore round trip.
	canaryPath := "openfireblocks/drill-test/canary"
	if _, err := client.Logical().Write("secret/data/"+canaryPath, map[string]interface{}{
		"data": map[string]interface{}{"value": "drill-canary"},
	}); err != nil {
		t.Fatalf("failed to seed canary secret: %v", err)
	}

	storage := &FilesystemBackupStorage{Dir: t.TempDir()}
	manager := NewBackupManager(storage, vb, pg)

	// --- Backup ---
	meta, err := manager.ExecuteFullBackup(ctx, "local")
	if err != nil {
		t.Fatalf("ExecuteFullBackup failed: %v", err)
	}
	if meta.Status != BackupStatusCompleted {
		t.Fatalf("expected backup to complete, got status %s: %s", meta.Status, meta.ErrorMessage)
	}
	if err := storage.SaveMetadata(meta); err != nil {
		t.Fatalf("failed to save backup metadata: %v", err)
	}
	t.Logf("real backup completed in %s, %d bytes (postgres+vault combined)", meta.Duration, meta.Size)

	// Delete the canary before restoring, so restoring it back is a real
	// assertion, not a no-op against data that was never removed.
	if _, err := client.Logical().Delete("secret/data/" + canaryPath); err != nil {
		t.Fatalf("failed to delete canary secret: %v", err)
	}

	// --- Restore ---
	points, err := manager.ListRestorePoints(ctx)
	if err != nil {
		t.Fatalf("ListRestorePoints failed: %v", err)
	}
	var target *RestorePoint
	for i := range points {
		if points[i].ID == meta.ID {
			target = &points[i]
		}
	}
	if target == nil {
		t.Fatalf("restore point for backup %s not found", meta.ID)
	}

	if err := manager.RestoreFromPoint(ctx, *target); err != nil {
		t.Fatalf("RestoreFromPoint failed: %v", err)
	}

	// --- Verify: Postgres data matches ---
	srcCount := countRows(t, sourceDSN, "customers")
	restoredCount := countRows(t, restoreDSN, "customers")
	if srcCount != restoredCount {
		t.Fatalf("row count mismatch after restore: source has %d customers, restored has %d", srcCount, restoredCount)
	}
	if srcCount == 0 {
		t.Fatal("source database has zero customers -- this drill needs at least one row to actually verify anything")
	}

	// --- Verify: Vault secret restored ---
	secret, err := client.Logical().Read("secret/data/" + canaryPath)
	if err != nil || secret == nil || secret.Data == nil {
		t.Fatalf("canary secret not found after restore: %v", err)
	}
	data, _ := secret.Data["data"].(map[string]interface{})
	if data["value"] != "drill-canary" {
		t.Fatalf("canary secret value mismatch after restore: got %v", data["value"])
	}

	t.Logf("SUCCESS: real backup+restore drill verified %d customer rows and 1 Vault secret round-tripped correctly", srcCount)
}

func requireDrillEnv(t *testing.T) (sourceDSN, restoreDSN, vaultAddr, vaultToken string) {
	t.Helper()
	vaultAddr = os.Getenv("VAULT_ADDR")
	if vaultAddr == "" {
		t.Skip("VAULT_ADDR not set; this test needs real Postgres and Vault")
	}
	vaultToken = os.Getenv("VAULT_TOKEN")
	sourceDSN = os.Getenv("DRILL_SOURCE_DATABASE_URL")
	if sourceDSN == "" {
		sourceDSN = "postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}
	restoreDSN = "postgres://app_admin:dev-only@localhost:5432/openfireblocks_drill_test?sslmode=disable"
	return sourceDSN, restoreDSN, vaultAddr, vaultToken
}

func mustExec(t *testing.T, name string, args []string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "PGPASSWORD=dev-only")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v: %s", name, args, err, out)
	}
}

func countRows(t *testing.T, dsn, table string) int {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("failed to open %s: %v", dsn, err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("failed to count rows in %s: %v", table, err)
	}
	return count
}
