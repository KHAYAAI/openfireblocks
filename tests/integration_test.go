package tests

import (
	"context"
	"testing"
	"time"

	"github.com/openfireblocks/openfireblocks-go-sdk/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyManagement(t *testing.T) {
	c := setupTestClient(t)

	t.Run("CreateKeyPair", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		ceremony, err := c.CreateKey(ctx, &client.CreateKeyRequest{
			Blockchain:  "bitcoin",
			Threshold:   4,
			TotalParties: 7,
			Name:        "Test Bitcoin Key",
		})

		require.NoError(t, err)
		assert.NotEmpty(t, ceremony.ID)
		assert.Equal(t, "preparing", ceremony.Status)
	})

	t.Run("ListKeys", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		resp, err := c.ListKeys(ctx, &client.ListKeysRequest{
			Limit:      10,
			Blockchain: "ethereum",
		})

		require.NoError(t, err)
		assert.NotNil(t, resp.Data)
	})

	t.Run("GetKey", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Create key first
		ceremony, err := c.CreateKey(ctx, &client.CreateKeyRequest{
			Blockchain:  "ethereum",
			Threshold:   3,
			TotalParties: 5,
		})
		require.NoError(t, err)

		// Get key
		key, err := c.GetKey(ctx, ceremony.KeyPairID)
		require.NoError(t, err)
		assert.Equal(t, ceremony.KeyPairID, key.ID)
	})
}

func TestSigning(t *testing.T) {
	c := setupTestClient(t)

	t.Run("SignTransaction", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Create key
		ceremony, err := c.CreateKey(ctx, &client.CreateKeyRequest{
			Blockchain:  "bitcoin",
			Threshold:   2,
			TotalParties: 3,
		})
		require.NoError(t, err)

		// Wait for key creation
		time.Sleep(5 * time.Second)

		// Sign transaction
		signingResp, err := c.Sign(ctx, &client.SignRequest{
			KeyPairID:   ceremony.KeyPairID,
			Transaction: "02000000...", // Bitcoin tx
		})

		require.NoError(t, err)
		assert.NotEmpty(t, signingResp.ID)
	})

	t.Run("WaitForSignature", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		// Create and sign
		ceremony, _ := c.CreateKey(ctx, &client.CreateKeyRequest{
			Blockchain:  "ethereum",
			Threshold:   2,
			TotalParties: 3,
		})
		time.Sleep(2 * time.Second)

		signingResp, _ := c.Sign(ctx, &client.SignRequest{
			KeyPairID:   ceremony.KeyPairID,
			Transaction: `{"to":"0x...","value":"1000"}`,
		})

		// Wait for completion
		completed, err := c.WaitForSignature(ctx, signingResp.ID, &client.WaitOptions{
			MaxWaitTime: 120 * time.Second,
			PollInterval: 1 * time.Second,
		})

		require.NoError(t, err)
		assert.Equal(t, "completed", completed.Status)
		assert.NotEmpty(t, completed.SignedTransaction)
	})

	t.Run("IdempotentSigning", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		ceremony, _ := c.CreateKey(ctx, &client.CreateKeyRequest{
			Blockchain: "bitcoin",
			Threshold: 2,
			TotalParties: 3,
		})

		idempotencyKey := "test_merchant_order_12345"
		txHex := "02000000..."

		// First signing
		signing1, err := c.Sign(ctx, &client.SignRequest{
			KeyPairID:      ceremony.KeyPairID,
			Transaction:    txHex,
			IdempotencyKey: idempotencyKey,
		})
		require.NoError(t, err)

		// Retry with same idempotency key
		signing2, err := c.Sign(ctx, &client.SignRequest{
			KeyPairID:      ceremony.KeyPairID,
			Transaction:    txHex,
			IdempotencyKey: idempotencyKey,
		})
		require.NoError(t, err)

		// Should return same signing ID
		assert.Equal(t, signing1.ID, signing2.ID)
	})
}

func TestCompliance(t *testing.T) {
	c := setupTestClient(t)

	t.Run("RegisterCustomer", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		customer, err := c.RegisterCustomer(ctx, &client.CustomerRequest{
			Name:    "Test Corp",
			Email:   "test@example.com",
			Country: "US",
		})

		require.NoError(t, err)
		assert.NotEmpty(t, customer.ID)
		assert.Equal(t, "pending", customer.KYCStatus)
	})

	t.Run("RiskLevelDetection", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		customer, _ := c.RegisterCustomer(ctx, &client.CustomerRequest{
			Name:    "Test Corp",
			Email:   "test@example.com",
			Country: "US", // Low-risk country
		})

		assert.Equal(t, "low", customer.AMLRiskLevel)
	})
}

func TestErrorHandling(t *testing.T) {
	c := setupTestClient(t)

	t.Run("InvalidKeyID", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := c.GetKey(ctx, "invalid_uuid")
		assert.Error(t, err)
		assert.Equal(t, 400, err.(*client.APIError).StatusCode)
	})

	t.Run("SigningTimeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		ceremony, _ := c.CreateKey(ctx, &client.CreateKeyRequest{
			Blockchain: "bitcoin",
			Threshold: 2,
			TotalParties: 3,
		})

		signingResp, _ := c.Sign(ctx, &client.SignRequest{
			KeyPairID:   ceremony.KeyPairID,
			Transaction: "02000000...",
		})

		// Wait with short timeout
		_, err := c.WaitForSignature(ctx, signingResp.ID, &client.WaitOptions{
			MaxWaitTime: 100 * time.Millisecond,
		})
		assert.Error(t, err)
	})
}

func TestPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test")
	}

	c := setupTestClient(t)

	t.Run("SigningLatency", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		ceremony, _ := c.CreateKey(ctx, &client.CreateKeyRequest{
			Blockchain: "bitcoin",
			Threshold: 2,
			TotalParties: 3,
		})
		time.Sleep(5 * time.Second)

		signingResp, _ := c.Sign(ctx, &client.SignRequest{
			KeyPairID:   ceremony.KeyPairID,
			Transaction: "02000000...",
		})

		completed, _ := c.WaitForSignature(ctx, signingResp.ID, &client.WaitOptions{
			MaxWaitTime: 120 * time.Second,
		})

		// P95 latency should be <100ms
		assert.Less(t, completed.LatencyMS, int64(100), 
			"Signing latency should be <100ms p95")
	})

	t.Run("Throughput", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		ceremony, _ := c.CreateKey(ctx, &client.CreateKeyRequest{
			Blockchain: "ethereum",
			Threshold: 2,
			TotalParties: 3,
		})

		start := time.Now()
		count := 0

		for time.Since(start) < 30*time.Second {
			_, err := c.Sign(ctx, &client.SignRequest{
				KeyPairID:   ceremony.KeyPairID,
				Transaction: "{}",
			})
			if err == nil {
				count++
			}
		}

		throughput := float64(count) / 30.0
		// Should support 100+ signatures/sec
		assert.Greater(t, throughput, float64(100),
			"Should support 100+ signatures/second")
	})
}

func setupTestClient(t *testing.T) *client.Client {
	c, err := client.New(
		"test-api-key",
		client.WithBaseURL("http://localhost:8080/v1"),
	)
	require.NoError(t, err)
	return c
}
