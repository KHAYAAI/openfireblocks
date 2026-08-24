// Command backup-server is the real entrypoint for services/backup.
// Previously that package (backup_manager.go, disaster_recovery.go) had no
// func main() anywhere in it -- two files of orchestration logic with no
// way to actually invoke them, no binary, no cron, nothing. This wires
// them to real backends (pg_dump/pg_restore for Postgres, a recursive KV
// export for Vault, local disk for storage -- see postgres_backup.go,
// vault_backup.go, filesystem_storage.go for what's real vs. not yet in
// each) and exposes them over HTTP.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	vaultapi "github.com/hashicorp/vault/api"

	backup "forge-crypto/backup"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dumpDir := getenv("BACKUP_DUMP_DIR", "/var/lib/openfireblocks-backup")
	connURI := getenv("DATABASE_URL", "postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable")
	restoreURI := getenv("RESTORE_DATABASE_URL", connURI)

	pg := &backup.RealPostgreSQLBackup{ConnURI: connURI, RestoreURI: restoreURI, DumpDir: dumpDir + "/postgres"}
	storage := &backup.FilesystemBackupStorage{Dir: dumpDir + "/meta"}

	vaultBackend := backup.NewVaultNotConfiguredBackup()
	if addr := os.Getenv("VAULT_ADDR"); addr != "" {
		client, err := vaultapi.NewClient(&vaultapi.Config{Address: addr})
		if err != nil {
			log.Fatalf("failed to create vault client: %v", err)
		}
		if token := os.Getenv("VAULT_TOKEN"); token != "" {
			client.SetToken(token)
		}
		vaultBackend = &backup.RealVaultBackup{
			Client:  client,
			Mount:   getenv("VAULT_KV_MOUNT", "secret"),
			DumpDir: dumpDir + "/vault",
		}
	} else {
		log.Printf("VAULT_ADDR not set: Vault backup/restore endpoints will fail closed with a clear error rather than silently skip Vault")
	}

	manager := backup.NewBackupManager(storage, vaultBackend, pg)
	drCoordinator := backup.NewDisasterRecoveryCoordinator(manager)

	srv := &backupServer{manager: manager, storage: storage, dr: drCoordinator}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "backup"})
	})
	mux.HandleFunc("/backup/full", srv.handleBackupFull)
	mux.HandleFunc("/backup/incremental", srv.handleBackupIncremental)
	mux.HandleFunc("/restore", srv.handleRestore)
	mux.HandleFunc("/restore-points", srv.handleRestorePoints)
	mux.HandleFunc("/dr/plan", srv.handleCreateDRPlan)
	mux.HandleFunc("/dr/test", srv.handleTestDR)
	mux.HandleFunc("/dr/failover", srv.handleInitiateFailover)
	mux.HandleFunc("/dr/failover/status", srv.handleFailoverStatus)

	port := getenv("PORT", "8088")
	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("backup service listening on :%s (dump dir: %s)", port, dumpDir)
	log.Fatal(httpSrv.ListenAndServe())
}
