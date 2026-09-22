package chains

import (
	"context"
	"crypto/sha256"
	"testing"
)

// testPrivKey is a fixed secp256k1/Ed25519-seed key. Test-only, obviously --
// it is a well-known throwaway value, never a real key.
const testPrivKey = "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

// hash32 is what every chain here except Solana expects: a 32-byte digest.
// The previous version of these tests passed raw strings like "test message"
// (12 bytes), which is why they had never actually run -- secp256k1 signing
// rejects anything that is not exactly 32 bytes.
func hash32(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func TestSignerRouter_SupportedChains(t *testing.T) {
	router := NewSignerRouter()

	supported := router.SupportedChains()
	if len(supported) != 4 {
		t.Errorf("expected 4 chains, got %d", len(supported))
	}

	expectedChains := map[string]bool{
		"ethereum":   true,
		"bitcoin":    true,
		"solana":     true,
		"cosmos-hub": true,
	}

	for _, chain := range supported {
		if !expectedChains[chain] {
			t.Errorf("unexpected chain: %s", chain)
		}
	}
}

func TestSignerRouter_IsValidChainID(t *testing.T) {
	router := NewSignerRouter()

	tests := []struct {
		chainID string
		valid   bool
	}{
		{"ethereum", true},
		{"bitcoin", true},
		{"solana", true},
		{"cosmos-hub", true},
		{"unknown", false},
		{"", false},
	}

	for _, test := range tests {
		if router.IsValidChainID(test.chainID) != test.valid {
			t.Errorf("IsValidChainID(%q) = %v, want %v", test.chainID, !test.valid, test.valid)
		}
	}
}

func TestSignerRouter_SignMultiChain_Ethereum(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{ChainID: "ethereum", Message: hash32("test message")}

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Fatalf("SignMultiChain failed: %v", err)
	}
	if resp.ChainID != "ethereum" {
		t.Errorf("expected chainID ethereum, got %s", resp.ChainID)
	}
	if resp.Signature == nil || resp.Signature.SignatureBytes == "" {
		t.Fatal("expected signature to be set")
	}
	if resp.Status != "signed" {
		t.Errorf("expected status signed, got %s", resp.Status)
	}
	if len(resp.From) != 42 || resp.From[:2] != "0x" {
		t.Errorf("expected a 0x-prefixed Ethereum address, got %q", resp.From)
	}
}

func TestSignerRouter_SignMultiChain_Bitcoin(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{ChainID: "bitcoin", Message: hash32("test message hash")}

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Fatalf("SignMultiChain failed: %v", err)
	}
	if resp.ChainID != "bitcoin" {
		t.Errorf("expected chainID bitcoin, got %s", resp.ChainID)
	}
	if resp.Signature == nil {
		t.Fatal("expected signature to be set")
	}
	// A mainnet P2PKH address is base58 and starts with '1'.
	if resp.From == "" || resp.From[0] != '1' {
		t.Errorf("expected a mainnet P2PKH address starting with '1', got %q", resp.From)
	}
}

func TestSignerRouter_SignMultiChain_Solana(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	// Solana signs the message itself, not a digest of it, so a raw
	// (non-32-byte) message is correct here.
	req := &ChainSignRequest{ChainID: "solana", Message: []byte("test message for solana ed25519")}

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Fatalf("SignMultiChain failed: %v", err)
	}
	if resp.ChainID != "solana" {
		t.Errorf("expected chainID solana, got %s", resp.ChainID)
	}
	if resp.Signature == nil {
		t.Fatal("expected signature to be set")
	}
	// Ed25519 cannot recover a key from a signature, so the router's
	// best-effort recovery is expected to leave this unknown rather than
	// invent an address.
	if resp.From != "unknown" {
		t.Errorf("expected From to be \"unknown\" for Solana, got %q", resp.From)
	}
}

func TestSignerRouter_SignMultiChain_Cosmos(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{ChainID: "cosmos-hub", Message: hash32("cosmos sign doc")}

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Fatalf("SignMultiChain failed: %v", err)
	}
	if resp.ChainID != "cosmos-hub" {
		t.Errorf("expected chainID cosmos-hub, got %s", resp.ChainID)
	}
	if resp.Signature == nil {
		t.Fatal("expected signature to be set")
	}
	if len(resp.From) < 7 || resp.From[:7] != "cosmos1" {
		t.Errorf("expected a bech32 cosmos1... address, got %q", resp.From)
	}
}

func TestSignerRouter_SignMultiChain_UnsupportedChain(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{ChainID: "unknown-chain", Message: hash32("test")}

	_, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err == nil {
		t.Fatal("expected error for unsupported chain")
	}
	if err.Error() != "unsupported chain: unknown-chain" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSignerRouter_RecoverAddress(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	message := hash32("test message")
	req := &ChainSignRequest{ChainID: "ethereum", Message: message}

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Fatalf("SignMultiChain failed: %v", err)
	}

	recovered, err := router.RecoverAddress(ctx, "ethereum", message, resp.Signature)
	if err != nil {
		t.Fatalf("RecoverAddress failed: %v", err)
	}
	if recovered == "" {
		t.Fatal("expected recovered address")
	}
	if recovered != resp.From {
		t.Errorf("recovered address %s != signed address %s", recovered, resp.From)
	}
}

func TestSignerRouter_VerifySignature_RejectsEmptyPubKey(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	message := hash32("test message")
	req := &ChainSignRequest{ChainID: "ethereum", Message: message}

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Fatalf("SignMultiChain failed: %v", err)
	}

	ok, err := router.VerifySignature(ctx, "ethereum", message, resp.Signature, "")
	if err == nil && ok {
		t.Error("verification against an empty public key must not succeed")
	}
}

// The same key on different chains must not produce the same signature or
// the same address -- different hashing, encoding, and in Solana's case a
// different curve entirely.
func TestSignerRouter_ChainIsolation(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	digest := hash32("same message for all chains")
	signatures := map[string]string{}
	addresses := map[string]string{}

	for _, chainID := range []string{"ethereum", "bitcoin", "solana", "cosmos-hub"} {
		message := digest
		if chainID == "solana" {
			message = []byte("same message for all chains")
		}

		resp, err := router.SignMultiChain(ctx, &ChainSignRequest{ChainID: chainID, Message: message}, testPrivKey)
		if err != nil {
			t.Fatalf("SignMultiChain for %s failed: %v", chainID, err)
		}
		signatures[chainID] = resp.Signature.SignatureBytes
		addresses[chainID] = resp.From
	}

	if signatures["ethereum"] == signatures["bitcoin"] {
		t.Error("ethereum and bitcoin should produce different signatures")
	}
	if signatures["ethereum"] == signatures["solana"] {
		t.Error("ethereum and solana should produce different signatures")
	}
	if addresses["ethereum"] == addresses["cosmos-hub"] {
		t.Error("ethereum and cosmos addresses should be different")
	}
	if addresses["ethereum"][:2] != "0x" {
		t.Errorf("ethereum address should start with 0x, got %s", addresses["ethereum"])
	}
	if addresses["cosmos-hub"][:6] != "cosmos" {
		t.Errorf("cosmos address should start with cosmos, got %s", addresses["cosmos-hub"])
	}
}
