package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Against a real immudb server, only when IMMUDB_TEST_ADDR names one (for
// example "127.0.0.1:3322"). It exists for client upgrades: the audit logger is
// best-effort, so a client that could no longer talk to the server would show
// up only as a warning in the log and a ledger that stopped growing.
func TestAuditRecordsReachImmudbAndVerify(t *testing.T) {
	addr := os.Getenv("IMMUDB_TEST_ADDR")
	if addr == "" {
		t.Skip("IMMUDB_TEST_ADDR not set; this test needs a running immudb server")
	}
	// The client keeps each server's identity and last verified state in
	// files in the working directory; a fresh one per run keeps a test server
	// that was wiped and restarted from looking like an impostor.
	t.Chdir(t.TempDir())

	audit, err := NewAuditLogger(addr, os.Getenv("IMMUDB_USER"), os.Getenv("IMMUDB_PASSWORD"))
	if err != nil {
		t.Fatalf("opening a session: %v", err)
	}
	defer func() {
		if err := audit.Close(); err != nil {
			t.Errorf("closing the session: %v", err)
		}
	}()

	ctx := context.Background()
	var last uint64
	for _, ev := range []AuditEvent{
		{Type: "SIGN_REQUEST_RECEIVED", RequestID: "upgrade-check-1", Status: "pending"},
		{Type: "SIGN_SUCCESS", RequestID: "upgrade-check-1", Status: "signed", Signature: "0xabc"},
	} {
		txID, err := audit.LogEvent(ctx, ev)
		if err != nil {
			t.Fatalf("writing %s: %v", ev.Type, err)
		}
		if txID <= last {
			t.Fatalf("transaction ids did not increase: %d after %d", txID, last)
		}
		last = txID
	}

	// The point of immudb is that a record can be proved untampered. Ask the
	// server for the last write with its proof and let the client check it.
	tx, err := audit.client.VerifiedTxByID(ctx, last)
	if err != nil {
		t.Fatalf("verifying transaction %d: %v", last, err)
	}
	found := false
	for _, e := range tx.Entries {
		if strings.HasPrefix(string(e.Key), "audit:") && strings.HasSuffix(string(e.Key), ":upgrade-check-1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("verified transaction %d does not hold the audit record", last)
	}
}
