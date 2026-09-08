package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// A direct client for the Stripe REST API.
//
// This replaces a stub that returned a locally-generated string shaped like
// a Stripe object id ("pi_" + hex) as if Stripe had been called. Stripe had
// never heard of that id, so anything that stored it -- an invoice record, a
// reconciliation report, a support ticket -- carried a real-looking payment
// reference for a payment that did not exist. The stub was later changed to
// fail loudly instead, which was correct but left billing unable to collect
// money.
//
// Written against the HTTP API rather than the official SDK: the surface
// used here is four endpoints of form-encoded POSTs, and the SDK's value is
// mostly in the breadth this does not need. That trade would be wrong if
// this grew to handle subscriptions and webhooks natively.
type StripeClient struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// stripeAPIBase is where requests go unless overridden.
//
// Overridable through STRIPE_API_BASE so the tests can point at a stub that
// asserts on the exact request Stripe would receive. Verifying the request
// shape without a live secret key is the only way to test this at all --
// and the shape is where the mistakes are (wrong units, missing idempotency
// key, amount as a float).
const stripeAPIBase = "https://api.stripe.com"

func NewStripeClient(apiKey string) *StripeClient {
	base := os.Getenv("STRIPE_API_BASE")
	if base == "" {
		base = stripeAPIBase
	}
	return &StripeClient{
		apiKey:  apiKey,
		baseURL: strings.TrimSuffix(base, "/"),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Configured reports whether this client can reach Stripe at all.
//
// Billing is optional in a self-hosted deployment -- an operator running
// this for their own institution has nobody to charge -- so an unconfigured
// client is a valid state, not an error. Callers check this and skip
// collection rather than failing a request.
func (s *StripeClient) Configured() bool { return s.apiKey != "" }

// stripeError is Stripe's error envelope.
type stripeError struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Param   string `json:"param"`
	} `json:"error"`
}

// PaymentIntent is the subset of Stripe's object this service uses.
type PaymentIntent struct {
	ID           string `json:"id"`
	Amount       int    `json:"amount"`
	Currency     string `json:"currency"`
	Status       string `json:"status"`
	ClientSecret string `json:"client_secret"`
	Customer     string `json:"customer"`
}

// StripeCustomer is the subset of Stripe's customer object used here.
type StripeCustomer struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// post sends a form-encoded request and decodes the JSON response.
//
// idempotencyKey is not optional in practice and the signature makes it
// awkward to omit deliberately: every write to Stripe here can be retried by
// a caller, a workflow, or an operator hitting a button twice, and a
// duplicated payment intent is real money charged twice. Stripe deduplicates
// on this header for 24 hours.
func (s *StripeClient) post(ctx context.Context, path string, form url.Values, idempotencyKey string, out interface{}) error {
	if !s.Configured() {
		return fmt.Errorf("stripe is not configured: no API key set")
	}
	if idempotencyKey == "" {
		return fmt.Errorf("refusing to POST %s to Stripe without an idempotency key", path)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("failed to build Stripe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	req.Header.Set("Stripe-Version", stripeAPIVersion)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("stripe request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("failed to read Stripe response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var se stripeError
		if json.Unmarshal(body, &se) == nil && se.Error.Message != "" {
			return fmt.Errorf("stripe %s: %s (type=%s code=%s param=%s)",
				resp.Status, se.Error.Message, se.Error.Type, se.Error.Code, se.Error.Param)
		}
		return fmt.Errorf("stripe %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("failed to decode Stripe response: %w", err)
		}
	}
	return nil
}

// stripeAPIVersion pins the API version.
//
// Without it Stripe uses whatever version the account is defaulted to, which
// an administrator can change in the dashboard -- so the shape of responses
// this code parses could change without a deploy. Pinned so an upgrade is a
// code change someone reviews.
const stripeAPIVersion = "2024-06-20"

// CreateCustomer registers a tenant with Stripe.
//
// The idempotency key is the platform's own customer id: creating the same
// tenant twice is a duplicate billing relationship, and this is exactly the
// call a retried provisioning workflow would repeat.
func (s *StripeClient) CreateCustomer(ctx context.Context, customerID, email, name string) (*StripeCustomer, error) {
	form := url.Values{}
	form.Set("email", email)
	form.Set("name", name)
	// Ties the Stripe object back to the local tenant, so reconciliation
	// does not depend on matching by email.
	form.Set("metadata[openfireblocks_customer_id]", customerID)

	var out StripeCustomer
	if err := s.post(ctx, "/v1/customers", form, "customer:"+customerID, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePaymentIntent starts a payment.
//
// amountCents is an integer count of the currency's smallest unit, which is
// what Stripe expects and what Invoice.Amount already holds. Taking a float
// here would invite a caller to pass 10.99 and be charged 10 cents.
func (s *StripeClient) CreatePaymentIntent(
	ctx context.Context,
	amountCents int,
	currency string,
	stripeCustomerID string,
	idempotencyKey string,
	metadata map[string]string,
) (*PaymentIntent, error) {
	if amountCents <= 0 {
		return nil, fmt.Errorf("refusing to create a payment intent for %d cents", amountCents)
	}
	if currency == "" {
		return nil, fmt.Errorf("currency is required")
	}

	form := url.Values{}
	form.Set("amount", strconv.Itoa(amountCents))
	form.Set("currency", strings.ToLower(currency))
	if stripeCustomerID != "" {
		form.Set("customer", stripeCustomerID)
	}
	for k, v := range metadata {
		form.Set("metadata["+k+"]", v)
	}

	var out PaymentIntent
	if err := s.post(ctx, "/v1/payment_intents", form, idempotencyKey, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ChargeInvoice collects payment for one invoice.
//
// The idempotency key is derived from the invoice id, so retrying this --
// from a workflow, a cron, or an operator -- reuses the existing payment
// intent instead of charging the customer a second time. That property is
// the whole reason this method exists rather than callers reaching for
// CreatePaymentIntent directly.
func (b *BillingService) ChargeInvoice(ctx context.Context, invoice *Invoice, stripeCustomerID string) (*PaymentIntent, error) {
	if invoice == nil {
		return nil, fmt.Errorf("no invoice given")
	}
	if invoice.Status == "paid" {
		return nil, fmt.Errorf("invoice %s is already paid", invoice.InvoiceID)
	}
	if !b.stripe.Configured() {
		return nil, fmt.Errorf("stripe is not configured; cannot collect payment for invoice %s", invoice.InvoiceID)
	}

	return b.stripe.CreatePaymentIntent(ctx, invoice.Amount, invoice.Currency, stripeCustomerID,
		"invoice:"+invoice.InvoiceID,
		map[string]string{
			"openfireblocks_invoice_id":      invoice.InvoiceID,
			"openfireblocks_customer_id":     invoice.CustomerID,
			"openfireblocks_subscription_id": invoice.SubscriptionID,
		})
}
