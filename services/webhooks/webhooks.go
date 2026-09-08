package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// WebhookService manages event delivery and webhook handling
type WebhookService struct {
	db         *PostgresDB
	httpClient *http.Client
}

// WebhookEvent represents an event to be delivered
type WebhookEvent struct {
	EventID    string      `json:"event_id"`
	EventType  string      `json:"event_type"`
	Timestamp  time.Time   `json:"timestamp"`
	Data       interface{} `json:"data"`
	CustomerID string      `json:"customer_id"`
}

// WebhookDelivery tracks webhook delivery attempts
type WebhookDelivery struct {
	DeliveryID   string     `json:"delivery_id"`
	WebhookID    string     `json:"webhook_id"`
	EventID      string     `json:"event_id"`
	EventType    string     `json:"event_type"`
	Attempt      int        `json:"attempt"`
	StatusCode   int        `json:"status_code"`
	ResponseTime int64      `json:"response_time_ms"`
	Success      bool       `json:"success"`
	ErrorMessage string     `json:"error_message,omitempty"`
	NextRetryAt  *time.Time `json:"next_retry_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`

	// The exact bytes sent and signed for this attempt. Persisted so a
	// retry can resend them verbatim -- the HMAC signature is over these
	// bytes, so re-marshalling the event could change key order and produce
	// a signature the receiver rejects. Not serialised outward: a delivery
	// log is operator-facing and the payload can carry customer data.
	Payload []byte `json:"-"`
}

// SigningCompletedEvent is sent when signing completes
type SigningCompletedEvent struct {
	SigningID        string `json:"signing_id"`
	KeyID            string `json:"key_id"`
	TransactionHash  string `json:"transaction_hash"`
	Status           string `json:"status"`
	ConfirmationTime int64  `json:"confirmation_time,omitempty"`
	CreatedAt        string `json:"created_at"`
}

// SettlementCompletedEvent is sent when settlement completes
type SettlementCompletedEvent struct {
	SettlementID     string `json:"settlement_id"`
	SigningID        string `json:"signing_id"`
	TransactionHash  string `json:"transaction_hash"`
	Blockchain       string `json:"blockchain"`
	Status           string `json:"status"`
	GasUsed          uint64 `json:"gas_used,omitempty"`
	ConfirmationTime int64  `json:"confirmation_time,omitempty"`
	CreatedAt        string `json:"created_at"`
}

