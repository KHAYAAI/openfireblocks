package backup

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// Live test of real streaming-replication failover. Requires an actual
// standby -- skips otherwise, matching this package's existing convention.
//
// Set up (what this test was verified against):
//
//	pg_basebackup -h 127.0.0.1 -U replicator -D <standby> -Fp -Xs -R -P
//	# point the standby at port 5433, start it
//	STANDBY_DATABASE_URL=postgres://app_admin:...@127.0.0.1:5433/openfireblocks?sslmode=disable \
//	  go test -run TestLivePostgresFailover -v ./...
//
// This is destructive to the standby: promotion is one-way. The standby has
// to be rebuilt from the primary afterwards, which is exactly what a real
// failback would require too.
func standbyDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("STANDBY_DATABASE_URL")
	if dsn == "" {
		t.Skip("STANDBY_DATABASE_URL not set; skipping live failover test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("cannot open standby: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("standby unreachable: %v", err)
	}
	return dsn
}

// The real thing: promote an actual standby and verify it becomes a
// writable primary.
func TestLivePostgresFailover_PromotesStandby(t *testing.T) {
	if os.Getenv("RUN_DESTRUCTIVE_FAILOVER") != "true" {
		t.Skip("set RUN_DESTRUCTIVE_FAILOVER=true to run this: promotion is one-way and the standby must be rebuilt afterwards")
	}
	dsn := standbyDSN(t)

	pf := &PostgresFailover{StandbyDSN: dsn, Force: true}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open standby: %v", err)
	}
	defer db.Close()

	var inRecoveryBefore bool
	if err := db.QueryRow(`SELECT pg_is_in_recovery()`).Scan(&inRecoveryBefore); err != nil {
		t.Fatalf("check recovery state: %v", err)
	}
	if !inRecoveryBefore {
		t.Skip("target is already a primary; rebuild the standby before running this test")
	}

	// A standby must reject writes before promotion -- otherwise this test
	// proves nothing about what promotion changed. The rejection must be
	// specifically because the server is read-only: a permissions error here
	// would make the check pass for the wrong reason.
	_, preErr := db.Exec(`INSERT INTO dr_replication_probe(note) VALUES ('should fail on a standby')`)
	if preErr == nil {
		t.Fatal("a standby accepted a write before promotion; it is not a real read-only replica")
	}
	if !strings.Contains(preErr.Error(), "read-only") {
		t.Fatalf("standby rejected the write for the wrong reason (want a read-only transaction error): %v", preErr)
	}

	result, err := pf.Promote(context.Background(), 0)
	if err != nil {
		t.Fatalf("Promote failed: %v", err)
	}
	if !result.Promoted {
		t.Fatalf("expected promotion, got %+v", result)
	}
	if !result.WasInRecovery {
		t.Error("expected WasInRecovery to be true")
	}
	if !result.WritableAfter {
		t.Error("expected the promoted server to be writable")
	}

	// The proof: the same connection that refused a write above must now
	// accept one.
	if _, err := db.Exec(`INSERT INTO dr_replication_probe(note) VALUES ('written after promotion')`); err != nil {
		t.Fatalf("promoted server still rejects writes: %v", err)
	}

	// And the data replicated before promotion must have survived it.
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM dr_replication_probe WHERE note = 'written on primary before failover'`).Scan(&count); err != nil {
		t.Fatalf("failed to read pre-failover data: %v", err)
	}
	if count == 0 {
		t.Error("data written on the primary before failover did not survive promotion")
	}

	t.Logf("SUCCESS: promoted a real streaming-replication standby in %s (lag at promotion: %s); pre-failover data intact and the server now accepts writes",
		result.PromotionTime, result.LagAtPromotion)
}
