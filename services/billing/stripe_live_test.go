package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Talking to Stripe for real.
//
// stripe_test.go proves the platform sends the request Stripe documents:
// the right path, the right form encoding, the idempotency key in the right
// header. What it cannot prove is that Stripe agrees. A stub says yes to
// whatever it is given, so a wrong field name, a deprecated parameter, an
// API version that moved, or an idempotency key that does not behave the
// way the docs describe would all pass in CI and fail the first time real
// money was involved.
//
// So these run against Stripe's actual test API. They need a test-mode
// secret key in STRIPE_TEST_API_KEY and skip without one -- and the skip
// says plainly what has therefore not been verified, because a silently
// skipped test is indistinguishable from a passing one on a CI dashboard.
//
//	STRIPE_TEST_API_KEY=sk_test_... go test ./services/billing -run Live -v
//
// A test key only. The guard below refuses anything that is not sk_test_:
// this creates customers and charges cards, and pointed at a live key it
// would do both for real.

func liveClient(t *testing.T) *StripeClient {
	t.Helper()

	key := os.Getenv("STRIPE_TEST_API_KEY")
	if key == "" {
		t.Skip("STRIPE_TEST_API_KEY is not set: whether this platform can " +
			"actually collect money is UNVERIFIED. Set a Stripe test-mode " +
			"secret key to check it.")
	}
	// Not a warning, a refusal. The difference between sk_test_ and sk_live_
	// is the difference between a test suite and a suite that charges
	// people, and it is one character of typing.
	if !strings.HasPrefix(key, "sk_test_") {
		t.Fatal("STRIPE_TEST_API_KEY is not a test-mode key (expected sk_test_ prefix); " +
			"refusing to create customers and charge cards against a live account")
	}

	// The real Stripe, explicitly: if something in the environment has
	// pointed STRIPE_API_BASE at the stub used by the other tests, these
	// would silently prove nothing.
	t.Setenv("STRIPE_API_BASE", "")
	return NewStripeClient(key)
}

func TestLiveStripeAcceptsOurCustomerCreation(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tenant := "drill-" + time.Now().UTC().Format("20060102150405")
	customer, err := client.CreateCustomer(ctx, tenant, tenant+"@example.com", "Live drill "+tenant)
	if err != nil {
		t.Fatalf("Stripe refused the customer this platform builds: %v", err)
	}
	if customer.ID == "" {
		t.Fatal("Stripe accepted the customer and returned no id")
	}

	// The local tenant id has to survive into Stripe, or a payment that
	// arrives cannot be attributed to anybody. This is the field a finance
	// team uses to answer "who is this money from".
	if customer.Metadata["openfireblocks_customer_id"] != tenant {
		t.Errorf("Stripe stored metadata %v; the local tenant id is not recoverable from the charge",
			customer.Metadata)
	}
}

func TestLiveStripeAcceptsOurPaymentIntent(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	intent, err := client.CreatePaymentIntent(ctx, 51_400, "usd", "",
		"live-drill-"+time.Now().UTC().Format("20060102150405.000000"),
		map[string]string{"openfireblocks_invoice_id": "drill"})
	if err != nil {
		t.Fatalf("Stripe refused the payment intent this platform builds: %v", err)
	}
	if intent.ID == "" {
		t.Fatal("Stripe accepted the intent and returned no id")
	}
	// 51400 cents is the amount billing-drill.sh produces. Checking it
	// round-trips exactly guards against the units mistake that matters:
	// sending dollars where Stripe expects cents undercharges by 100x.
	if intent.Amount != 51_400 {
		t.Errorf("asked to charge 51400 cents and Stripe recorded %d", intent.Amount)
	}
	if !strings.EqualFold(intent.Currency, "usd") {
		t.Errorf("currency came back as %q", intent.Currency)
	}
}

// The property the whole design rests on.
//
// ChargeInvoice derives its idempotency key from the invoice id precisely
// so that a retry -- from the billing CronJob, an operator, a redelivered
// webhook -- cannot charge a customer twice. That is a claim about Stripe's
// behaviour, not ours, and until now nothing had ever checked it.
func TestLiveStripeIdempotencyKeyPreventsASecondCharge(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	key := "live-idempotency-" + time.Now().UTC().Format("20060102150405.000000")

	first, err := client.CreatePaymentIntent(ctx, 12_345, "usd", "", key, nil)
	if err != nil {
		t.Fatalf("the first charge failed: %v", err)
	}

	second, err := client.CreatePaymentIntent(ctx, 12_345, "usd", "", key, nil)
	if err != nil {
		t.Fatalf("the retry failed: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("the same idempotency key produced two payment intents, %s and %s: "+
			"a retried billing run would charge the customer twice", first.ID, second.ID)
	}
}

// Reusing a key for a different amount must not quietly succeed. If it did,
// a bug that changed an invoice total between attempts would charge the old
// amount while the platform recorded the new one.
func TestLiveStripeRefusesAReusedKeyForADifferentAmount(t *testing.T) {
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	key := "live-conflict-" + time.Now().UTC().Format("20060102150405.000000")

	if _, err := client.CreatePaymentIntent(ctx, 1_000, "usd", "", key, nil); err != nil {
		t.Fatalf("the first charge failed: %v", err)
	}

	_, err := client.CreatePaymentIntent(ctx, 9_999, "usd", "", key, nil)
	if err == nil {
		t.Fatal("Stripe accepted the same idempotency key for a different amount; " +
			"an invoice whose total changed between attempts would charge the wrong one")
	}
}

// An invoice that is already paid must not be chargeable again, and the
// refusal has to be a client error rather than an outage -- an operator
// seeing 503 goes looking for a broken dependency.
func TestLiveAnAlreadyPaidInvoiceIsRefusedWithoutCallingStripe(t *testing.T) {
	client := liveClient(t)
	svc := &BillingService{stripe: client}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	paid := &Invoice{InvoiceID: "inv-live", Amount: 100, Currency: "usd", Status: "paid"}

	_, err := svc.ChargeInvoice(ctx, paid, "cus_nonexistent")
	if err == nil {
		t.Fatal("a paid invoice was charged a second time")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("charging a paid invoice reported %v, which is not a client error", err)
	}
}
