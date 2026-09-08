package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// A stand-in for api.stripe.com that records exactly what it was sent.
//
// The point is not to simulate Stripe -- it is to pin the *request*, because
// that is where billing bugs live: an amount in the wrong units, a missing
// idempotency key, an unpinned API version. None of those are visible from
// the response, and all of them cost real money.
type stubStripe struct {
	mu       sync.Mutex
	paths    []string
	forms    []url.Values
	headers  []http.Header
	respond  func(path string) (int, string)
	requests int
}

func newStubStripe(t *testing.T) (*stubStripe, *StripeClient, func()) {
	t.Helper()
	stub := &stubStripe{
		respond: func(path string) (int, string) {
			switch {
			case strings.HasSuffix(path, "/payment_intents"):
				return 200, `{"id":"pi_test_123","amount":2500,"currency":"usd","status":"requires_payment_method","client_secret":"pi_test_123_secret","customer":"cus_test"}`
			case strings.HasSuffix(path, "/customers"):
				return 200, `{"id":"cus_test","email":"a@b.io"}`
			}
			return 404, `{"error":{"message":"no such endpoint"}}`
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		stub.mu.Lock()
		stub.paths = append(stub.paths, r.URL.Path)
		stub.forms = append(stub.forms, r.PostForm)
		stub.headers = append(stub.headers, r.Header.Clone())
		stub.requests++
		respond := stub.respond
		stub.mu.Unlock()

		code, body := respond(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))

	client := &StripeClient{apiKey: "sk_test_key", baseURL: srv.URL, client: srv.Client()}
	return stub, client, srv.Close
}

func TestCreatePaymentIntentSendsTheRequestStripeExpects(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()

	pi, err := client.CreatePaymentIntent(context.Background(), 2500, "USD", "cus_test",
		"invoice:inv-1", map[string]string{"openfireblocks_invoice_id": "inv-1"})
	if err != nil {
		t.Fatalf("CreatePaymentIntent: %v", err)
	}
	if pi.ID != "pi_test_123" {
		t.Errorf("id = %q, want the id Stripe returned, not a locally generated one", pi.ID)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	form, hdr := stub.forms[0], stub.headers[0]

	// Amount must be an integer count of cents. A float here is how a $25.00
	// invoice becomes a 25-cent charge.
	if got := form.Get("amount"); got != "2500" {
		t.Errorf("amount = %q, want \"2500\" (integer cents)", got)
	}
	// Stripe rejects an uppercase currency.
	if got := form.Get("currency"); got != "usd" {
		t.Errorf("currency = %q, want lowercase \"usd\"", got)
	}
	if got := form.Get("customer"); got != "cus_test" {
		t.Errorf("customer = %q", got)
	}
	if got := form.Get("metadata[openfireblocks_invoice_id]"); got != "inv-1" {
		t.Errorf("invoice metadata = %q; without it a Stripe payment cannot be reconciled to an invoice", got)
	}
	if got := hdr.Get("Idempotency-Key"); got != "invoice:inv-1" {
		t.Errorf("Idempotency-Key = %q, want \"invoice:inv-1\"", got)
	}
	if got := hdr.Get("Stripe-Version"); got != stripeAPIVersion {
		t.Errorf("Stripe-Version = %q; unpinned, a dashboard change could alter response shapes without a deploy", got)
	}
	if got := hdr.Get("Authorization"); got != "Bearer sk_test_key" {
		t.Errorf("Authorization = %q", got)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q; Stripe does not accept JSON bodies", ct)
	}
}

// The property that matters most in billing: charging the same invoice twice
// must reuse one idempotency key, so Stripe collapses it rather than taking
// the customer's money again.
func TestChargingAnInvoiceTwiceReusesTheIdempotencyKey(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()

	svc := &BillingService{stripe: client}
	invoice := &Invoice{
		InvoiceID:      "inv-42",
		SubscriptionID: "sub-7",
		CustomerID:     "cust-9",
		Amount:         12345,
		Currency:       "usd",
		Status:         "unpaid",
	}

	for i := 0; i < 2; i++ {
		if _, err := svc.ChargeInvoice(context.Background(), invoice, "cus_test"); err != nil {
			t.Fatalf("charge %d: %v", i+1, err)
		}
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.requests != 2 {
		t.Fatalf("stub saw %d requests, want 2", stub.requests)
	}
	if stub.headers[0].Get("Idempotency-Key") != stub.headers[1].Get("Idempotency-Key") {
		t.Fatalf("two charges for the same invoice used different idempotency keys (%q, %q) -- Stripe would charge twice",
			stub.headers[0].Get("Idempotency-Key"), stub.headers[1].Get("Idempotency-Key"))
	}
	if got := stub.headers[0].Get("Idempotency-Key"); got != "invoice:inv-42" {
		t.Errorf("idempotency key = %q, want it derived from the invoice id", got)
	}
}

// A write without an idempotency key is refused outright rather than sent.
// Stripe would accept it; the customer would be charged twice on any retry.
func TestPostWithoutIdempotencyKeyIsRefused(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()

	err := client.post(context.Background(), "/v1/payment_intents", url.Values{}, "", nil)
	if err == nil {
		t.Fatal("a write with no idempotency key was allowed")
	}
	if !strings.Contains(err.Error(), "idempotency") {
		t.Errorf("error does not explain the problem: %v", err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.requests != 0 {
		t.Error("the request was sent to Stripe anyway")
	}
}

func TestStripeErrorsAreReportedWithStripesOwnMessage(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()

	stub.mu.Lock()
	stub.respond = func(string) (int, string) {
		return 402, `{"error":{"type":"card_error","code":"card_declined","message":"Your card was declined.","param":"payment_method"}}`
	}
	stub.mu.Unlock()

	_, err := client.CreatePaymentIntent(context.Background(), 100, "usd", "cus_test", "k", nil)
	if err == nil {
		t.Fatal("a 402 was treated as success")
	}
	// An operator reading a log needs Stripe's reason, not "request failed".
	for _, want := range []string{"Your card was declined", "card_declined", "card_error"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestRefusesNonsenseAmounts(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()

	for _, amount := range []int{0, -1, -2500} {
		if _, err := client.CreatePaymentIntent(context.Background(), amount, "usd", "cus_test", "k", nil); err == nil {
			t.Errorf("accepted an amount of %d cents", amount)
		}
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.requests != 0 {
		t.Error("a nonsense amount was sent to Stripe")
	}
}

// Self-hosted operators running this for their own institution have nobody
// to bill. An unconfigured client is a valid state and must fail clearly
// rather than pretending, or silently doing nothing.
func TestUnconfiguredClientFailsClearlyRatherThanFabricating(t *testing.T) {
	client := NewStripeClient("")
	if client.Configured() {
		t.Fatal("a client with no API key reports itself configured")
	}

	svc := &BillingService{stripe: client}
	invoice := &Invoice{InvoiceID: "inv-1", Amount: 100, Currency: "usd", Status: "unpaid"}
	_, err := svc.ChargeInvoice(context.Background(), invoice, "cus_x")
	if err == nil {
		t.Fatal("charging with no Stripe key appeared to succeed")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error does not say Stripe is unconfigured: %v", err)
	}
}

func TestAlreadyPaidInvoiceIsNotChargedAgain(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()

	svc := &BillingService{stripe: client}
	paid := &Invoice{InvoiceID: "inv-8", Amount: 500, Currency: "usd", Status: "paid"}

	if _, err := svc.ChargeInvoice(context.Background(), paid, "cus_test"); err == nil {
		t.Fatal("a paid invoice was charged again")
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.requests != 0 {
		t.Error("a paid invoice reached Stripe")
	}
}

func TestCreateCustomerTiesTheStripeObjectToTheLocalTenant(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()

	if _, err := client.CreateCustomer(context.Background(), "cust-9", "a@b.io", "Acme"); err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	form := stub.forms[0]
	if got := form.Get("metadata[openfireblocks_customer_id]"); got != "cust-9" {
		t.Errorf("customer metadata = %q; without it reconciliation depends on matching by email", got)
	}
	if got := stub.headers[0].Get("Idempotency-Key"); got != "customer:cust-9" {
		t.Errorf("idempotency key = %q; a retried provisioning workflow would create a duplicate billing relationship", got)
	}
}
