package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// BillingService manages customer billing, subscriptions, and usage metrics.
type BillingService struct {
	db      *PostgresDB
	stripe  *StripeClient // Stripe integration
	metrics *MetricsStore
}

// Plan represents a subscription plan.
type Plan struct {
	PlanID           string            `json:"plan_id"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Price            int               `json:"price"` // in cents
	Currency         string            `json:"currency"`
	BillingCycle     string            `json:"billing_cycle"` // monthly, yearly
	SigningLimit     int               `json:"signing_limit"`
	KeyLimit         int               `json:"key_limit"`
	SupportLevel     string            `json:"support_level"` // basic, standard, premium
	Features         []string          `json:"features"`
	CreatedAt        time.Time         `json:"created_at"`
}

// Subscription represents an active subscription.
type Subscription struct {
	SubscriptionID   string    `json:"subscription_id"`
	CustomerID       string    `json:"customer_id"`
	PlanID           string    `json:"plan_id"`
	Status           string    `json:"status"` // active, paused, canceled, past_due
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
	CanceledAt       *time.Time `json:"canceled_at,omitempty"`
	TrialEndsAt      *time.Time `json:"trial_ends_at,omitempty"`
	AutoRenew        bool       `json:"auto_renew"`
	PaymentMethod    string     `json:"payment_method"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// Invoice represents a billing invoice.
type Invoice struct {
	InvoiceID        string         `json:"invoice_id"`
	SubscriptionID   string         `json:"subscription_id"`
	CustomerID       string         `json:"customer_id"`
	Amount           int            `json:"amount"` // in cents
	Currency         string         `json:"currency"`
	Status           string         `json:"status"` // paid, unpaid, overdue
	DueDate          time.Time      `json:"due_date"`
	PaidAt           *time.Time     `json:"paid_at,omitempty"`
	LineItems        []LineItem     `json:"line_items"`
	CreatedAt        time.Time      `json:"created_at"`
}

// LineItem represents a single line in an invoice.
type LineItem struct {
	Description string `json:"description"`
	Quantity    int    `json:"quantity"`
	UnitPrice   int    `json:"unit_price"` // in cents
	Amount      int    `json:"amount"`     // in cents
}

// UsageMetrics tracks customer usage.
type UsageMetrics struct {
	MetricsID          string    `json:"metrics_id"`
	SubscriptionID     string    `json:"subscription_id"`
	CustomerID         string    `json:"customer_id"`
	PeriodStart        time.Time `json:"period_start"`
	PeriodEnd          time.Time `json:"period_end"`
	SigningRequests    int       `json:"signing_requests"`
	KeyOperations      int       `json:"key_operations"`
	APIRequests        int       `json:"api_requests"`
	DataTransferGB     float64   `json:"data_transfer_gb"`
	AvailableSignings  int       `json:"available_signings"`
	AvailableKeys      int       `json:"available_keys"`
	CreatedAt          time.Time `json:"created_at"`
}

// PricingTier represents usage-based pricing.
type PricingTier struct {
	From           int    `json:"from"` // Usage threshold start
	To             int    `json:"to"`   // Usage threshold end (0 = unlimited)
	PricePerUnit   int    `json:"price_per_unit"`
	Currency       string `json:"currency"`
}

// NewBillingService creates a new billing service.
func NewBillingService(db *PostgresDB, stripe *StripeClient) *BillingService {
	return &BillingService{
		db:      db,
		stripe:  stripe,
		metrics: NewMetricsStore(),
	}
}

// CreatePlan creates a new subscription plan.
func (b *BillingService) CreatePlan(ctx context.Context, plan *Plan) error {
	plan.PlanID = uuid.New().String()
	plan.CreatedAt = time.Now()

	if err := b.db.CreatePlan(ctx, plan); err != nil {
		return fmt.Errorf("failed to create plan: %w", err)
	}

	log.Printf("Created plan: %s (%s)", plan.PlanID, plan.Name)
	return nil
}

// Subscribe creates a new subscription for a customer.
func (b *BillingService) Subscribe(ctx context.Context, customerID, planID string) (*Subscription, error) {
	plan, err := b.db.GetPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %w", err)
	}

	// Create subscription in database
	subscription := &Subscription{
		SubscriptionID:  uuid.New().String(),
		CustomerID:      customerID,
		PlanID:          planID,
		Status:          "active",
		CurrentPeriodStart: time.Now(),
		CurrentPeriodEnd:   time.Now().AddDate(0, 1, 0),
		TrialEndsAt:     nil,
		AutoRenew:       true,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := b.db.CreateSubscription(ctx, subscription); err != nil {
		return nil, fmt.Errorf("failed to create subscription: %w", err)
	}

	// Charge for subscription (if not on trial)
	invoice := &Invoice{
		InvoiceID:      uuid.New().String(),
		SubscriptionID: subscription.SubscriptionID,
		CustomerID:     customerID,
		Amount:         plan.Price,
		Currency:       plan.Currency,
		Status:         "unpaid",
		DueDate:        time.Now().Add(30 * 24 * time.Hour),
		LineItems: []LineItem{
			{
				Description: plan.Name,
				Quantity:    1,
				UnitPrice:   plan.Price,
				Amount:      plan.Price,
			},
		},
		CreatedAt: time.Now(),
	}

	if err := b.db.CreateInvoice(ctx, invoice); err != nil {
		log.Printf("Failed to create invoice: %v", err)
	}

	log.Printf("Created subscription: %s for customer %s (plan: %s)",
		subscription.SubscriptionID, customerID, planID)

	return subscription, nil
}

// CancelSubscription cancels an active subscription.
func (b *BillingService) CancelSubscription(ctx context.Context, subscriptionID string, immediate bool) error {
	subscription, err := b.db.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("subscription not found: %w", err)
	}

	if immediate {
		subscription.Status = "canceled"
		now := time.Now()
		subscription.CanceledAt = &now
	} else {
		subscription.Status = "scheduled_for_cancellation"
		subscription.CurrentPeriodEnd = time.Now()
	}

	subscription.UpdatedAt = time.Now()

	if err := b.db.UpdateSubscription(ctx, subscription); err != nil {
		return fmt.Errorf("failed to cancel subscription: %w", err)
	}

	log.Printf("Canceled subscription: %s", subscriptionID)
	return nil
}

