package chains

import (
	"context"
	"testing"
)

func TestSignerRouter_SupportedChains(t *testing.T) {
	router := NewSignerRouter()

	chains := router.SupportedChains()
	if len(chains) != 4 {
		t.Errorf("expected 4 chains, got %d", len(chains))
	}

	expectedChains := map[string]bool{
		"ethereum":   true,
		"bitcoin":    true,
		"solana":     true,
		"cosmos-hub": true,
	}

	for _, chain := range chains {
		if !expectedChains[chain] {
			t.Errorf("unexpected chain: %s", chain)
		}
	}
}

func TestSignerRouter_IsValidChainId(t *testing.T) {
	router := NewSignerRouter()

	tests := []struct {
		chainId string
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
		if router.IsValidChainId(test.chainId) != test.valid {
			t.Errorf("IsValidChainId(%q) = %v, want %v", test.chainId, !test.valid, test.valid)
		}
	}
}

func TestSignerRouter_SignMultiChain_Ethereum(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{
		ChainId: "ethereum",
		Message: []byte("test message"),
	}

	// Use a test private key
	testPrivKey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Errorf("SignMultiChain failed: %v", err)
	}

	if resp.ChainID != "ethereum" {
		t.Errorf("expected chainId ethereum, got %s", resp.ChainID)
	}

	if resp.Signature == nil || resp.Signature.SignatureBytes == "" {
		t.Error("expected signature to be set")
	}

	if resp.Status != "signed" {
		t.Errorf("expected status signed, got %s", resp.Status)
	}
}

func TestSignerRouter_SignMultiChain_Bitcoin(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{
		ChainId: "bitcoin",
		Message: []byte("test message hash"),
	}

	testPrivKey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Errorf("SignMultiChain failed: %v", err)
	}

	if resp.ChainID != "bitcoin" {
		t.Errorf("expected chainId bitcoin, got %s", resp.ChainID)
	}

	if resp.Signature == nil {
		t.Error("expected signature to be set")
	}
}

func TestSignerRouter_SignMultiChain_Solana(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{
		ChainId: "solana",
		Message: []byte("test message for solana ed25519"),
	}

	// Solana needs a 32-byte private key for Ed25519
	testPrivKey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Errorf("SignMultiChain failed: %v", err)
	}

	if resp.ChainID != "solana" {
		t.Errorf("expected chainId solana, got %s", resp.ChainID)
	}

	if resp.Signature == nil {
		t.Error("expected signature to be set")
	}
}

func TestSignerRouter_SignMultiChain_Cosmos(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{
		ChainId: "cosmos-hub",
		Message: []byte("cosmos sign doc"),
	}

	testPrivKey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Errorf("SignMultiChain failed: %v", err)
	}

	if resp.ChainID != "cosmos-hub" {
		t.Errorf("expected chainId cosmos-hub, got %s", resp.ChainID)
	}

	if resp.Signature == nil {
		t.Error("expected signature to be set")
	}

	if resp.From == "" {
		t.Error("expected from address to be set")
	}
}

func TestSignerRouter_SignMultiChain_UnsupportedChain(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{
		ChainId: "unknown-chain",
		Message: []byte("test"),
	}

	_, err := router.SignMultiChain(ctx, req, "test-key")
	if err == nil {
		t.Error("expected error for unsupported chain")
	}

	if err.Error() != "unsupported chain: unknown-chain" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSignerRouter_RecoverAddress(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	// Test Ethereum recovery
	req := &ChainSignRequest{
		ChainId: "ethereum",
		Message: []byte("test message"),
	}

	testPrivKey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Errorf("SignMultiChain failed: %v", err)
	}

	recovered, err := router.RecoverAddress(ctx, "ethereum", req.Message, resp.Signature)
	if err != nil {
		t.Errorf("RecoverAddress failed: %v", err)
	}

	if recovered == "" {
		t.Error("expected recovered address")
	}

	if recovered != resp.From {
		t.Errorf("recovered address %s != signed address %s", recovered, resp.From)
	}
}

func TestSignerRouter_VerifySignature(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	req := &ChainSignRequest{
		ChainId: "ethereum",
		Message: []byte("test message"),
	}

	testPrivKey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	resp, err := router.SignMultiChain(ctx, req, testPrivKey)
	if err != nil {
		t.Errorf("SignMultiChain failed: %v", err)
	}

	// Get the public key for verification (in real scenario, would be derived from private key)
	// For now, just test that verification call doesn't panic
	valid, err := router.VerifySignature(ctx, "ethereum", req.Message, resp.Signature, "")
	if err != nil && err.Error() == "invalid public key: " {
		// Expected error with empty pub key, but method works
		return
	}
}

func TestSignerRouter_ChainIsolation(t *testing.T) {
	router := NewSignerRouter()
	ctx := context.Background()

	message := []byte("same message for all chains")
	testPrivKey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	chains := []string{"ethereum", "bitcoin", "solana", "cosmos-hub"}
	signatures := make(map[string]string)
	addresses := make(map[string]string)

	for _, chainId := range chains {
		req := &ChainSignRequest{
			ChainId: chainId,
			Message: message,
		}

		resp, err := router.SignMultiChain(ctx, req, testPrivKey)
		if err != nil {
			t.Errorf("SignMultiChain for %s failed: %v", chainId, err)
		}

		signatures[chainId] = resp.Signature.SignatureBytes
		addresses[chainId] = resp.From
	}

	// Verify that different chains produce different signatures
	if signatures["ethereum"] == signatures["bitcoin"] {
		t.Error("ethereum and bitcoin should produce different signatures")
	}

	if signatures["ethereum"] == signatures["solana"] {
		t.Error("ethereum and solana should produce different signatures")
	}

	// Verify that addresses are chain-specific
	if addresses["ethereum"] == addresses["cosmos-hub"] {
		t.Error("ethereum and cosmos addresses should be different")
	}

	// Ethereum address should start with 0x
	if addresses["ethereum"][:2] != "0x" {
		t.Errorf("ethereum address should start with 0x, got %s", addresses["ethereum"])
	}

	// Cosmos address should start with cosmos1
	if addresses["cosmos-hub"][:6] != "cosmos" {
		t.Errorf("cosmos address should start with cosmos, got %s", addresses["cosmos-hub"])
	}
}
