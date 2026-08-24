package activities

// Real-Postgres integration test for ActivateKeyPair/SetKeyPairStatus --
// no build tag, following services/backup/integration_test.go's
// convention: skip (not fail) if a real database isn't reachable, so this
// only runs where one actually is.
//
//	DATABASE_URL=postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable \
//	  go test -run TestKeyPairStatusTransitions -v ./activities/...

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"forge-crypto/temporal-worker/workflows"
)

func requireLiveDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("cannot open database connection, skipping: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("database not reachable, skipping: %v", err)
	}
	return db
}

// TestKeyPairStatusTransitions runs the real rotation-relevant key_pairs
// transitions against a real Postgres instance: create a customer + a
// pending_dkg key_pairs row (standing in for the old key), a second
// pending_dkg row (the new key), then drive them through exactly the
// transitions KeyRotationWorkflow performs -- ActivateKeyPair on the new
// row, SetKeyPairStatus('inactive') on the old one -- and verify with a
// direct SELECT that both landed, not just that the activity calls
// returned no error.
func TestKeyPairStatusTransitions(t *testing.T) {
	db := requireLiveDB(t)
	defer db.Close()

	ctx := context.Background()

	var customerID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO customers (name, api_key_hash) VALUES ($1, decode(md5(clock_timestamp()::text || random()::text), 'hex')) RETURNING customer_id`,
		"key-rotation-test-customer",
	).Scan(&customerID); err != nil {
		t.Fatalf("failed to seed customer: %v", err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM customers WHERE customer_id = $1`, customerID) })

	var oldKeyID, newKeyID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO key_pairs (customer_id, name, blockchain, threshold, total_parties, status) VALUES ($1, $2, 'ethereum', 1, 3, 'active') RETURNING key_id`,
		customerID, "old-key",
	).Scan(&oldKeyID); err != nil {
		t.Fatalf("failed to seed old key_pairs row: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`INSERT INTO key_pairs (customer_id, name, blockchain, threshold, total_parties, status) VALUES ($1, $2, 'ethereum', 1, 3, 'pending_dkg') RETURNING key_id`,
		customerID, "new-key",
	).Scan(&newKeyID); err != nil {
		t.Fatalf("failed to seed new key_pairs row: %v", err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM key_pairs WHERE key_id IN ($1, $2)`, oldKeyID, newKeyID) })

	a := NewActivities("", "", "", 3, db)

	const newAddress = "0x7777777777777777777777777777777777777777"
	const newPubKey = "0xrotationtestpubkey"
	if err := a.ActivateKeyPair(ctx, workflows.ActivateKeyPairRequest{
		KeyID: newKeyID, Address: newAddress, PublicKey: newPubKey,
	}); err != nil {
		t.Fatalf("ActivateKeyPair returned an error: %v", err)
	}
	if err := a.SetKeyPairStatus(ctx, workflows.SetKeyPairStatusRequest{
		KeyID: oldKeyID, Status: "inactive",
	}); err != nil {
		t.Fatalf("SetKeyPairStatus returned an error: %v", err)
	}

	var status, address, pubKey string
	if err := db.QueryRowContext(ctx, `SELECT status, COALESCE(address,''), COALESCE(public_key,'') FROM key_pairs WHERE key_id = $1`, newKeyID).
		Scan(&status, &address, &pubKey); err != nil {
		t.Fatalf("failed to read back new key_pairs row: %v", err)
	}
	if status != "active" || address != newAddress || pubKey != newPubKey {
		t.Fatalf("new key_pairs row not activated correctly: status=%s address=%s public_key=%s", status, address, pubKey)
	}

	if err := db.QueryRowContext(ctx, `SELECT status FROM key_pairs WHERE key_id = $1`, oldKeyID).Scan(&status); err != nil {
		t.Fatalf("failed to read back old key_pairs row: %v", err)
	}
	if status != "inactive" {
		t.Fatalf("expected old key_pairs row status 'inactive', got %s", status)
	}

	// Also prove the not-found path is real, not silently accepted.
	if err := a.SetKeyPairStatus(ctx, workflows.SetKeyPairStatusRequest{KeyID: "00000000-0000-0000-0000-000000000000", Status: "inactive"}); err == nil {
		t.Fatal("expected SetKeyPairStatus on a nonexistent key_id to return an error")
	}

	t.Logf("SUCCESS: real Postgres key_pairs transitions verified -- new key %s activated (%s), old key %s marked inactive", newKeyID, newAddress, oldKeyID)
}
