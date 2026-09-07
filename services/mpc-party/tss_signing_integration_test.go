package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// TestSigningWithEveryCommittee proves a 2-of-3 key can actually be signed
// with by *any* two of its three parties.
//
// That is the entire availability claim of threshold signing, and it was
// false. The gateway always chose the first `threshold` parties by id, so
// only the committee {1, 2} was ever exercised -- and only in that committee
// do a party's DKG index and its committee index coincide. The first time a
// different committee was tried, because party 1's node had been drained,
// tss-lib panicked with "PrepareForSigning: len(ks) <= i": the committee
// carried original DKG indices into a subset that no longer had that many
// entries.
//
// One DKG, then every committee in turn, because the failure depends
// entirely on *which* parties are chosen. Testing only {1, 2} is what let
// this survive.
func TestSigningWithEveryCommittee(t *testing.T) {
	if testing.Short() {
		t.Skip("real DKG + signing takes real time; skipped in -short")
	}

	const n = 3
	const threshold = 1 // 2-of-3

	managers := make(map[int]*TSSPartyManager, n)
	peers := make(map[int]string, n)
	for i := 1; i <= n; i++ {
		// Generous, because safe-prime generation is CPU-bound and this
		// test runs three parties at once: a peer that is mid-generation
		// legitimately takes a while to answer, and a tight client timeout
		// turns that into a spurious relay failure.
		mgr := NewTSSPartyManager(i, &http.Client{Timeout: 90 * time.Second})
		managers[i] = mgr
		router := mux.NewRouter()
		ps := &PartyServer{partyID: i, tssManager: mgr}
		router.HandleFunc("/tss/keygen/message", ps.HandleTSSKeygenMessage).Methods(http.MethodPost)
		router.HandleFunc("/tss/sign/message", ps.HandleTSSSignMessage).Methods(http.MethodPost)
		server := httptest.NewServer(router)
		peers[i] = server.URL
		t.Cleanup(server.Close)
	}

	ceremonyID := "every-committee-keygen"
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

	// {1,2} is the committee that always worked. {1,3} and {2,3} are the
	// ones that panicked: in each, at least one member's DKG index is
	// larger than the committee it now belongs to.
	for _, committee := range [][]int{{1, 2}, {1, 3}, {2, 3}} {
		committee := committee
		name := fmt.Sprintf("committee_%d_%d", committee[0], committee[1])
		t.Run(name, func(t *testing.T) {
			messageHash := sha256.Sum256([]byte("committee " + name))
			signID := "sign-" + name

			for _, partyID := range committee {
				if err := managers[partyID].StartSigning(signID, ceremonyID, messageHash[:], committee); err != nil {
					t.Fatalf("party %d: StartSigning failed: %v", partyID, err)
				}
			}

			results := make(map[int]*SigningStatusResult)
			deadline := time.Now().Add(3 * time.Minute)
			for len(results) < len(committee) && time.Now().Before(deadline) {
				for _, partyID := range committee {
					if _, done := results[partyID]; done {
						continue
					}
					status, err := managers[partyID].GetSigningStatus(signID)
					if err != nil {
						t.Fatalf("party %d: GetSigningStatus failed: %v", partyID, err)
					}
					switch status.Status {
					case ceremonyCompleted:
						results[partyID] = status
					case ceremonyFailed:
						t.Fatalf("party %d: signing failed with committee %v: %s", partyID, committee, status.Error)
					}
				}
				if len(results) < len(committee) {
					time.Sleep(250 * time.Millisecond)
				}
			}
			if len(results) != len(committee) {
				t.Fatalf("committee %v timed out; got %d/%d parties", committee, len(results), len(committee))
			}

			// The signature has to recover to the *same* address the DKG
			// derived. A committee that produced a valid-looking signature
			// for a different key would be far worse than one that failed.
			sig := results[committee[0]].Signature
			for _, partyID := range committee {
				if results[partyID].Signature != sig {
					t.Fatalf("committee %v did not converge on one signature", committee)
				}
			}
			raw, err := hex.DecodeString(sig)
			if err != nil {
				t.Fatalf("signature is not hex: %v", err)
			}
			pub, err := crypto.SigToPub(messageHash[:], raw)
			if err != nil {
				t.Fatalf("committee %v produced an unrecoverable signature: %v", committee, err)
			}
			got := crypto.PubkeyToAddress(*pub).Hex()
			if got != sharedAddress {
				t.Fatalf("committee %v signed for %s, but the key is %s", committee, got, sharedAddress)
			}
			t.Logf("committee %v signed for %s", committee, got)
		})
	}
}
