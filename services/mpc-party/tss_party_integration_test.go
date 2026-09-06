package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// TestRealMultiPartyKeygenOverHTTP proves the actual production shape: N
// independent processes (here, N independent httptest servers -- real OS
// sockets, real HTTP round trips, real JSON+base64 message serialization,
// not in-process Go channels) run a genuine bnb-chain/tss-lib DKG and
// arrive at the identical shared public key/address without any party
// ever seeing another party's private key material. This is the
// real-transport counterpart to services/mpc-signer/tss/tss_test.go's
// TestThresholdKeygenAndSign, which proves the same protocol in-process.
func TestRealMultiPartyKeygenOverHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("real DKG prime generation takes real time; skipped in -short")
	}

	const n = 3
	const threshold = 1 // 2-of-3

	managers := make(map[int]*TSSPartyManager, n)
	servers := make(map[int]*httptest.Server, n)
	peers := make(map[int]string, n)

	for i := 1; i <= n; i++ {
		mgr := NewTSSPartyManager(i, &http.Client{Timeout: 10 * time.Second})
		managers[i] = mgr

		router := mux.NewRouter()
		ps := &PartyServer{partyID: i, tssManager: mgr}
		router.HandleFunc("/tss/keygen/message", ps.HandleTSSKeygenMessage).Methods(http.MethodPost)
		server := httptest.NewServer(router)
		servers[i] = server
		peers[i] = server.URL
		t.Cleanup(server.Close)
	}

	ceremonyID := "integration-test-ceremony"
	for i := 1; i <= n; i++ {
		if err := managers[i].StartKeygen(ceremonyID, threshold, peers); err != nil {
			t.Fatalf("party %d: StartKeygen failed: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Minute)
	results := make(map[int]*KeygenStatusResult)
	for len(results) < n && time.Now().Before(deadline) {
		for i := 1; i <= n; i++ {
			if _, done := results[i]; done {
				continue
			}
			status, err := managers[i].GetStatus(ceremonyID)
			if err != nil {
				t.Fatalf("party %d: GetStatus failed: %v", i, err)
			}
			switch status.Status {
			case ceremonyCompleted:
				results[i] = status
			case ceremonyFailed:
				t.Fatalf("party %d: ceremony failed: %s", i, status.Error)
			}
		}
		if len(results) < n {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if len(results) != n {
		t.Fatalf("timed out waiting for all %d parties to complete keygen; got %d", n, len(results))
	}

	var sharedAddress, sharedPubKey string
	for i := 1; i <= n; i++ {
		r := results[i]
		if r.Address == "" || r.PublicKey == "" {
			t.Fatalf("party %d completed with empty address/public key", i)
		}
		if sharedAddress == "" {
			sharedAddress, sharedPubKey = r.Address, r.PublicKey
			continue
		}
		if r.Address != sharedAddress {
			t.Fatalf("party %d derived a different address (%s) than party 1 (%s) -- DKG did not converge on a shared key", i, r.Address, sharedAddress)
		}
		if r.PublicKey != sharedPubKey {
			t.Fatalf("party %d derived a different public key than party 1 -- DKG did not converge", i)
		}
	}

	t.Logf("SUCCESS: %d independent HTTP servers ran a real DKG and converged on shared address %s", n, sharedAddress)
}
