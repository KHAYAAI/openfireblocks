//go:build tss

package tss

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

// TestThresholdKeygenAndSign runs a real 2-of-3 distributed keygen and signing,
// then verifies the resulting signature recovers the shared address — proving
// the threshold key behaves exactly like a normal Ethereum signer while the
// private key is never reconstructed.
//
// Keygen pre-params are CPU-heavy, so this is skipped under `go test -short`.
func TestThresholdKeygenAndSign(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow TSS keygen test in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	keys, err := Keygen(ctx, 3, 1)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	addr := keys.Address()
	t.Logf("threshold address: %s", addr)

	// 32-byte message hash to sign.
	hash := crypto.Keccak256([]byte("openfireblocks threshold signing test"))

	sig, err := keys.Sign(ctx, hash)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("signature length = %d, want 65", len(sig))
	}

	// Verify: recover the public key from the signature and compare addresses.
	recovered, err := crypto.SigToPub(hash, sig)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	recoveredAddr := crypto.PubkeyToAddress(*recovered).Hex()
	if recoveredAddr != addr {
		t.Fatalf("recovered address %s != threshold address %s", recoveredAddr, addr)
	}

	// Also verify the signature directly against the public key bytes.
	pubBytes := crypto.FromECDSAPub(keys.PublicKey)
	if !crypto.VerifySignature(pubBytes, hash, sig[:64]) {
		t.Fatal("VerifySignature failed for threshold signature")
	}

	// Sanity: a different message must not verify.
	other := crypto.Keccak256([]byte("different message"))
	if crypto.VerifySignature(pubBytes, other, sig[:64]) {
		t.Fatal("signature unexpectedly verified for a different message")
	}
	if bytes.Equal(hash, other) {
		t.Fatal("test setup error: hashes equal")
	}
}
