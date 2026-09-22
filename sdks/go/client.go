package openfireblocks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Client is the OpenFireblocks API client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	timeout    time.Duration
}

// NewClient creates a new OpenFireblocks client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		timeout: 30 * time.Second,
	}
}

// KeyPair represents a threshold key pair.
type KeyPair struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Blockchain   string    `json:"blockchain"`
	Address      string    `json:"address"`
	PublicKey    string    `json:"public_key"`
	Threshold    int       `json:"threshold"`
	TotalParties int       `json:"total_parties"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// CreateKeyPairRequest is the request to create a new key pair.
type CreateKeyPairRequest struct {
	Name         string `json:"name"`
	Blockchain   string `json:"blockchain"`
	Threshold    int    `json:"threshold"`
	TotalParties int    `json:"total_parties"`
}

// CreateKeyPair creates a new threshold key pair via DKG ceremony.
func (c *Client) CreateKeyPair(ctx context.Context, req *CreateKeyPairRequest) (*KeyPair, error) {
	if req.Threshold > req.TotalParties {
		return nil, fmt.Errorf("threshold must be <= total parties")
	}
	if req.Threshold < 1 || req.TotalParties < 1 {
		return nil, fmt.Errorf("threshold and total parties must be >= 1")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/keys", body)
	if err != nil {
		return nil, fmt.Errorf("failed to create key pair: %w", err)
	}
	defer resp.Body.Close()

	var keyPair KeyPair
	if err := json.NewDecoder(resp.Body).Decode(&keyPair); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &keyPair, nil
}

// GetKeyPair retrieves a key pair by ID.
func (c *Client) GetKeyPair(ctx context.Context, keyID string) (*KeyPair, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/keys/%s", keyID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get key pair: %w", err)
	}
	defer resp.Body.Close()

	var keyPair KeyPair
	if err := json.NewDecoder(resp.Body).Decode(&keyPair); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &keyPair, nil
}

// ListKeyPairs lists all key pairs for the customer.
func (c *Client) ListKeyPairs(ctx context.Context) ([]*KeyPair, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/keys", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list key pairs: %w", err)
	}
	defer resp.Body.Close()

	var keyPairs []*KeyPair
	if err := json.NewDecoder(resp.Body).Decode(&keyPairs); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return keyPairs, nil
}

// SigningRequest is a request to sign a transaction.
type SigningRequest struct {
	KeyPairID      string `json:"key_pair_id"`
	Transaction    string `json:"transaction"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// SigningResponse is the result of a signing request.
type SigningResponse struct {
	ID               string        `json:"id"`
	Status           string        `json:"status"`
	SignedTransaction string       `json:"signed_transaction,omitempty"`
	Signature        string        `json:"signature,omitempty"`
	LatencyMs        int           `json:"latency_ms"`
	Error            string        `json:"error,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
}

// Sign submits a transaction for threshold signing.
// Automatically generates idempotency key if not provided.
func (c *Client) Sign(ctx context.Context, req *SigningRequest) (*SigningResponse, error) {
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = uuid.New().String()
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, "/sign", body)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}
	defer resp.Body.Close()

	var sigResp SigningResponse
	if err := json.NewDecoder(resp.Body).Decode(&sigResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &sigResp, nil
}

// GetSigningStatus retrieves the status of a signing request.
func (c *Client) GetSigningStatus(ctx context.Context, requestID string) (*SigningResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/sign/%s", requestID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get signing status: %w", err)
	}
	defer resp.Body.Close()

	var sigResp SigningResponse
	if err := json.NewDecoder(resp.Body).Decode(&sigResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &sigResp, nil
}

// HealthResponse is the health check response.
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Version   string `json:"version"`
}

// Health checks the API health.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &health, nil
}

// doRequest performs an HTTP request with proper error handling and auth.
func (c *Client) doRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := c.baseURL + "/v1" + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status=%d body=%s", resp.StatusCode, string(bodyBytes))
	}

	return resp, nil
}
