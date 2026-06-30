package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	DeliveryID    string    `json:"delivery_id"`
	WebhookID     string    `json:"webhook_id"`
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	Attempt       int       `json:"attempt"`
	StatusCode    int       `json:"status_code"`
	ResponseTime  int64     `json:"response_time_ms"`
	Success       bool      `json:"success"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	NextRetryAt   *time.Time `json:"next_retry_at,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// SigningCompletedEvent is sent when signing completes
type SigningCompletedEvent struct {
	SigningID       string `json:"signing_id"`
	KeyID           string `json:"key_id"`
	TransactionHash string `json:"transaction_hash"`
	Status          string `json:"status"`
	ConfirmationTime int64  `json:"confirmation_time,omitempty"`
	CreatedAt       string `json:"created_at"`
}

// SettlementCompletedEvent is sent when settlement completes
type SettlementCompletedEvent struct {
	SettlementID    string `json:"settlement_id"`
	SigningID       string `json:"signing_id"`
	TransactionHash string `json:"transaction_hash"`
	Blockchain      string `json:"blockchain"`
	Status          string `json:"status"`
	GasUsed         uint64 `json:"gas_used,omitempty"`
	ConfirmationTime int64  `json:"confirmation_time,omitempty"`
	CreatedAt       string `json:"created_at"`
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
	CheckID   string `json:"check_id"`
	CustomerID string `json:"customer_id"`
	CheckType string `json:"check_type"`
	Status    string `json:"status"`
	RiskLevel string `json:"risk_level"`
	CreatedAt string `json:"created_at"`
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
	delivery := &WebhookDelivery{
		DeliveryID:   uuid.New().String(),
		WebhookID:    webhook.WebhookID,
		EventID:      event.EventID,
		EventType:    event.EventType,
		Attempt:      1,
		CreatedAt:    time.Now(),
	}

	// Marshal event
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to marshal event: %v", err)
		return
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payload))
	if err != nil {
		log.Printf("Failed to create request: %v", err)
		return
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event-Type", event.EventType)
	req.Header.Set("X-Event-ID", event.EventID)
	req.Header.Set("X-Webhook-Signature", s.generateSignature(payload, webhook.Secret))

	// Add custom headers
	for k, v := range webhook.CustomHeaders {
		req.Header.Set(k, v)
	}

	// Send request
	startTime := time.Now()
	resp, err := s.httpClient.Do(req)
	delivery.ResponseTime = time.Since(startTime).Milliseconds()

	if err != nil {
		delivery.Success = false
		delivery.ErrorMessage = err.Error()
		delivery.StatusCode = 0

		// Schedule retry
		if delivery.Attempt < webhook.MaxRetries {
			backoffSeconds := webhook.BackoffSeconds
			if webhook.ExponentialBackoff {
				backoffSeconds = backoffSeconds * (1 << uint(delivery.Attempt-1))
			}
			nextRetry := time.Now().Add(time.Duration(backoffSeconds) * time.Second)
			delivery.NextRetryAt = &nextRetry
		}

		// Store delivery log
		s.db.CreateWebhookDelivery(context.Background(), delivery)
		return
	}

	defer resp.Body.Close()

	delivery.StatusCode = resp.StatusCode
	delivery.Success = resp.StatusCode >= 200 && resp.StatusCode < 300

	// Store delivery log
	s.db.CreateWebhookDelivery(context.Background(), delivery)

	if !delivery.Success && delivery.Attempt < webhook.MaxRetries {
		// Schedule retry
		backoffSeconds := webhook.BackoffSeconds
		if webhook.ExponentialBackoff {
			backoffSeconds = backoffSeconds * (1 << uint(delivery.Attempt-1))
		}

		log.Printf("Webhook delivery failed (status %d), scheduling retry in %ds", resp.StatusCode, backoffSeconds)

		// In production, use a task queue or scheduled job to retry
		// For now, just log it
	}

	log.Printf("Webhook delivered: %s (status: %d, time: %dms)", webhook.WebhookID, resp.StatusCode, delivery.ResponseTime)
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

// RetryWebhookDelivery retries a failed webhook delivery
func (s *WebhookService) RetryWebhookDelivery(ctx context.Context, deliveryID string) error {
	delivery, err := s.db.GetWebhookDelivery(ctx, deliveryID)
	if err != nil {
		return fmt.Errorf("delivery not found")
	}

	webhook, err := s.db.GetWebhook(ctx, delivery.WebhookID)
	if err != nil {
		return fmt.Errorf("webhook not found")
	}

	if delivery.Attempt >= webhook.MaxRetries {
		return fmt.Errorf("max retries exceeded")
	}

	// Create new delivery attempt
	newDelivery := &WebhookDelivery{
		DeliveryID:   uuid.New().String(),
		WebhookID:    delivery.WebhookID,
		EventID:      delivery.EventID,
		EventType:    delivery.EventType,
		Attempt:      delivery.Attempt + 1,
		CreatedAt:    time.Now(),
	}

	// In production, reconstruct the original event from storage
	// For now, log it
	log.Printf("Retrying webhook delivery: %s (attempt %d)", newDelivery.DeliveryID, newDelivery.Attempt)

	return nil
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

	if webhookID == "" {
		http.Error(w, "webhook_id required", http.StatusBadRequest)
		return
	}

	deliveries, err := s.GetWebhookDeliveries(ctx, webhookID, 100)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch deliveries: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deliveries)
}

// Webhook represents a webhook configuration
type Webhook struct {
	WebhookID         string            `json:"webhook_id"`
	CustomerID        string            `json:"customer_id"`
	URL               string            `json:"url"`
	Secret            string            `json:"secret,omitempty"`
	Events            []string          `json:"events"`
	IsActive          bool              `json:"is_active"`
	MaxRetries        int               `json:"max_retries"`
	BackoffSeconds    int               `json:"backoff_seconds"`
	ExponentialBackoff bool             `json:"exponential_backoff"`
	CustomHeaders     map[string]string `json:"custom_headers,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}
