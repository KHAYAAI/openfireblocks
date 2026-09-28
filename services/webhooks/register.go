package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Registering an endpoint to be called.
//
// The delivery machinery here has been complete for a while: it signs
// payloads, records every attempt, retries with exponential backoff, and
// exposes a manual retry. All of it operated on webhooks that no code path
// could create -- there was no route to register one and no CreateWebhook
// anywhere in the service. A customer could not subscribe to anything, so
// the events the platform emits had nowhere to go and nothing to prove the
// delivery machinery worked.

// RegisterWebhookRequest is what a customer supplies to subscribe.
type RegisterWebhookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
	// Secret is optional. One is generated when absent, because a webhook
	// with no secret cannot be authenticated by the receiver -- and a
	// receiver that cannot tell our request from anybody else's POST is a
	// hole, not a feature.
	Secret        string            `json:"secret,omitempty"`
	CustomHeaders map[string]string `json:"custom_headers,omitempty"`
}

// KnownEventTypes are the events the platform emits.
//
// Subscribing to an event that will never fire looks identical to a broken
// integration: the customer waits, nothing arrives, and there is nothing
// to look at. Rejecting an unknown name turns that into an error at
// registration, when somebody is reading the response.
var KnownEventTypes = []string{
	"key.created",
	"key.activated",
	"key.failed",
	"signature.created",
	"transaction.broadcast",
	"settlement.completed",
	"compliance.alert",
}

func isKnownEvent(name string) bool {
	for _, known := range KnownEventTypes {
		if known == name {
			return true
		}
	}
	return false
}

// validateWebhook checks a registration before it reaches the database.
func validateWebhook(req *RegisterWebhookRequest) error {
	parsed, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: url must be an absolute URL", ErrInvalidInput)
	}
	// HTTPS only. The payload carries what a customer signed and for how
	// much; delivering that over plaintext hands it to anyone on the path,
	// and the signature proves origin, not confidentiality.
	if parsed.Scheme != "https" {
		return fmt.Errorf("%w: url must be https, got %q", ErrInvalidInput, parsed.Scheme)
	}
	if len(req.Events) == 0 {
		return fmt.Errorf("%w: subscribe to at least one event", ErrInvalidInput)
	}
	for _, event := range req.Events {
		if !isKnownEvent(event) {
			return fmt.Errorf("%w: unknown event %q; known events are %s",
				ErrInvalidInput, event, strings.Join(KnownEventTypes, ", "))
		}
	}
	if req.Secret != "" && len(req.Secret) < 16 {
		return fmt.Errorf("%w: secret must be at least 16 characters", ErrInvalidInput)
	}
	return nil
}

// generateSecret returns a signing secret for a new webhook.
func generateSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating a webhook secret: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// CreateWebhook stores a customer's subscription.
func (p *PostgresDB) CreateWebhook(ctx context.Context, hook *Webhook) error {
	headers, err := json.Marshal(hook.CustomHeaders)
	if err != nil {
		return fmt.Errorf("encoding custom headers: %w", err)
	}
	return p.withTenant(ctx, hook.CustomerID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO webhooks (webhook_id, customer_id, url, secret, events, is_active,
			                      max_retries, backoff_seconds, exponential_backoff,
			                      custom_headers, created_at, updated_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			hook.WebhookID, hook.CustomerID, hook.URL, hook.Secret, pq.Array(hook.Events),
			hook.IsActive, hook.MaxRetries, hook.BackoffSeconds, hook.ExponentialBackoff,
			headers, hook.CreatedAt, hook.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert webhook: %w", err)
		}
		return nil
	})
}

// DeactivateWebhook stops deliveries without deleting the record.
//
// Deactivating rather than deleting: the deliveries table references the
// webhook, and a customer investigating a missed event needs its history
// to still exist. An endpoint that is gone and one that never existed are
// different situations.
func (p *PostgresDB) DeactivateWebhook(ctx context.Context, webhookID, customerID string) error {
	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE webhooks SET is_active = FALSE, updated_at = NOW()
			 WHERE webhook_id = $1::uuid AND customer_id = $2::uuid`,
			webhookID, customerID)
		if err != nil {
			return fmt.Errorf("failed to deactivate webhook: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return fmt.Errorf("webhook %s: %w", webhookID, ErrNotFound)
		}
		return nil
	})
}

// HandleRegisterWebhook subscribes an endpoint to events.
func (s *WebhookService) HandleRegisterWebhook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodGet:
		s.handleListWebhooks(w, r)
		return
	case http.MethodDelete:
		s.handleDeleteWebhook(w, r)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	customerID := r.Header.Get("X-Customer-ID")
	if err := requireUUID("X-Customer-ID", customerID); err != nil {
		writeError(w, "register webhook", err)
		return
	}

	var req RegisterWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "register webhook", fmt.Errorf("%w: %v", ErrInvalidInput, err))
		return
	}
	if err := validateWebhook(&req); err != nil {
		writeError(w, "register webhook", err)
		return
	}

	secret := req.Secret
	if secret == "" {
		generated, err := generateSecret()
		if err != nil {
			writeError(w, "register webhook", err)
			return
		}
		secret = generated
	}

	now := time.Now()
	hook := &Webhook{
		WebhookID:          uuid.New().String(),
		CustomerID:         customerID,
		URL:                strings.TrimSpace(req.URL),
		Secret:             secret,
		Events:             req.Events,
		IsActive:           true,
		MaxRetries:         3,
		BackoffSeconds:     60,
		ExponentialBackoff: true,
		CustomHeaders:      req.CustomHeaders,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := s.db.CreateWebhook(r.Context(), hook); err != nil {
		writeError(w, "register webhook", err)
		return
	}

	// The secret is returned exactly once, here. It is what the receiver
	// verifies our signature with, so they need it -- and it should not
	// then be readable from a list endpoint, where an exposed API key
	// would hand an attacker the ability to forge our webhooks.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(hook)
}

func (s *WebhookService) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	customerID := r.Header.Get("X-Customer-ID")
	if err := requireUUID("X-Customer-ID", customerID); err != nil {
		writeError(w, "list webhooks", err)
		return
	}

	hooks, err := s.db.GetWebhooksByCustomer(r.Context(), customerID)
	if err != nil {
		writeError(w, "list webhooks", err)
		return
	}
	// Never in a listing. Anyone who can read a customer's webhooks could
	// otherwise forge signed events to them.
	for _, hook := range hooks {
		hook.Secret = ""
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"webhooks": hooks})
}

func (s *WebhookService) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	customerID := r.Header.Get("X-Customer-ID")
	if err := requireUUID("X-Customer-ID", customerID); err != nil {
		writeError(w, "delete webhook", err)
		return
	}
	webhookID := r.URL.Query().Get("webhook_id")
	if err := requireUUID("webhook_id", webhookID); err != nil {
		writeError(w, "delete webhook", err)
		return
	}

	if err := s.db.DeactivateWebhook(r.Context(), webhookID, customerID); err != nil {
		writeError(w, "delete webhook", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
