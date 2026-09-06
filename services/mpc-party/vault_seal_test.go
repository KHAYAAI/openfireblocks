package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// requireVaultEnv skips the test unless a real Vault is configured --
// these tests write and read real secrets and are meant to run against a
// real `vault server -dev` instance, not mocked. Run with:
//
//	VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN=<token> go test -run TestSeal -v
func requireVaultEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("VAULT_ADDR") == "" {
		t.Skip("VAULT_ADDR not set; this test needs a real Vault (e.g. `vault server -dev`)")
	}
}

// TestSealAndLoadKeyShareRoundTrip proves SealKeyShare/LoadKeyShare
// round-trip real key-share material through a real Vault correctly: what
// comes back out is byte-for-byte the same sensitive data that went in,
// not a mock or a stub. Runs a real (n=2, threshold=1) DKG over real HTTP
// first, so the "key share" under test is real tss-lib output -- including
// the PaillierSK, Ks, and BigXj fields that actually matter -- not
// fabricated test data that might happen to round-trip through JSON
// without exercising the same code path production sealing uses.
func TestSealAndLoadKeyShareRoundTrip(t *testing.T) {
	requireVaultEnv(t)
	if testing.Short() {
		t.Skip("real DKG takes real time; skipped in -short")
	}

	const partyID = 101
	const otherPartyID = 102
	const ceremonyID = "vault-seal-roundtrip-test"

	managers := make(map[int]*TSSPartyManager, 2)
	peers := make(map[int]string, 2)
	for _, id := range []int{partyID, otherPartyID} {
		mgr := NewTSSPartyManager(id, &http.Client{Timeout: 10 * time.Second})
		managers[id] = mgr

		router := mux.NewRouter()
		ps := &PartyServer{partyID: id, tssManager: mgr}
		router.HandleFunc("/tss/keygen/message", ps.HandleTSSKeygenMessage).Methods(http.MethodPost)
		server := httptest.NewServer(router)
		peers[id] = server.URL
		t.Cleanup(server.Close)
	}

	for _, id := range []int{partyID, otherPartyID} {
		if err := managers[id].StartKeygen(ceremonyID, 1, peers); err != nil {
			t.Fatalf("party %d: StartKeygen failed: %v", id, err)
		}
	}

	deadline := time.Now().Add(3 * time.Minute)
	for {
		status, err := managers[partyID].GetStatus(ceremonyID)
		if err != nil {
			t.Fatalf("GetStatus failed: %v", err)
		}
		if status.Status == ceremonyCompleted {
			break
		}
		if status.Status == ceremonyFailed {
			t.Fatalf("keygen failed: %s", status.Error)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for keygen to complete")
		}
		time.Sleep(250 * time.Millisecond)
	}

	managers[partyID].mu.Lock()
	ceremony := managers[partyID].ceremonies[ceremonyID]
	managers[partyID].mu.Unlock()

	ceremony.mu.Lock()
	original := *ceremony.saveData
	wasSealed := ceremony.sealed
	ceremony.mu.Unlock()

	if !wasSealed {
		t.Fatal("ceremony completed but sealed=false even though VAULT_ADDR is set -- sealing silently didn't happen")
	}

	loaded, err := LoadKeyShare(context.Background(), os.Getenv, partyID, ceremonyID)
	if err != nil {
		t.Fatalf("LoadKeyShare failed: %v", err)
	}

	// Compare via JSON rather than reflect.DeepEqual: LocalPartySaveData
	// embeds *big.Int and elliptic-curve point types whose internal
	// representation isn't guaranteed identical after a marshal/unmarshal
	// round-trip even when the mathematical values are -- JSON
	// serialization is the actual contract SealKeyShare/LoadKeyShare rely
	// on, so it's also the right equality check here.
	originalJSON, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("failed to marshal original save data: %v", err)
	}
	loadedJSON, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("failed to marshal loaded save data: %v", err)
	}
	if string(originalJSON) != string(loadedJSON) {
		t.Fatalf("key share read back from Vault does not match what was sealed:\noriginal: %s\nloaded:   %s", originalJSON, loadedJSON)
	}

	t.Logf("SUCCESS: key share round-tripped through real Vault byte-for-byte identical (%d bytes)", len(originalJSON))
}

// TestSealKeyShareSkippedWithoutVaultAddr proves the documented fallback:
// with VAULT_ADDR unset, SealKeyShare is a no-op (sealed=false, no error)
// rather than failing outright -- the existing behavior every other test
// in this package (which don't set VAULT_ADDR) depends on.
func TestSealKeyShareSkippedWithoutVaultAddr(t *testing.T) {
	if os.Getenv("VAULT_ADDR") != "" {
		t.Skip("VAULT_ADDR is set in this environment; this test specifically checks the unset case")
	}
	sealed, err := SealKeyShare(context.Background(), os.Getenv, 1, "irrelevant", nil)
	if err != nil {
		t.Fatalf("expected no error when VAULT_ADDR is unset, got: %v", err)
	}
	if sealed {
		t.Fatal("expected sealed=false when VAULT_ADDR is unset")
	}
}
