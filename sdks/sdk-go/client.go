// Package openfireblocks is a Go client for the OpenFireblocks API.
package openfireblocks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SignRequest is an unsigned transaction to sign (and optionally broadcast).
type SignRequest struct {
	ChainID  int    `json:"chainId"`
	To       string `json:"to"`
	Data     string `json:"data,omitempty"`
	Value    string `json:"value,omitempty"`
	GasLimit int    `json:"gasLimit"`
	GasPrice string `json:"gasPrice"`
	Nonce    int    `json:"nonce"`
	Country  string `json:"country,omitempty"`
}

// SignResult is returned by Sign.
type SignResult struct {
	RequestID   string `json:"requestId"`
	SignedTx    string `json:"signedTx"`
	TxHash      string `json:"txHash"`
	From        string `json:"from"`
	Status      string `json:"status"`
	Broadcasted bool   `json:"broadcasted"`
}

// APIError is returned for any non-2xx response.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("openfireblocks: HTTP %d: %s", e.StatusCode, e.Body)
}

// Client is a tenant-scoped API client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// New creates a Client. baseURL e.g. "https://api.openfireblocks.example".
func New(baseURL, apiKey string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) do(ctx context.Context, method, path string, body, out interface{}) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Sign signs (and optionally broadcasts) a transaction.
func (c *Client) Sign(ctx context.Context, req SignRequest) (*SignResult, error) {
	var out SignResult
	if err := c.do(ctx, http.MethodPost, "/sign", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTransactions returns the tenant's transactions.
func (c *Client) ListTransactions(ctx context.Context) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	if err := c.do(ctx, http.MethodGet, "/transactions", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTransaction returns one of the tenant's transactions by request id.
func (c *Client) GetTransaction(ctx context.Context, requestID string) (map[string]interface{}, error) {
	var out map[string]interface{}
	path := "/transactions/" + url.PathEscape(requestID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAuditTrail returns the audit events for a transaction.
func (c *Client) GetAuditTrail(ctx context.Context, requestID string) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	path := "/transactions/" + url.PathEscape(requestID) + "/audit"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
