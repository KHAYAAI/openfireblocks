package main

import (
	"context"
	"encoding/json"
	"errors"
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
	// Sent on creation and, until now, discarded on the way back.
	//
	// This carries openfireblocks_customer_id, which is the only thing
	// linking a payment in Stripe to a tenant here. Reading it back is how
	// you confirm the link actually exists rather than assuming the field
	// was accepted -- Stripe silently ignores metadata keys it considers
	// malformed, and a charge nobody can attribute is a reconciliation
	// problem discovered at month end.
	Metadata map[string]string `json:"metadata"`
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
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	return s.do(req, out)
}

// get reads from Stripe. No idempotency key: a read has nothing to
// deduplicate.
func (s *StripeClient) get(ctx context.Context, path string, query url.Values, out interface{}) error {
	if !s.Configured() {
		return fmt.Errorf("stripe is not configured: no API key set")
	}
	u := s.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("failed to build Stripe request: %w", err)
	}
	return s.do(req, out)
}

// do sends a prepared request with the pinned version and credentials.
func (s *StripeClient) do(req *http.Request, out interface{}) error {
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
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

// CreatePaymentIntent charges a saved payment method, now.
//
// It used to create an intent and stop. With no payment_method and no
// confirm, Stripe leaves the intent in requires_payment_method forever, and
// nothing in the platform ever collected a card to attach -- so no invoice
// could ever have been paid, whatever the account or the keys. This charges:
//
//   - payment_method is the customer's saved card;
//   - confirm=true takes the money in this call rather than leaving an intent
//     for a front end that does not exist;
//   - off_session=true says the customer is not present, which is true of a
//     scheduled run and tells the issuer this is a merchant-initiated charge;
//   - error_on_requires_action=true makes a card that wants 3-D Secure fail
//     the call instead of leaving a half-finished intent no one will finish.
//
// amountCents is an integer count of the currency's smallest unit, which is
// what Stripe expects and what Invoice.Amount already holds. Taking a float
// here would invite a caller to pass 10.99 and be charged 10 cents.
func (s *StripeClient) CreatePaymentIntent(
	ctx context.Context,
	amountCents int,
	currency string,
	stripeCustomerID string,
	paymentMethodID string,
	idempotencyKey string,
	metadata map[string]string,
) (*PaymentIntent, error) {
	if amountCents <= 0 {
		return nil, fmt.Errorf("refusing to create a payment intent for %d cents", amountCents)
	}
	if currency == "" {
		return nil, fmt.Errorf("currency is required")
	}
	if stripeCustomerID == "" || paymentMethodID == "" {
		return nil, fmt.Errorf("a charge needs both a customer and a payment method")
	}

	form := url.Values{}
	form.Set("amount", strconv.Itoa(amountCents))
	form.Set("currency", strings.ToLower(currency))
	form.Set("customer", stripeCustomerID)
	form.Set("payment_method", paymentMethodID)
	form.Set("confirm", "true")
	form.Set("off_session", "true")
	form.Set("error_on_requires_action", "true")
	for k, v := range metadata {
		form.Set("metadata["+k+"]", v)
	}

	var out PaymentIntent
	if err := s.post(ctx, "/v1/payment_intents", form, idempotencyKey, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ErrNoPaymentMethod means the customer has no saved card to charge. Not a
// fault in the run: the customer has to add one, which they do through a
// setup session (CreateSetupSession).
var ErrNoPaymentMethod = errors.New("no payment method on file")

// DefaultPaymentMethod returns the card to charge for a customer: the one
// they marked as their invoicing default, else their first saved card. Empty
// when they have none.
func (s *StripeClient) DefaultPaymentMethod(ctx context.Context, stripeCustomerID string) (string, error) {
	var c struct {
		InvoiceSettings struct {
			DefaultPaymentMethod string `json:"default_payment_method"`
		} `json:"invoice_settings"`
	}
	if err := s.get(ctx, "/v1/customers/"+url.PathEscape(stripeCustomerID), nil, &c); err != nil {
		return "", err
	}
	if c.InvoiceSettings.DefaultPaymentMethod != "" {
		return c.InvoiceSettings.DefaultPaymentMethod, nil
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	q := url.Values{"customer": {stripeCustomerID}, "type": {"card"}, "limit": {"1"}}
	if err := s.get(ctx, "/v1/payment_methods", q, &list); err != nil {
		return "", err
	}
	if len(list.Data) == 0 {
		return "", nil
	}
	return list.Data[0].ID, nil
}

// CardSummary is what is safe to show about a saved card.
type CardSummary struct {
	Brand    string `json:"brand"`
	Last4    string `json:"last4"`
	ExpMonth int    `json:"exp_month"`
	ExpYear  int    `json:"exp_year"`
}

// CardOnFile describes the card collection would charge, or nil if none.
func (s *StripeClient) CardOnFile(ctx context.Context, stripeCustomerID string) (*CardSummary, error) {
	pm, err := s.DefaultPaymentMethod(ctx, stripeCustomerID)
	if err != nil || pm == "" {
		return nil, err
	}
	var out struct {
		Card CardSummary `json:"card"`
	}
	if err := s.get(ctx, "/v1/payment_methods/"+url.PathEscape(pm), nil, &out); err != nil {
		return nil, err
	}
	return &out.Card, nil
}

// SucceededIntentForInvoice finds a payment that already took this invoice's
// money, or nil.
//
// An idempotency key only protects a retry that sends the same request. If an
// invoice was paid but marking it paid failed, and the customer then changed
// their card, the next attempt is a different request and would charge twice.
// Looking for the payment first closes that: the invoice id is in the intent's
// metadata, and a success found here is returned for the collector to mark
// paid instead of charging again. Search is eventually consistent (seconds to
// a minute), which is why the idempotency key stays as well.
func (s *StripeClient) SucceededIntentForInvoice(ctx context.Context, invoiceID string) (*PaymentIntent, error) {
	var out struct {
		Data []PaymentIntent `json:"data"`
	}
	q := url.Values{"query": {fmt.Sprintf("metadata['openfireblocks_invoice_id']:'%s' AND status:'succeeded'", strings.ReplaceAll(invoiceID, "'", ""))}}
	if err := s.get(ctx, "/v1/payment_intents/search", q, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	return &out.Data[0], nil
}

// SetupSession is a Stripe-hosted page where a customer saves a card.
type SetupSession struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// CreateSetupSession starts a Stripe Checkout session in setup mode.
//
// Hosted by Stripe rather than built on Stripe.js: the console's content
// security policy allows scripts from this origin only, and a card form on a
// page we serve would put card entry in our PCI scope. The customer is sent
// to Stripe, saves a card, and is sent back; the card is then on the Stripe
// customer and DefaultPaymentMethod finds it.
func (s *StripeClient) CreateSetupSession(ctx context.Context, stripeCustomerID, successURL, cancelURL, idempotencyKey string) (*SetupSession, error) {
	if stripeCustomerID == "" || successURL == "" || cancelURL == "" {
		return nil, fmt.Errorf("a setup session needs a customer, a success URL and a cancel URL")
	}
	form := url.Values{}
	form.Set("mode", "setup")
	form.Set("customer", stripeCustomerID)
	form.Add("payment_method_types[]", "card")
	form.Set("success_url", successURL)
	form.Set("cancel_url", cancelURL)
	var out SetupSession
	if err := s.post(ctx, "/v1/checkout/sessions", form, idempotencyKey, &out); err != nil {
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
		return nil, fmt.Errorf("%w: invoice %s is already paid", ErrInvalidInput, invoice.InvoiceID)
	}
	if !b.stripe.Configured() {
		// Unavailable rather than a server error: the request is fine and
		// the deployment is not finished. Reported as 503 so an operator
		// sees a missing dependency instead of a crash, and so a caller
		// knows retrying after configuration will work.
		return nil, fmt.Errorf("%w: no payment processor is configured, so invoice %s cannot be charged",
			ErrUnavailable, invoice.InvoiceID)
	}

	// A payment already taken for this invoice is returned, not repeated.
	if done, err := b.stripe.SucceededIntentForInvoice(ctx, invoice.InvoiceID); err != nil {
		// Fail closed: if we cannot tell whether it was already paid, charging
		// risks charging twice. The next run retries.
		return nil, fmt.Errorf("could not check whether invoice %s was already paid: %w", invoice.InvoiceID, err)
	} else if done != nil {
		return done, nil
	}

	pm, err := b.stripe.DefaultPaymentMethod(ctx, stripeCustomerID)
	if err != nil {
		return nil, fmt.Errorf("could not read the customer's payment method: %w", err)
	}
	if pm == "" {
		return nil, fmt.Errorf("%w: customer %s has no saved card, so invoice %s cannot be charged",
			ErrNoPaymentMethod, stripeCustomerID, invoice.InvoiceID)
	}

	// The key carries the card, so a customer who replaces a declined card is
	// retried with the new one instead of Stripe rejecting the changed request
	// for the rest of the key's 24 hours.
	return b.stripe.CreatePaymentIntent(ctx, invoice.Amount, invoice.Currency, stripeCustomerID, pm,
		"invoice:"+invoice.InvoiceID+":"+pm,
		map[string]string{
			"openfireblocks_invoice_id":      invoice.InvoiceID,
			"openfireblocks_customer_id":     invoice.CustomerID,
			"openfireblocks_subscription_id": invoice.SubscriptionID,
		})
}
