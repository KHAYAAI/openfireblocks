package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// MarketplaceService manages integrations and API integrations
type MarketplaceService struct {
	db *PostgresDB
}

// Integration represents a third-party integration
type Integration struct {
	IntegrationID string            `json:"integration_id"`
	CustomerID    string            `json:"customer_id"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Type          string            `json:"type"` // webhook, api_key, oauth, custom
	Status        string            `json:"status"` // active, inactive, suspended
	Config        IntegrationConfig `json:"config"`
	WebhookURL    string            `json:"webhook_url,omitempty"`
	WebhookSecret string            `json:"webhook_secret,omitempty"`
	ApiKey        string            `json:"api_key,omitempty"`
	Events        []string          `json:"events"` // signing, key_created, etc.
	RetryPolicy   *RetryPolicy      `json:"retry_policy,omitempty"`
	RateLimit     *RateLimitConfig  `json:"rate_limit,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	LastActivated *time.Time        `json:"last_activated,omitempty"`
}

// IntegrationConfig contains integration-specific configuration
type IntegrationConfig struct {
	Timeout         int               `json:"timeout_seconds,omitempty"`
	MaxRetries      int               `json:"max_retries,omitempty"`
	CustomHeaders   map[string]string `json:"custom_headers,omitempty"`
	AuthType        string            `json:"auth_type,omitempty"` // bearer, basic, api_key
	AuthValue       string            `json:"auth_value,omitempty"`
	VerifySSL       bool              `json:"verify_ssl"`
}

// RetryPolicy defines webhook retry behavior
type RetryPolicy struct {
	MaxAttempts    int `json:"max_attempts"`
	BackoffSeconds int `json:"backoff_seconds"`
	Exponential    bool `json:"exponential"`
}

// RateLimitConfig defines rate limiting for webhooks
type RateLimitConfig struct {
	MaxPerMinute int `json:"max_per_minute"`
	MaxPerHour   int `json:"max_per_hour"`
}

// WebhookEvent represents an event sent to webhooks
type WebhookEvent struct {
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	Timestamp     time.Time `json:"timestamp"`
	Data          interface{} `json:"data"`
	IntegrationID string    `json:"integration_id"`
	Signature     string    `json:"signature"`
}

// CreateIntegrationRequest is the request to create an integration
type CreateIntegrationRequest struct {
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Type          string            `json:"type"`
	WebhookURL    string            `json:"webhook_url,omitempty"`
	Events        []string          `json:"events"`
	Config        IntegrationConfig `json:"config,omitempty"`
	RetryPolicy   *RetryPolicy      `json:"retry_policy,omitempty"`
	RateLimit     *RateLimitConfig  `json:"rate_limit,omitempty"`
}

// IntegrationTestRequest is used to test an integration
type IntegrationTestRequest struct {
	EventType string      `json:"event_type"`
	TestData  interface{} `json:"test_data,omitempty"`
}

