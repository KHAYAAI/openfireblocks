package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	immuclient "github.com/codenotary/immudb/pkg/client"
)

// audit.go writes immutable, cryptographically verifiable audit records to
// immudb. Every sign request, success and failure produces an entry keyed by
// "audit:{unix_nano}:{request_id}" so the full lifecycle of a request can be
// replayed and independently verified.
//
// The logger degrades gracefully: if immudb is unavailable the signer keeps
// serving requests and LogEvent returns an error the caller logs as a warning.
// The NestJS api-gateway also records the same events in PostgreSQL, so an
// immudb outage never loses the audit trail entirely.

// AuditEvent is a single immutable audit record.
type AuditEvent struct {
	Type      string `json:"type"`                // e.g. SIGN_REQUEST_RECEIVED, SIGN_SUCCESS, SIGN_FAILED
	RequestID string `json:"requestId"`           //
	Timestamp string `json:"timestamp"`           // RFC3339
	Message   string `json:"message,omitempty"`   //
	Signature string `json:"signature,omitempty"` //
	Hash      string `json:"hash,omitempty"`      //
	From      string `json:"from,omitempty"`      //
	Status    string `json:"status"`              // pending | signed | failed
}

// AuditLogger wraps an immudb session.
type AuditLogger struct {
	client immuclient.ImmuClient
}

// NewAuditLogger opens an immudb session. immudbURL is "host:port"
// (e.g. "immudb:3322"); user/pass default to immudb's built-in admin account.
func NewAuditLogger(immudbURL, user, pass string) (*AuditLogger, error) {
	host, port, err := splitHostPort(immudbURL)
	if err != nil {
		return nil, err
	}
	if user == "" {
		user = "immudb"
	}
	if pass == "" {
		pass = "immudb"
	}

	opts := immuclient.DefaultOptions().WithAddress(host).WithPort(port)
	c := immuclient.NewClient().WithOptions(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.OpenSession(ctx, []byte(user), []byte(pass), "defaultdb"); err != nil {
		return nil, fmt.Errorf("immudb OpenSession failed: %w", err)
	}

	return &AuditLogger{client: c}, nil
}

// LogEvent appends an event to the immutable ledger and returns the immudb
// transaction id of the write.
func (a *AuditLogger) LogEvent(ctx context.Context, event AuditEvent) (uint64, error) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return 0, err
	}

	key := fmt.Sprintf("audit:%d:%s", time.Now().UnixNano(), event.RequestID)
	hdr, err := a.client.Set(ctx, []byte(key), payload)
	if err != nil {
		return 0, err
	}
	return hdr.Id, nil
}

// Close closes the immudb session.
func (a *AuditLogger) Close() error {
	if a.client == nil {
		return nil
	}
	return a.client.CloseSession(context.Background())
}

// splitHostPort parses "host:port" into a host and an integer port. It tolerates
// a leading scheme (e.g. "http://immudb:3322") for convenience.
func splitHostPort(addr string) (string, int, error) {
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")

	parts := strings.Split(addr, ":")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid immudb address %q, want host:port", addr)
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid immudb port in %q: %w", addr, err)
	}
	return parts[0], port, nil
}
