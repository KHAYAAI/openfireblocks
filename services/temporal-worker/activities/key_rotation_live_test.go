//go:build live

package activities

// Live integration test for DeactivateOldKeyShares against a real Vault
// dev server -- seeds fake sealed-share secrets at exactly the paths
// services/mpc-party/vault_seal.go writes real ones to (see this file's
// own doc comment for why this is a second, independent path derivation
// rather than an import), calls the activity, and confirms via a direct
// read that the secret is genuinely gone (soft-deleted) afterward, not
// just that the call returned no error.
//
//	vault server -dev -dev-root-token-id=dev-root-token &
//	VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN=dev-root-token \
//	  go test -tags live -run TestLiveDeactivateOldKeyShares -v ./activities/...

import (
	"context"
	"os"
	"strconv"
	"testing"

	vault "github.com/hashicorp/vault/api"

	"forge-crypto/temporal-worker/workflows"
)

func TestLiveDeactivateOldKeyShares(t *testing.T) {
	addr := os.Getenv("VAULT_ADDR")
	if addr == "" {
		t.Skip("VAULT_ADDR not set; skipping live Vault test")
	}

	client, err := vault.NewClient(&vault.Config{Address: addr})
	if err != nil {
		t.Fatalf("vault client: %v", err)
	}
	if token := os.Getenv("VAULT_TOKEN"); token != "" {
		client.SetToken(token)
	}
	kv := client.KVv2(getenvDefault("VAULT_KV_MOUNT", "secret"))
	basePath := getenvDefault("VAULT_KEY_SHARE_PATH", "openfireblocks/mpc-party")
	ceremonyID := "live-deactivate-test-ceremony"
	partyIDs := []int{1, 2, 3}

	// Seed fake sealed shares -- real content doesn't matter, this
	// activity never reads it, only deletes by path.
	for _, partyID := range partyIDs {
		path := basePathFor(basePath, partyID, ceremonyID)
		if _, err := kv.Put(context.Background(), path, map[string]interface{}{
			"party_id":    partyID,
			"ceremony_id": ceremonyID,
			"save_data":   "fake-share-material-for-test",
		}); err != nil {
			t.Fatalf("failed to seed fake share at %s: %v", path, err)
		}
	}

	// Confirm they're readable before deactivation, so the "gone after"
	// check below is meaningful.
	for _, partyID := range partyIDs {
		path := basePathFor(basePath, partyID, ceremonyID)
		secret, err := kv.Get(context.Background(), path)
		if err != nil || secret == nil || secret.Data == nil {
			t.Fatalf("seeded share at %s not readable before deactivation: %v", path, err)
		}
	}

	a := NewActivities("", "", "", 3, nil)
	result, err := a.DeactivateOldKeyShares(context.Background(), workflows.DeactivateSharesRequest{
		CeremonyID: ceremonyID,
		PartyIDs:   partyIDs,
	})
	if err != nil {
		t.Fatalf("DeactivateOldKeyShares returned an error: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("DeactivateOldKeyShares reported per-party errors: %v", result.Errors)
	}
	if result.DeactivatedCount != len(partyIDs) {
		t.Fatalf("expected DeactivatedCount=%d, got %d", len(partyIDs), result.DeactivatedCount)
	}

	// The real proof: read each path again and confirm Vault now reports
	// it deleted (KV v2 soft delete: Get on a deleted version returns a
	// non-nil secret with nil/empty Data and delete metadata, not the
	// original content).
	for _, partyID := range partyIDs {
		path := basePathFor(basePath, partyID, ceremonyID)
		secret, err := kv.Get(context.Background(), path)
		if err != nil {
			t.Fatalf("unexpected error reading deactivated share at %s: %v", path, err)
		}
		if secret != nil && len(secret.Data) > 0 {
			t.Fatalf("share at %s is still readable after DeactivateOldKeyShares -- soft delete did not take effect: %+v", path, secret.Data)
		}
	}

	t.Logf("SUCCESS: DeactivateOldKeyShares soft-deleted %d real shares in Vault, confirmed unreadable by an independent follow-up read", result.DeactivatedCount)
}

func basePathFor(base string, partyID int, ceremonyID string) string {
	return base + "/party-" + strconv.Itoa(partyID) + "/" + ceremonyID
}
