package main

import (
	"crypto/ed25519"
	"encoding/hex"
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
	runKeygenOverHTTP(t, CurveSecp256k1)
}

// The same proof for Ed25519, which is what Solana signs with.
//
// Until this existed Solana had no threshold path at all: the signer in
// mpc-signer/chains/solana.go holds a whole private key, so calling Solana
// MPC custody would have been false. This is the test that makes it true.
//
// Not skipped under -short, unlike its secp256k1 sibling: an EdDSA DKG
// needs no Paillier pre-parameters, so there is no safe-prime generation
// and the ceremony finishes in a second or two rather than minutes.
func TestRealMultiPartyEd25519KeygenOverHTTP(t *testing.T) {
	runKeygenOverHTTP(t, CurveEd25519)
}

func runKeygenOverHTTP(t *testing.T, curve Curve) *sharedKey {
	t.Helper()

	// An Ed25519 ceremony needs no Paillier pre-parameters, so the parties
	// must not spend CPU generating them. Without this each Ed25519 party
	// starts a safe-prime search it will never use, and six of those
	// running beside the secp256k1 tests in this package starved them of
	// the CPU they genuinely need -- the ECDSA ceremonies then timed out,
	// and the failure looked like a bug in code this change never touched.
	if curve == CurveEd25519 {
		t.Setenv("TSS_PREPARAMS_POOL", "0")
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
		router.HandleFunc("/tss/sign/message", ps.HandleTSSSignMessage).Methods(http.MethodPost)
		server := httptest.NewServer(router)
		servers[i] = server
		peers[i] = server.URL
		t.Cleanup(server.Close)
	}

	ceremonyID := "integration-test-ceremony-" + string(curve)
	for i := 1; i <= n; i++ {
		if err := managers[i].StartKeygen(ceremonyID, threshold, peers, curve); err != nil {
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

	t.Logf("SUCCESS: %d independent HTTP servers ran a real %s DKG and converged on shared address %s",
		n, curve, sharedAddress)

	return &sharedKey{
		ceremonyID: ceremonyID,
		managers:   managers,
		peers:      peers,
		address:    sharedAddress,
		publicKey:  sharedPubKey,
	}
}

// sharedKey is what a completed DKG leaves behind, for a signing test to
// use without repeating the ceremony.
type sharedKey struct {
	ceremonyID string
	managers   map[int]*TSSPartyManager
	peers      map[int]string
	address    string
	publicKey  string
}

// The claim, end to end: three parties that never see each other's share
// produce one Ed25519 signature, and crypto/ed25519 -- the same code
// Solana validators run -- verifies it against the group public key.
//
// This is the test that separates threshold signing from a single-key
// signer wearing the same API. If any party could produce this alone, or
// if the parties did not converge on one key, ed25519.Verify says no.
func TestEd25519ThresholdSignatureVerifies(t *testing.T) {
	key := runKeygenOverHTTP(t, CurveEd25519)

	// Ed25519 signs the message itself rather than a digest of it, which
	// is why StartSigning's 32-byte rule is per curve. This is a
	// deliberately non-32-byte message: under the old rule it would have
	// been refused outright.
	message := []byte("a Solana transaction, or anything else worth signing")

	committee := []int{1, 2} // 2 of 3
	signID := "ed25519-signing-test"
	for _, id := range committee {
		if err := key.managers[id].StartSigning(signID, key.ceremonyID, message, committee); err != nil {
			t.Fatalf("party %d: StartSigning failed: %v", id, err)
		}
	}

	var signature string
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		status, err := key.managers[committee[0]].GetSigningStatus(signID)
		if err != nil {
			t.Fatalf("GetSigningStatus failed: %v", err)
		}
		if status.Status == ceremonyFailed {
			t.Fatalf("signing ceremony failed: %s", status.Error)
		}
		if status.Status == ceremonyCompleted && status.Signature != "" {
			signature = status.Signature
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if signature == "" {
		t.Fatal("timed out waiting for the signing ceremony to produce a signature")
	}

	sig, err := hex.DecodeString(signature)
	if err != nil {
		t.Fatalf("the signature is not hex: %v", err)
	}
	// Exactly 64. A 65th byte -- the recovery id Ethereum uses -- would
	// make this invalid, and Ed25519 has no public-key recovery for it to
	// mean anything.
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("the signature is %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}

	pubKey, err := hex.DecodeString(key.publicKey)
	if err != nil {
		t.Fatalf("the public key is not hex: %v", err)
	}
	if len(pubKey) != ed25519.PublicKeySize {
		t.Fatalf("the public key is %d bytes, want %d", len(pubKey), ed25519.PublicKeySize)
	}

	if !ed25519.Verify(pubKey, message, sig) {
		t.Fatal("crypto/ed25519 rejected the threshold signature against the group public key; " +
			"the parties did not produce a valid signature for the key they agreed on")
	}

	// And it must not verify a different message, or the check above would
	// pass for a signature over anything.
	if ed25519.Verify(pubKey, []byte("a different transaction"), sig) {
		t.Fatal("the signature verified against a message it was not made over")
	}

	t.Logf("SUCCESS: a 2-of-3 Ed25519 threshold signature verified against Solana address %s", key.address)
}
