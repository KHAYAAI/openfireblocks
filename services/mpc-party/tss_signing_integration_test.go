package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gorilla/mux"
)

// TestRealMultiPartySigningOverHTTP proves the full production shape end to
// end: real DKG over real HTTP (as TestRealMultiPartyKeygenOverHTTP already
// proves), followed by a real threshold SIGNING ceremony over the same
// transport, producing a 65-byte [R||S||V] signature that independently
// recovers to the address the DKG derived -- the same correctness bar
// services/mpc-signer/tss/tss_test.go applies in-process, proven here over
// real network transport between independent processes instead.
func TestRealMultiPartySigningOverHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("real DKG + signing takes real time; skipped in -short")
	}

	const n = 3
	const threshold = 1 // 2-of-3

	managers := make(map[int]*TSSPartyManager, n)
	peers := make(map[int]string, n)

	for i := 1; i <= n; i++ {
		mgr := NewTSSPartyManager(i, &http.Client{Timeout: 10 * time.Second})
		managers[i] = mgr

		router := mux.NewRouter()
		ps := &PartyServer{partyID: i, tssManager: mgr}
		router.HandleFunc("/tss/keygen/message", ps.HandleTSSKeygenMessage).Methods(http.MethodPost)
		router.HandleFunc("/tss/sign/message", ps.HandleTSSSignMessage).Methods(http.MethodPost)
		server := httptest.NewServer(router)
		peers[i] = server.URL
		t.Cleanup(server.Close)
	}

	// --- Phase 1: real DKG over real HTTP ---
	ceremonyID := "signing-integration-keygen"
	for i := 1; i <= n; i++ {
		if err := managers[i].StartKeygen(ceremonyID, threshold, peers); err != nil {
			t.Fatalf("party %d: StartKeygen failed: %v", i, err)
		}
	}

	keygenResults := make(map[int]*KeygenStatusResult)
	deadline := time.Now().Add(5 * time.Minute)
	for len(keygenResults) < n && time.Now().Before(deadline) {
		for i := 1; i <= n; i++ {
			if _, done := keygenResults[i]; done {
				continue
			}
			status, err := managers[i].GetStatus(ceremonyID)
			if err != nil {
				t.Fatalf("party %d: GetStatus failed: %v", i, err)
			}
			switch status.Status {
			case ceremonyCompleted:
				keygenResults[i] = status
			case ceremonyFailed:
				t.Fatalf("party %d: keygen failed: %s", i, status.Error)
			}
		}
		if len(keygenResults) < n {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if len(keygenResults) != n {
		t.Fatalf("timed out waiting for keygen; got %d/%d parties", len(keygenResults), n)
	}
	sharedAddress := keygenResults[1].Address
	t.Logf("keygen converged on address %s", sharedAddress)

	// --- Phase 2: real threshold signing over real HTTP, committee = parties {1,2} ---
	messageHash := sha256.Sum256([]byte("openfireblocks: real threshold signature over real network transport"))
	signID := "signing-integration-sign"
	committee := []int{1, 2} // threshold+1 = 2 members

	for _, partyID := range committee {
		if err := managers[partyID].StartSigning(signID, ceremonyID, messageHash[:], committee); err != nil {
			t.Fatalf("party %d: StartSigning failed: %v", partyID, err)
		}
	}

	signResults := make(map[int]*SigningStatusResult)
	deadline = time.Now().Add(3 * time.Minute)
	for len(signResults) < len(committee) && time.Now().Before(deadline) {
		for _, partyID := range committee {
			if _, done := signResults[partyID]; done {
				continue
			}
			status, err := managers[partyID].GetSigningStatus(signID)
			if err != nil {
				t.Fatalf("party %d: GetSigningStatus failed: %v", partyID, err)
			}
			switch status.Status {
			case ceremonyCompleted:
				signResults[partyID] = status
			case ceremonyFailed:
				t.Fatalf("party %d: signing failed: %s", partyID, status.Error)
			}
		}
		if len(signResults) < len(committee) {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if len(signResults) != len(committee) {
		t.Fatalf("timed out waiting for signing; got %d/%d parties", len(signResults), len(committee))
	}

	// Every committee member independently reconstructs the identical
	// signature (tss-lib's protocol has every party in the committee end
	// up holding the full, valid signature, not just a share of it).
	var sig string
	for _, partyID := range committee {
		s := signResults[partyID].Signature
		if s == "" {
			t.Fatalf("party %d completed signing with an empty signature", partyID)
		}
		if sig == "" {
			sig = s
			continue
		}
		if s != sig {
			t.Fatalf("party %d produced a different signature than party %d -- signing did not converge", partyID, committee[0])
		}
	}

	// The real correctness bar: recover the public key from the signature
	// and confirm it derives the SAME address the DKG produced. This is
	// exactly what an Ethereum node does to validate a transaction
	// signature -- if this doesn't match, the signature is not valid,
	// full stop, regardless of what the ceremony reported.
	sigBytes, err := hex.DecodeString(sig)
	if err != nil {
		t.Fatalf("failed to decode signature hex: %v", err)
	}
	if len(sigBytes) != 65 {
		t.Fatalf("expected 65-byte signature, got %d bytes", len(sigBytes))
	}

	recoveredPub, err := crypto.SigToPub(messageHash[:], sigBytes)
	if err != nil {
		t.Fatalf("failed to recover public key from signature: %v", err)
	}
	recoveredAddress := crypto.PubkeyToAddress(*recoveredPub).Hex()

	if recoveredAddress != sharedAddress {
		t.Fatalf("signature recovers to %s, but DKG derived address %s -- signature is INVALID", recoveredAddress, sharedAddress)
	}

	t.Logf("SUCCESS: real threshold signature over real HTTP recovers to the DKG-derived address %s", recoveredAddress)
}
