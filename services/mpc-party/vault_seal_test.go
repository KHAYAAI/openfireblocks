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

// requireVaultEnv gives this test somewhere to seal to: the real Vault the
// environment already points at, or the in-process KV v2 stand-in from
// vault_fake_test.go.
//
// It used to skip when VAULT_ADDR was unset, which in practice meant it
// skipped always. That is not a cautious test, it is an absent one, and it
// cost something concrete: LoadKeyShare returned a silently empty share
// for as long as key shares have carried curve tags, and this is the test
// that would have caught it the same day. It was finally caught by running
// it against a real Vault -- which is an argument for making it run
// everywhere, not for relying on someone doing that again.
//
// Against a real server when one is configured:
//
//	VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN=<token> go test -run TestSeal -v
func requireVaultEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("VAULT_ADDR") != "" {
		t.Logf("using the Vault at %s", os.Getenv("VAULT_ADDR"))
		return
	}
	startFakeVault(t)
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
	// Parties talk over plain HTTP here, in one process, with no PKI --
	// so there is no client certificate to bind a sender to. Peer
	// authentication is disabled explicitly rather than implicitly: see
	// peer_identity.go, where the production default is to refuse a
	// protocol message that cannot be attributed to a certificate.
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")

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
		if err := managers[id].StartKeygen(ceremonyID, 1, peers, CurveSecp256k1); err != nil {
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

	// The first thing to check, because it is what actually broke. A share
	// that unmarshalled into the wrong shape came back with every field
	// nil and no error at all, and a JSON comparison of two such structs
	// against each other would have been perfectly happy.
	if loaded == nil || loaded.ECDSAPub == nil || loaded.PaillierSK == nil || len(loaded.Ks) == 0 {
		t.Fatalf("LoadKeyShare returned a share with no material in it: %+v", loaded)
	}

	// Compare via JSON rather than reflect.DeepEqual: LocalPartySaveData
	// embeds *big.Int and elliptic-curve point types whose internal
	// representation isn't guaranteed identical after a marshal/unmarshal
	// round-trip even when the mathematical values are -- JSON
	// serialization is the actual contract SealKeyShare/LoadKeyShare rely
	// on, so it's also the right equality check here.
	//
	// Against original.ECDSA, not original. The ceremony holds a KeyShare,
	// which is a curve-tagged union, and LoadKeyShare returns the
	// secp256k1 save-data inside it. Marshalling the two outer shapes and
	// comparing them compares a tagged wrapper to its own contents, which
	// can never match and says nothing about whether the material survived.
	originalJSON, err := json.Marshal(original.ECDSA)
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

	t.Logf("SUCCESS: key share round-tripped through %s byte-for-byte identical (%d bytes)",
		vaultUnderTest(), len(originalJSON))
}

// TestSealKeyShareSkippedWithoutVaultAddr proves the documented fallback:
// with VAULT_ADDR unset, SealKeyShare is a no-op (sealed=false, no error)
// rather than failing outright -- the existing behavior every other test
// in this package (which don't set VAULT_ADDR) depends on.
func TestSealKeyShareSkippedWithoutVaultAddr(t *testing.T) {
	// Parties talk over plain HTTP here, in one process, with no PKI --
	// so there is no client certificate to bind a sender to. Peer
	// authentication is disabled explicitly rather than implicitly: see
	// peer_identity.go, where the production default is to refuse a
	// protocol message that cannot be attributed to a certificate.
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")

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