// WebhookLog represents a webhook delivery log
type WebhookLog struct {
	LogID          string    `json:"log_id"`
	IntegrationID  string    `json:"integration_id"`
	EventID        string    `json:"event_id"`
	EventType      string    `json:"event_type"`
	StatusCode     int       `json:"status_code"`
	ResponseTime   int64     `json:"response_time_ms"`
	Success        bool      `json:"success"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	Attempt        int       `json:"attempt"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// NewMarketplaceService creates a new marketplace service
func NewMarketplaceService(db *PostgresDB) *MarketplaceService {
	return &MarketplaceService{
		db: db,
	}
}

// CreateIntegration creates a new integration
func (s *MarketplaceService) CreateIntegration(ctx context.Context, customerID string, req *CreateIntegrationRequest) (*Integration, error) {
	// Validate request
	if req.Name == "" || req.Type == "" {
		return nil, fmt.Errorf("name and type are required")
	}

	if req.Type == "webhook" && req.WebhookURL == "" {
		return nil, fmt.Errorf("webhook_url required for webhook type")
	}

	// Generate API key if needed
	apiKey := ""
	if req.Type == "api_key" {
		apiKey = generateAPIKey()
	}

	// Generate webhook secret
	webhookSecret := generateWebhookSecret()

	integration := &Integration{
		IntegrationID: uuid.New().String(),
		CustomerID:    customerID,
		Name:          req.Name,
		Description:   req.Description,
		Type:          req.Type,
		Status:        "active",
		Config:        req.Config,
		WebhookURL:    req.WebhookURL,
		WebhookSecret: webhookSecret,
		ApiKey:        apiKey,
		Events:        req.Events,
		RetryPolicy:   req.RetryPolicy,
		RateLimit:     req.RateLimit,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	// Set default retry policy
	if integration.RetryPolicy == nil {
		integration.RetryPolicy = &RetryPolicy{
			MaxAttempts:    3,
			BackoffSeconds: 5,
			Exponential:    true,
		}
	}

	// Store integration
	if err := s.db.CreateIntegration(ctx, integration); err != nil {
		log.Printf("Failed to store integration: %v", err)
		return nil, fmt.Errorf("failed to create integration: %w", err)
	}

	log.Printf("Integration created: %s for customer: %s", integration.IntegrationID, customerID)

	return integration, nil
}

// GetIntegration retrieves an integration by ID
func (s *MarketplaceService) GetIntegration(ctx context.Context, integrationID string) (*Integration, error) {
	return s.db.GetIntegration(ctx, integrationID)
}

// ListIntegrations lists integrations for a customer
func (s *MarketplaceService) ListIntegrations(ctx context.Context, customerID string) ([]*Integration, error) {
	return s.db.ListIntegrations(ctx, customerID)
}

// UpdateIntegration updates an integration
func (s *MarketplaceService) UpdateIntegration(ctx context.Context, integrationID string, updates *Integration) (*Integration, error) {
	integration, err := s.db.GetIntegration(ctx, integrationID)
	if err != nil {
		return nil, fmt.Errorf("integration not found")
	}

	// Update fields
	if updates.Name != "" {
		integration.Name = updates.Name
	}
	if updates.Description != "" {
		integration.Description = updates.Description
	}
	if updates.WebhookURL != "" {
		integration.WebhookURL = updates.WebhookURL
	}
	if len(updates.Events) > 0 {
		integration.Events = updates.Events
	}
	if updates.Status != "" {
		integration.Status = updates.Status
	}

	integration.UpdatedAt = time.Now()

	// Store updates
	if err := s.db.UpdateIntegration(ctx, integration); err != nil {
		log.Printf("Failed to update integration: %v", err)
		return nil, fmt.Errorf("failed to update integration: %w", err)
	}

	return integration, nil
}

// DeleteIntegration deletes an integration
func (s *MarketplaceService) DeleteIntegration(ctx context.Context, integrationID string) error {
	if err := s.db.DeleteIntegration(ctx, integrationID); err != nil {
		log.Printf("Failed to delete integration: %v", err)
		return fmt.Errorf("failed to delete integration: %w", err)
	}

	log.Printf("Integration deleted: %s", integrationID)
	return nil
}

// TestIntegration sends a test webhook event
func (s *MarketplaceService) TestIntegration(ctx context.Context, integrationID string, req *IntegrationTestRequest) error {
	integration, err := s.GetIntegration(ctx, integrationID)
	if err != nil {
		return fmt.Errorf("integration not found")
	}

	if integration.Type != "webhook" {
		return fmt.Errorf("test only supports webhook type")
	}

	// Create test event
	event := &WebhookEvent{
		EventID:       uuid.New().String(),
		EventType:     req.EventType,
		Timestamp:     time.Now(),
		Data:          req.TestData,
		IntegrationID: integrationID,
	}

	// Send test webhook
	return s.sendWebhook(ctx, integration, event)
}

// SendEvent sends an event to relevant integrations
func (s *MarketplaceService) SendEvent(ctx context.Context, customerID string, eventType string, data interface{}) error {
	integrations, err := s.ListIntegrations(ctx, customerID)
	if err != nil {
		log.Printf("Failed to list integrations: %v", err)
		return nil
	}

	for _, integration := range integrations {
		if integration.Status != "active" {
			continue
		}

		// Check if integration is subscribed to this event
		eventSubscribed := false
		for _, event := range integration.Events {
			if event == eventType || event == "*" {
				eventSubscribed = true
				break
			}
		}

		if !eventSubscribed {
			continue
		}

		// Send event in background
		go func(integ *Integration) {
			webhookEvent := &WebhookEvent{
				EventID:       uuid.New().String(),
				EventType:     eventType,
				Timestamp:     time.Now(),
				Data:          data,
				IntegrationID: integ.IntegrationID,
			}

			if err := s.sendWebhook(context.Background(), integ, webhookEvent); err != nil {
				log.Printf("Failed to send webhook to %s: %v", integ.WebhookURL, err)
			}
		}(integration)
	}

	return nil
}

// sendWebhook sends a webhook to a specific integration
func (s *MarketplaceService) sendWebhook(ctx context.Context, integration *Integration, event *WebhookEvent) error {
	// Sign the event with webhook secret
	event.Signature = generateSignature(event, integration.WebhookSecret)

	// Marshal event
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, integration.WebhookURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", event.Signature)
	req.Header.Set("X-Event-Type", event.EventType)
	req.Header.Set("X-Event-ID", event.EventID)

	// Add custom headers
	for k, v := range integration.Config.CustomHeaders {
		req.Header.Set(k, v)
	}

	// Send request
	client := &http.Client{
		Timeout: time.Duration(integration.Config.Timeout) * time.Second,
	}

	startTime := time.Now()
	resp, err := client.Do(req)
	responseTime := time.Since(startTime).Milliseconds()

	// Log webhook attempt
	logEntry := &WebhookLog{
		LogID:         uuid.New().String(),
		IntegrationID: integration.IntegrationID,
		EventID:       event.EventID,
		EventType:     event.EventType,
		Attempt:       1,
		ResponseTime:  responseTime,
		CreatedAt:     time.Now(),
	}

	if err != nil {
		logEntry.Success = false
		logEntry.ErrorMessage = err.Error()
		s.db.CreateWebhookLog(context.Background(), logEntry)
		return err
	}

	defer resp.Body.Close()

	logEntry.StatusCode = resp.StatusCode
	logEntry.Success = resp.StatusCode >= 200 && resp.StatusCode < 300

	// Store log
	if err := s.db.CreateWebhookLog(context.Background(), logEntry); err != nil {
		log.Printf("Failed to store webhook log: %v", err)
	}

	if !logEntry.Success {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	// Update last activated time
	now := time.Now()
	integration.LastActivated = &now
	s.db.UpdateIntegration(context.Background(), integration)

	return nil
}

// Helper functions

func generateAPIKey() string {
	return "sk_" + uuid.New().String()
}

func generateWebhookSecret() string {
	return uuid.New().String()
}

func generateSignature(event *WebhookEvent, secret string) string {
	// In production, use HMAC-SHA256 signing
	// For now, return a placeholder
	return "sig_" + uuid.New().String()
}

// HTTP Handlers

// HandleCreateIntegration is the HTTP handler for creating integrations
func (s *MarketplaceService) HandleCreateIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID := r.Header.Get("X-Customer-ID")

	if customerID == "" {
		http.Error(w, "X-Customer-ID header required", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	integration, err := s.CreateIntegration(ctx, customerID, &req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create integration: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(integration)
}

// HandleListIntegrations is the HTTP handler to list integrations
func (s *MarketplaceService) HandleListIntegrations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID := r.Header.Get("X-Customer-ID")

	if customerID == "" {
		http.Error(w, "X-Customer-ID header required", http.StatusBadRequest)
		return
	}

	integrations, err := s.ListIntegrations(ctx, customerID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch integrations: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(integrations)
}

// HandleGetIntegration is the HTTP handler to get an integration
func (s *MarketplaceService) HandleGetIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	integrationID := r.URL.Query().Get("integration_id")

	if integrationID == "" {
		http.Error(w, "integration_id required", http.StatusBadRequest)
		return
	}

	integration, err := s.GetIntegration(ctx, integrationID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Not found: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(integration)
}

// HandleTestIntegration is the HTTP handler to test an integration
func (s *MarketplaceService) HandleTestIntegration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	integrationID := r.URL.Query().Get("integration_id")

	if integrationID == "" {
		http.Error(w, "integration_id required", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req IntegrationTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.TestIntegration(ctx, integrationID, &req); err != nil {
		http.Error(w, fmt.Sprintf("Test failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
