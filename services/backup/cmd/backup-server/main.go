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
	"strings"
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
	connURI := mustDSN("DATABASE_URL", "postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable")
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

	// Real cross-region failover for the component that holds customer
	// data. Without STANDBY_DATABASE_URL the coordinator has no replica to
	// promote and reports the postgres component failed with that reason,
	// rather than claiming a failover it did not perform.
	if standbyURI := os.Getenv("STANDBY_DATABASE_URL"); standbyURI != "" {
		drCoordinator.SetPostgresFailover(&backup.PostgresFailover{
			StandbyDSN: standbyURI,
			// Promotion discards anything the standby has not received, so
			// it is refused past the DR plan's RPO unless FAILOVER_FORCE is
			// set to accept the data loss deliberately.
			Force: os.Getenv("FAILOVER_FORCE") == "true",
		})
		log.Printf("cross-region Postgres failover configured (standby: %s)", redactDSN(standbyURI))
	} else {
		log.Printf("no STANDBY_DATABASE_URL set: Postgres failover is unavailable and will report failed if a failover is initiated")
	}

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

// redactDSN strips credentials before a DSN reaches the log.
func redactDSN(dsn string) string {
	if at := strings.LastIndex(dsn, "@"); at != -1 {
		if scheme := strings.Index(dsn, "://"); scheme != -1 && scheme+3 < at {
			return dsn[:scheme+3] + "***@" + dsn[at+1:]
		}
	}
	return dsn
}

// mustDSN returns the DSN from env, or the local development fallback.
//
// Deliberately NOT written as getenv(key, devFallback(...)): Go evaluates
// arguments eagerly, so that shape would consult (and reject) the fallback
// even when the environment variable is correctly set. The fallback carries
// a well-known password, so it must only ever be reached when the variable
// is genuinely absent -- and in a deployed environment that is a hard
// failure rather than a silent localhost connection attempt. Flagged by
// gosec G101.
func mustDSN(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	for _, k := range []string{"APP_ENV", "ENVIRONMENT"} {
		switch os.Getenv(k) {
		case "production", "prod":
			log.Fatalf("%s is not set and this is a production environment (APP_ENV/ENVIRONMENT); refusing to fall back to local development credentials", envVar)
		}
	}
	return fallback
}
