package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// What a charge has to look like to take money. The bug these pin: the intent
// was created with no payment method and no confirm, so Stripe left it unpaid
// forever, and no invoice could ever have been collected.

func unpaidInvoice() *Invoice {
	return &Invoice{InvoiceID: "inv-1", SubscriptionID: "sub-1", CustomerID: "cust-1", Amount: 4200, Currency: "usd", Status: "unpaid"}
}

func TestAChargeConfirmsAgainstTheSavedCardInTheSameCall(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()
	svc := &BillingService{stripe: client}

	pi, err := svc.ChargeInvoice(context.Background(), unpaidInvoice(), "cus_test")
	if err != nil || pi.Status != "succeeded" {
		t.Fatalf("got %+v %v", pi, err)
	}
	posts := stub.postedIntents()
	if len(posts) != 1 {
		t.Fatalf("%d charge requests, want 1", len(posts))
	}
	form := stub.forms[posts[0]]
	for k, want := range map[string]string{
		"payment_method": "pm_saved_1", "confirm": "true", "off_session": "true",
		"error_on_requires_action": "true", "customer": "cus_test", "amount": "4200",
	} {
		if got := form.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestTheCustomersDefaultCardBeatsTheFirstSavedOne(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()
	inner := stub.respond
	stub.respond = func(path string) (int, string) {
		if strings.HasPrefix(path, "/v1/customers/") {
			return 200, `{"id":"cus_test","invoice_settings":{"default_payment_method":"pm_default"}}`
		}
		return inner(path)
	}
	svc := &BillingService{stripe: client}
	if _, err := svc.ChargeInvoice(context.Background(), unpaidInvoice(), "cus_test"); err != nil {
		t.Fatal(err)
	}
	if got := stub.forms[stub.postedIntents()[0]].Get("payment_method"); got != "pm_default" {
		t.Fatalf("charged %q, want the customer's default card", got)
	}
}

func TestACustomerWithNoSavedCardIsNeverCharged(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()
	inner := stub.respond
	stub.respond = func(path string) (int, string) {
		if strings.HasSuffix(path, "/payment_methods") {
			return 200, `{"data":[]}`
		}
		return inner(path)
	}
	svc := &BillingService{stripe: client}
	_, err := svc.ChargeInvoice(context.Background(), unpaidInvoice(), "cus_test")
	if !errors.Is(err, ErrNoPaymentMethod) {
		t.Fatalf("got %v, want ErrNoPaymentMethod", err)
	}
	if n := len(stub.postedIntents()); n != 0 {
		t.Fatalf("%d charge attempts were made against a customer with no card", n)
	}
}

// If the invoice was paid but marking it paid failed, and the customer then
// changed card, the retry is a different request. Looking for the payment
// first is what stops it being charged again.
func TestAnInvoiceAlreadyPaidAtStripeIsReturnedNotChargedAgain(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()
	inner := stub.respond
	stub.respond = func(path string) (int, string) {
		if strings.HasSuffix(path, "/payment_intents/search") {
			return 200, `{"data":[{"id":"pi_already","amount":4200,"currency":"usd","status":"succeeded"}]}`
		}
		return inner(path)
	}
	svc := &BillingService{stripe: client}
	pi, err := svc.ChargeInvoice(context.Background(), unpaidInvoice(), "cus_test")
	if err != nil || pi.ID != "pi_already" {
		t.Fatalf("got %+v %v", pi, err)
	}
	if n := len(stub.postedIntents()); n != 0 {
		t.Fatalf("a second charge was attempted for an invoice that was already paid")
	}
}

func TestWhenItCannotTellWhetherTheInvoiceWasPaidItDoesNotCharge(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()
	inner := stub.respond
	stub.respond = func(path string) (int, string) {
		if strings.HasSuffix(path, "/payment_intents/search") {
			return 500, `{"error":{"message":"search is down"}}`
		}
		return inner(path)
	}
	svc := &BillingService{stripe: client}
	if _, err := svc.ChargeInvoice(context.Background(), unpaidInvoice(), "cus_test"); err == nil {
		t.Fatal("charged without being able to check for a prior payment")
	}
	if n := len(stub.postedIntents()); n != 0 {
		t.Fatalf("%d charge attempts after the check failed", n)
	}
}

func TestAReplacedCardGetsANewIdempotencyKey(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()
	svc := &BillingService{stripe: client}
	_, _ = svc.ChargeInvoice(context.Background(), unpaidInvoice(), "cus_test")
	inner := stub.respond
	stub.respond = func(path string) (int, string) {
		if strings.HasSuffix(path, "/payment_methods") {
			return 200, `{"data":[{"id":"pm_new"}]}`
		}
		return inner(path)
	}
	_, _ = svc.ChargeInvoice(context.Background(), unpaidInvoice(), "cus_test")
	posts := stub.postedIntents()
	if len(posts) != 2 {
		t.Fatalf("%d charges", len(posts))
	}
	k1, k2 := stub.headers[posts[0]].Get("Idempotency-Key"), stub.headers[posts[1]].Get("Idempotency-Key")
	if k1 == k2 {
		t.Fatalf("both used %q: Stripe would reject the changed request for 24 hours", k1)
	}
}

func TestACollectorSkipsCustomersWithNoCardWithAReason(t *testing.T) {
	store := &fakeStore{unpaid: []UnpaidInvoice{{Invoice: unpaidInvoice(), StripeCustomerID: "cus_x"}}}
	charger := &fakeCharger{errs: map[string]error{"inv-1": ErrNoPaymentMethod}}
	res, err := collectPayments(context.Background(), store, charger, time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "no saved card") {
		t.Fatalf("got failed=%v skipped=%v", res.Failed, res.Skipped)
	}
	if c := store.chargeFor("inv-1"); c == nil || c.status != "skipped" {
		t.Fatalf("the audit row = %+v", c)
	}
}

func TestACardSessionRequestIsWhatStripeExpects(t *testing.T) {
	stub, client, done := newStubStripe(t)
	defer done()
	s, err := client.CreateSetupSession(context.Background(), "cus_test", "https://app.example/ok", "https://app.example/no", "k1")
	if err != nil || !strings.HasPrefix(s.URL, "https://checkout.stripe.com/") {
		t.Fatalf("got %+v %v", s, err)
	}
	f := stub.forms[0]
	if f.Get("mode") != "setup" || f.Get("customer") != "cus_test" || f.Get("payment_method_types[]") != "card" ||
		f.Get("success_url") != "https://app.example/ok" {
		t.Fatalf("form = %v", f)
	}
	if _, err := client.CreateSetupSession(context.Background(), "", "a", "b", "k"); err == nil {
		t.Fatal("a session with no customer was created")
	}
}

// The return URLs are where Stripe sends a customer after they enter a card.
func TestOnlyAllowlistedHttpsHostsMayBeReturnedTo(t *testing.T) {
	const hosts = "app.example, console.example:8443"
	for url, want := range map[string]bool{
		"https://app.example/billing/done":   true,
		"https://APP.example/x":              true,
		"https://console.example:8443/x":     true,
		"http://app.example/x":               false, // not https
		"https://evil.example/x":             false,
		"https://app.example.evil.example/x": false,
		"https://user@app.example/x":         false, // credentials in the URL
		"//app.example/x":                    false,
		"javascript:alert(1)":                false,
		"":                                   false,
	} {
		if got := returnURLAllowed(url, hosts); got != want {
			t.Errorf("%q: got %v want %v", url, got, want)
		}
	}
	if returnURLAllowed("https://app.example/x", "") {
		t.Error("with no hosts configured every URL must be refused")
	}
}

func TestCardSessionEndpointRefusesAForeignReturnURL(t *testing.T) {
	t.Setenv("BILLING_RETURN_HOSTS", "app.example")
	svc := &BillingService{stripe: &StripeClient{apiKey: "sk_test_x"}}
	rec := httptest.NewRecorder()
	body := `{"customer_id":"7b3c9f3e-5c1a-4e6b-9d3a-1a2b3c4d5e6f","success_url":"https://evil.example/x","cancel_url":"https://app.example/y"}`
	svc.HandleCreateCardSession(rec, httptest.NewRequest(http.MethodPost, "/v1/billing/card-session", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func authed(token string, allow bool, header, path string) int {
	h := requireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }), token, allow)
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestBillingRoutesNeedTheToken(t *testing.T) {
	if got := authed("s3cret", false, "Bearer s3cret", "/v1/invoices/charge"); got != http.StatusOK {
		t.Errorf("right token: %d", got)
	}
	for _, h := range []string{"", "Bearer nope", "s3cret", "Bearer s3cre"} {
		if got := authed("s3cret", false, h, "/v1/billing/stripe-customer"); got != http.StatusUnauthorized {
			t.Errorf("header %q: %d, want 401", h, got)
		}
	}
	if got := authed("", false, "", "/v1/invoices/charge"); got != http.StatusServiceUnavailable {
		t.Errorf("no token configured: %d, want 503", got)
	}
	if got := authed("", true, "", "/v1/invoices/charge"); got != http.StatusOK {
		t.Errorf("explicit dev opt-out: %d", got)
	}
	if got := authed("s3cret", false, "", "/health"); got != http.StatusOK {
		t.Errorf("/health: %d", got)
	}
}