// KeyCreatedEvent is sent when a key is created
type KeyCreatedEvent struct {
	KeyID        string `json:"key_id"`
	Name         string `json:"name"`
	Blockchain   string `json:"blockchain"`
	Threshold    int    `json:"threshold"`
	TotalParties int    `json:"total_parties"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

// ComplianceCheckEvent is sent when compliance check completes
type ComplianceCheckEvent struct {
	CheckID    string `json:"check_id"`
	CustomerID string `json:"customer_id"`
	CheckType  string `json:"check_type"`
	Status     string `json:"status"`
	RiskLevel  string `json:"risk_level"`
	CreatedAt  string `json:"created_at"`
}

// NewWebhookService creates a new webhook service
func NewWebhookService(db *PostgresDB) *WebhookService {
	return &WebhookService{
		db: db,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// PublishEvent publishes an event to all registered webhooks
func (s *WebhookService) PublishEvent(ctx context.Context, event *WebhookEvent) error {
	// Get all active webhooks for this customer
	webhooks, err := s.db.GetWebhooksByCustomer(ctx, event.CustomerID)
	if err != nil {
		log.Printf("Failed to get webhooks: %v", err)
		return nil
	}

	if len(webhooks) == 0 {
		return nil
	}

	// Deliver to each webhook
	for _, webhook := range webhooks {
		if !webhook.IsActive {
			continue
		}

		// Check if webhook is subscribed to this event type
		eventSubscribed := false
		for _, subscribedEvent := range webhook.Events {
			if subscribedEvent == event.EventType || subscribedEvent == "*" {
				eventSubscribed = true
				break
			}
		}

		if !eventSubscribed {
			continue
		}

		// Deliver in background
		go s.deliverWebhook(context.Background(), webhook, event)
	}

	return nil
}

// deliverWebhook attempts to deliver a webhook
func (s *WebhookService) deliverWebhook(ctx context.Context, webhook *Webhook, event *WebhookEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("failed to marshal event %s: %v", event.EventID, err)
		return
	}
	delivery := s.attemptDelivery(ctx, webhook, event.EventID, event.EventType, payload, 1)
	if err := s.db.CreateWebhookDelivery(context.Background(), delivery); err != nil {
		log.Printf("failed to record delivery %s: %v", delivery.DeliveryID, err)
	}
}

// attemptDelivery performs exactly one delivery attempt and returns the
// record describing it. It does not persist anything -- the caller decides,
// because the retry path needs to inspect the result first.
//
// Shared by first delivery and retry deliberately. They were separate before
// and the retry half was never written, so a retry could not have matched
// the original's headers, signature or backoff even if it had been. One code
// path means a retry is by construction the same request as the original.
func (s *WebhookService) attemptDelivery(
	ctx context.Context,
	webhook *Webhook,
	eventID, eventType string,
	payload []byte,
	attempt int,
) *WebhookDelivery {
	delivery := &WebhookDelivery{
		DeliveryID: uuid.New().String(),
		WebhookID:  webhook.WebhookID,
		EventID:    eventID,
		EventType:  eventType,
		Attempt:    attempt,
		CreatedAt:  time.Now(),
		Payload:    payload,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payload))
	if err != nil {
		delivery.ErrorMessage = fmt.Sprintf("failed to build request: %v", err)
		s.scheduleRetry(delivery, webhook)
		return delivery
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event-Type", eventType)
	req.Header.Set("X-Event-ID", eventID)
	req.Header.Set("X-Webhook-Signature", s.generateSignature(payload, webhook.Secret))
	// Lets a receiver tell a retry from a duplicate event, which is the
	// difference between "process this again" and "I already have this".
	req.Header.Set("X-Webhook-Attempt", strconv.Itoa(attempt))
	for k, v := range webhook.CustomHeaders {
		req.Header.Set(k, v)
	}

	startTime := time.Now()
	resp, err := s.httpClient.Do(req)
	delivery.ResponseTime = time.Since(startTime).Milliseconds()

	if err != nil {
		delivery.Success = false
		delivery.StatusCode = 0
		delivery.ErrorMessage = err.Error()
		s.scheduleRetry(delivery, webhook)
		return delivery
	}
	defer resp.Body.Close()
	// Drain so the connection can be reused rather than dropped.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	delivery.StatusCode = resp.StatusCode
	delivery.Success = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !delivery.Success {
		delivery.ErrorMessage = fmt.Sprintf("endpoint returned %d", resp.StatusCode)
		s.scheduleRetry(delivery, webhook)
	}
	return delivery
}

// scheduleRetry stamps next_retry_at when another attempt is allowed.
//
// The previous code set this only when the request failed at the transport
// level. A 500 from the endpoint logged "scheduling retry" and then
// scheduled nothing, so the most common failure a webhook receiver actually
// produces was the one that never got retried.
func (s *WebhookService) scheduleRetry(delivery *WebhookDelivery, webhook *Webhook) {
	if delivery.Attempt >= webhook.MaxRetries {
		return
	}
	backoff := webhook.BackoffSeconds
	if backoff <= 0 {
		backoff = 1
	}
	if webhook.ExponentialBackoff {
		// Capped: with maxRetries in the double digits an uncapped shift
		// overflows and lands the next attempt in the past or the far
		// future, depending on sign.
		shift := delivery.Attempt - 1
		if shift > 16 {
			shift = 16
		}
		backoff = backoff * (1 << uint(shift))
	}
	next := time.Now().Add(time.Duration(backoff) * time.Second)
	delivery.NextRetryAt = &next
}

// generateSignature creates HMAC-SHA256 signature for webhook verification
func (s *WebhookService) generateSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

// PublishSigningCompleted publishes signing completion event
func (s *WebhookService) PublishSigningCompleted(ctx context.Context, customerID string, signingData *SigningCompletedEvent) error {
	event := &WebhookEvent{
		EventID:    uuid.New().String(),
		EventType:  "signing_completed",
		Timestamp:  time.Now(),
		CustomerID: customerID,
		Data:       signingData,
	}

	return s.PublishEvent(ctx, event)
}

// PublishSettlementCompleted publishes settlement completion event
func (s *WebhookService) PublishSettlementCompleted(ctx context.Context, customerID string, settlementData *SettlementCompletedEvent) error {
	event := &WebhookEvent{
		EventID:    uuid.New().String(),
		EventType:  "settlement_completed",
		Timestamp:  time.Now(),
		CustomerID: customerID,
		Data:       settlementData,
	}

	return s.PublishEvent(ctx, event)
}

// PublishKeyCreated publishes key creation event
func (s *WebhookService) PublishKeyCreated(ctx context.Context, customerID string, keyData *KeyCreatedEvent) error {
	event := &WebhookEvent{
		EventID:    uuid.New().String(),
		EventType:  "key_created",
		Timestamp:  time.Now(),
		CustomerID: customerID,
		Data:       keyData,
	}

	return s.PublishEvent(ctx, event)
}

// PublishComplianceCheck publishes compliance check event
func (s *WebhookService) PublishComplianceCheck(ctx context.Context, customerID string, complianceData *ComplianceCheckEvent) error {
	event := &WebhookEvent{
		EventID:    uuid.New().String(),
		EventType:  "compliance_check",
		Timestamp:  time.Now(),
		CustomerID: customerID,
		Data:       complianceData,
	}

	return s.PublishEvent(ctx, event)
}

// GetWebhookDeliveries retrieves delivery logs for a webhook
func (s *WebhookService) GetWebhookDeliveries(ctx context.Context, webhookID string, limit int) ([]*WebhookDelivery, error) {
	return s.db.GetWebhookDeliveries(ctx, webhookID, limit)
}

// RetryWebhookDelivery resends the exact payload of a failed delivery.
//
// Records a *new* delivery row rather than mutating the old one: the
// delivery log is the evidence of what was attempted and when, and
// overwriting an attempt would destroy the history an operator is using the
// log to reconstruct.
func (s *WebhookService) RetryWebhookDelivery(ctx context.Context, deliveryID string) (*WebhookDelivery, error) {
	previous, err := s.db.GetWebhookDelivery(ctx, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("delivery not found")
	}
	if previous.Success {
		return nil, fmt.Errorf("delivery %s already succeeded; nothing to retry", deliveryID)
	}

	webhook, err := s.db.GetWebhook(ctx, previous.WebhookID)
	if err != nil {
		return nil, fmt.Errorf("webhook not found")
	}
	if previous.Attempt >= webhook.MaxRetries {
		return nil, fmt.Errorf("max retries exceeded (%d of %d)", previous.Attempt, webhook.MaxRetries)
	}
	// Deliveries written before migration 018 have no stored payload. Say so
	// precisely rather than sending an empty body that would fail its
	// signature check at the receiver and look like a receiver bug.
	if len(previous.Payload) == 0 {
		return nil, fmt.Errorf(
			"delivery %s predates payload capture (migration 018) and cannot be retried", deliveryID)
	}

	delivery := s.attemptDelivery(ctx, webhook, previous.EventID, previous.EventType,
		previous.Payload, previous.Attempt+1)
	if err := s.db.CreateWebhookDelivery(context.Background(), delivery); err != nil {
		return nil, fmt.Errorf("retry was sent but could not be recorded: %w", err)
	}
	if !delivery.Success {
		return delivery, fmt.Errorf("retry attempt %d failed: %s", delivery.Attempt, delivery.ErrorMessage)
	}
	return delivery, nil
}

// DeliveriesDueForRetry returns failed deliveries whose backoff has elapsed.
//
// The scheduling half of retry is real -- next_retry_at is stamped on every
// retryable failure -- but nothing sweeps it yet, so a retry is currently
// operator-triggered through this service. This is the query a sweeper would
// use; wiring it to a Temporal schedule is the remaining step.
func (s *WebhookService) DeliveriesDueForRetry(ctx context.Context, limit int) ([]*WebhookDelivery, error) {
	return s.db.GetDeliveriesDueForRetry(ctx, limit)
}

// HTTP Handlers

// HandlePublishEvent is for internal use to publish events
func (s *WebhookService) HandlePublishEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var event WebhookEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.PublishEvent(ctx, &event); err != nil {
		http.Error(w, fmt.Sprintf("Failed to publish event: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"event_id": event.EventID})
}

// HandleGetDeliveries is the HTTP handler to get webhook deliveries
func (s *WebhookService) HandleGetDeliveries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	webhookID := r.URL.Query().Get("webhook_id")
	if err := requireUUID("webhook_id", webhookID); err != nil {
		writeError(w, "list deliveries", err)
		return
	}

	deliveries, err := s.GetWebhookDeliveries(ctx, webhookID, 100)
	if err != nil {
		writeError(w, "list deliveries", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deliveries)
}

// HandleRetryDelivery re-sends a failed delivery on operator request.
func (s *WebhookService) HandleRetryDelivery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	deliveryID := r.URL.Query().Get("delivery_id")
	if deliveryID == "" {
		http.Error(w, "delivery_id required", http.StatusBadRequest)
		return
	}

	delivery, err := s.RetryWebhookDelivery(r.Context(), deliveryID)
	if err != nil {
		// A retry that was sent and rejected is not the same as one that
		// could not be attempted: the first is the endpoint's answer and is
		// reported with the attempt record, the second is a request error.
		if delivery != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":    err.Error(),
				"delivery": delivery,
			})
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(delivery)
}

// Webhook represents a webhook configuration
type Webhook struct {
	WebhookID          string            `json:"webhook_id"`
	CustomerID         string            `json:"customer_id"`
	URL                string            `json:"url"`
	Secret             string            `json:"secret,omitempty"`
	Events             []string          `json:"events"`
	IsActive           bool              `json:"is_active"`
	MaxRetries         int               `json:"max_retries"`
	BackoffSeconds     int               `json:"backoff_seconds"`
	ExponentialBackoff bool              `json:"exponential_backoff"`
	CustomHeaders      map[string]string `json:"custom_headers,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}
