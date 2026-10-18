package openfireblocks

import (
	"context"
	"fmt"
	"testing"
)

// ExampleCreateKeyPair demonstrates creating a threshold key pair.
func ExampleClient_CreateKeyPair() {
	client := NewClient("https://api.openfireblocks.io", "your-api-key")
	ctx := context.Background()

	// Create a 4-of-7 threshold ECDSA key for Bitcoin
	keyPair, err := client.CreateKeyPair(ctx, &CreateKeyPairRequest{
		Name:         "bitcoin-cold-wallet",
		Blockchain:   "bitcoin",
		Threshold:    4,
		TotalParties: 7,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Created key pair: %s (status: %s)\n", keyPair.ID, keyPair.Status)
	fmt.Printf("Address: %s\n", keyPair.Address)
	// Output: Created key pair: ... (status: pending_dkg)
}

// ExampleClient_Sign demonstrates signing a transaction.
func ExampleClient_Sign() {
	client := NewClient("https://api.openfireblocks.io", "your-api-key")
	ctx := context.Background()

	// Sign a Bitcoin transaction
	sigResp, err := client.Sign(ctx, &SigningRequest{
		KeyPairID:   "key-pair-id",
		Transaction: "0x...", // Bitcoin transaction hex
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Signing request ID: %s\n", sigResp.ID)
	fmt.Printf("Status: %s\n", sigResp.Status)
	if sigResp.Status == "completed" {
		fmt.Printf("Signature: %s\n", sigResp.Signature)
	}
	// Output: Signing request ID: ...
}

// ExampleClient_WaitForSigning demonstrates polling for signing completion.
func ExampleClient_WaitForSigning() {
	client := NewClient("https://api.openfireblocks.io", "your-api-key")
	ctx := context.Background()

	// Start signing
	sigResp, _ := client.Sign(ctx, &SigningRequest{
		KeyPairID:   "key-pair-id",
		Transaction: "0x...",
	})

	// Poll for completion (in production, use WebSocket or polling)
	for sigResp.Status == "pending" || sigResp.Status == "in_progress" {
		sigResp, _ = client.GetSigningStatus(ctx, sigResp.ID)
		fmt.Printf("Status: %s\n", sigResp.Status)
	}

	if sigResp.Status == "completed" {
		fmt.Printf("Signed successfully: %s\n", sigResp.SignedTransaction)
	} else if sigResp.Status == "failed" {
		fmt.Printf("Signing failed: %s\n", sigResp.Error)
	}
	// Output: ...
}

// ExampleClient_ListKeys demonstrates listing all key pairs.
func ExampleClient_ListKeys() {
	client := NewClient("https://api.openfireblocks.io", "your-api-key")
	ctx := context.Background()

	keys, err := client.ListKeyPairs(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Found %d key pairs:\n", len(keys))
	for _, key := range keys {
		fmt.Printf("  - %s (%s): %s\n", key.ID, key.Blockchain, key.Status)
	}
	// Output: Found ... key pairs:
}

// ExampleClient_Health demonstrates health check.
func ExampleClient_Health() {
	client := NewClient("https://api.openfireblocks.io", "your-api-key")
	ctx := context.Background()

	health, err := client.Health(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("API Status: %s (version: %s)\n", health.Status, health.Version)
	// Output: API Status: healthy (version: 1.0.0)
}

// TestClientCreation tests client instantiation.
func TestClientCreation(t *testing.T) {
	client := NewClient("https://api.test.local", "test-key")
	if client.baseURL != "https://api.test.local" {
		t.Errorf("baseURL mismatch")
	}
	if client.apiKey != "test-key" {
		t.Errorf("apiKey mismatch")
	}
}

// TestKeyPairValidation tests key pair parameter validation.
func TestKeyPairValidation(t *testing.T) {
	client := NewClient("https://api.test.local", "test-key")
	ctx := context.Background()

	// Invalid threshold > total parties
	_, err := client.CreateKeyPair(ctx, &CreateKeyPairRequest{
		Threshold:    5,
		TotalParties: 3,
	})
	if err == nil {
		t.Errorf("expected error for invalid threshold")
	}
}