// GetSubscription retrieves a subscription.
func (b *BillingService) GetSubscription(ctx context.Context, subscriptionID string) (*Subscription, error) {
	return b.db.GetSubscription(ctx, subscriptionID)
}

// ListSubscriptions lists subscriptions for a customer.
func (b *BillingService) ListSubscriptions(ctx context.Context, customerID string) ([]*Subscription, error) {
	return b.db.ListSubscriptions(ctx, customerID)
}

// RecordUsage records customer usage metrics.
func (b *BillingService) RecordUsage(ctx context.Context, subscriptionID string, usage map[string]int) error {
	subscription, err := b.db.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("subscription not found: %w", err)
	}

	plan, err := b.db.GetPlan(ctx, subscription.PlanID)
	if err != nil {
		return fmt.Errorf("plan not found: %w", err)
	}

	// Check usage limits
	signingRequests := usage["signing_requests"]
	if signingRequests > plan.SigningLimit {
		log.Printf("Usage exceeded for subscription %s: %d > %d",
			subscriptionID, signingRequests, plan.SigningLimit)
		// Could trigger overage charges or throttling
	}

	metrics := &UsageMetrics{
		MetricsID:       uuid.New().String(),
		SubscriptionID:  subscriptionID,
		CustomerID:      subscription.CustomerID,
		PeriodStart:     subscription.CurrentPeriodStart,
		PeriodEnd:       subscription.CurrentPeriodEnd,
		SigningRequests: signingRequests,
		KeyOperations:   usage["key_operations"],
		APIRequests:     usage["api_requests"],
		AvailableSignings: plan.SigningLimit - signingRequests,
		AvailableKeys:   plan.KeyLimit - usage["key_operations"],
		CreatedAt:       time.Now(),
	}

	if err := b.db.CreateUsageMetrics(ctx, metrics); err != nil {
		log.Printf("Failed to record usage: %v", err)
	}

	return nil
}

// GetUsageMetrics retrieves usage metrics for a subscription.
func (b *BillingService) GetUsageMetrics(ctx context.Context, subscriptionID string) (*UsageMetrics, error) {
	return b.db.GetLatestUsageMetrics(ctx, subscriptionID)
}

// GetInvoices retrieves invoices for a customer.
func (b *BillingService) GetInvoices(ctx context.Context, customerID string) ([]*Invoice, error) {
	return b.db.GetInvoices(ctx, customerID)
}

// HandleSubscribe is the HTTP handler for creating subscriptions.
func (b *BillingService) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		CustomerID string `json:"customer_id"`
		PlanID     string `json:"plan_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	subscription, err := b.Subscribe(ctx, req.CustomerID, req.PlanID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create subscription: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(subscription)
}

// HandleGetUsage is the HTTP handler for retrieving usage metrics.
func (b *BillingService) HandleGetUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subscriptionID := r.URL.Query().Get("subscription_id")

	if subscriptionID == "" {
		http.Error(w, "subscription_id required", http.StatusBadRequest)
		return
	}

	metrics, err := b.GetUsageMetrics(ctx, subscriptionID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get usage: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// StripeClient represents a Stripe integration client.
type StripeClient struct {
	apiKey string
	client *http.Client
}

// NewStripeClient creates a new Stripe client.
func NewStripeClient(apiKey string) *StripeClient {
	return &StripeClient{
		apiKey: apiKey,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// CreatePaymentIntent creates a Stripe payment intent.
func (s *StripeClient) CreatePaymentIntent(amount int, currency string) (string, error) {
	// Generate a pseudo-random intent ID for demo
	b := make([]byte, 16)
	rand.Read(b)
	intentID := "pi_" + hex.EncodeToString(b)[:20]

	return intentID, nil
}

// MetricsStore is an in-memory store for metrics.
type MetricsStore struct {
	metrics map[string]*UsageMetrics
}

// NewMetricsStore creates a new metrics store.
func NewMetricsStore() *MetricsStore {
	return &MetricsStore{
		metrics: make(map[string]*UsageMetrics),
	}
}

// Store stores metrics in memory.
func (m *MetricsStore) Store(metrics *UsageMetrics) {
	m.metrics[metrics.MetricsID] = metrics
}

// Get retrieves metrics from memory.
func (m *MetricsStore) Get(metricsID string) *UsageMetrics {
	return m.metrics[metricsID]
}
