package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Collection decides, per invoice, whether money moved. Each of these
// tests is a way of getting that wrong that costs somebody real money --
// either the customer, who is charged twice, or us, who write off an
// invoice that was never paid.

type fakeStore struct {
	unpaid  []UnpaidInvoice
	charges []recordedCharge
	paid    []string

	unpaidErr error
	recordErr error
	markErr   error
}

type recordedCharge struct {
	invoiceID string
	intentID  string
	status    string
	detail    string
}

func (f *fakeStore) UnpaidInvoices(_ context.Context, limit int) ([]UnpaidInvoice, error) {
	if f.unpaidErr != nil {
		return nil, f.unpaidErr
	}
	if limit < len(f.unpaid) {
		return f.unpaid[:limit], nil
	}
	return f.unpaid, nil
}

func (f *fakeStore) RecordCharge(_ context.Context, invoiceID, _, paymentIntentID string,
	_ int, _, status, detail string) error {
	f.charges = append(f.charges, recordedCharge{invoiceID, paymentIntentID, status, detail})
	return f.recordErr
}

func (f *fakeStore) MarkInvoicePaid(_ context.Context, invoiceID, _ string, _ time.Time) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.paid = append(f.paid, invoiceID)
	return nil
}

func (f *fakeStore) chargeFor(invoiceID string) *recordedCharge {
	for i := range f.charges {
		if f.charges[i].invoiceID == invoiceID {
			return &f.charges[i]
		}
	}
	return nil
}

type fakeCharger struct {
	// Per invoice id.
	intents map[string]*PaymentIntent
	errs    map[string]error
	calls   []string
}

func (f *fakeCharger) ChargeInvoice(_ context.Context, invoice *Invoice, _ string) (*PaymentIntent, error) {
	f.calls = append(f.calls, invoice.InvoiceID)
	if err, ok := f.errs[invoice.InvoiceID]; ok {
		return nil, err
	}
	if intent, ok := f.intents[invoice.InvoiceID]; ok {
		return intent, nil
	}
	return &PaymentIntent{ID: "pi_" + invoice.InvoiceID, Status: "succeeded"}, nil
}

func unpaid(id, customer string, amount int, stripeID string) UnpaidInvoice {
	return UnpaidInvoice{
		Invoice: &Invoice{
			InvoiceID:  id,
			CustomerID: customer,
			Amount:     amount,
			Currency:   "usd",
			Status:     "unpaid",
		},
		StripeCustomerID: stripeID,
	}
}

// The happy path, and the two things that must both happen.
func TestASuccessfulChargeMarksTheInvoicePaidAndRecordsIt(t *testing.T) {
	store := &fakeStore{unpaid: []UnpaidInvoice{unpaid("inv-1", "cus-1", 7500, "cus_stripe_1")}}
	charger := &fakeCharger{}

	result, err := collectPayments(context.Background(), store, charger, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Collected) != 1 {
		t.Fatalf("collected %d, want 1 (failed: %+v, skipped: %+v)",
			len(result.Collected), result.Failed, result.Skipped)
	}
	if len(store.paid) != 1 || store.paid[0] != "inv-1" {
		t.Errorf("the invoice was charged but not marked paid: %v", store.paid)
	}
	if c := store.chargeFor("inv-1"); c == nil || c.status != "succeeded" {
		t.Errorf("no succeeded charge was recorded: %+v", store.charges)
	}
}

// The one that writes off revenue.
//
// A PaymentIntent that still requires action -- 3D Secure, a mandate --
// has not taken the money. Marking it paid stops every future run
// retrying it, and the amount is simply never collected. Nobody notices,
// because the invoice says paid.
func TestAPaymentThatHasNotSettledDoesNotMarkTheInvoicePaid(t *testing.T) {
	for _, status := range []string{"requires_action", "requires_payment_method", "processing", "canceled"} {
		t.Run(status, func(t *testing.T) {
			store := &fakeStore{unpaid: []UnpaidInvoice{unpaid("inv-1", "cus-1", 7500, "cus_stripe_1")}}
			charger := &fakeCharger{intents: map[string]*PaymentIntent{
				"inv-1": {ID: "pi_1", Status: status},
			}}

			if _, err := collectPayments(context.Background(), store, charger, time.Now(), 0); err != nil {
				t.Fatal(err)
			}

			if len(store.paid) != 0 {
				t.Fatalf("an invoice whose payment is %q was marked paid; that amount is now "+
					"never collected and nothing will retry it", status)
			}
		})
	}
}

// The largest customers have no Stripe id, and their invoices must be
// visible rather than quietly passed over.
func TestAnInvoiceWithNoProcessorIdentityIsReportedRatherThanDropped(t *testing.T) {
	store := &fakeStore{unpaid: []UnpaidInvoice{
		unpaid("inv-enterprise", "cus-bank", 40000000, ""),
	}}
	charger := &fakeCharger{}

	result, err := collectPayments(context.Background(), store, charger, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(charger.calls) != 0 {
		t.Error("a customer with no processor identity was sent to the processor anyway")
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("the invoice was not reported as skipped: %+v", result)
	}
	if result.Skipped[0].AmountCents != 40000000 {
		t.Errorf("the skipped line does not carry the amount, so nobody knows what to chase: %+v",
			result.Skipped[0])
	}
	if len(result.Failed) != 0 {
		t.Error("a manually-collected invoice was reported as a failure; it is a normal outcome")
	}
	// Still recorded, so "why was this never charged" has an answer.
	if c := store.chargeFor("inv-enterprise"); c == nil || c.status != "skipped" {
		t.Errorf("the skip left no trail: %+v", store.charges)
	}
}

// One decline must not stop the rest.
func TestOneFailureDoesNotStopTheOthers(t *testing.T) {
	store := &fakeStore{unpaid: []UnpaidInvoice{
		unpaid("inv-1", "cus-1", 1000, "cus_s1"),
		unpaid("inv-declined", "cus-2", 2000, "cus_s2"),
		unpaid("inv-3", "cus-3", 3000, "cus_s3"),
	}}
	charger := &fakeCharger{errs: map[string]error{
		"inv-declined": errors.New("card_declined"),
	}}

	result, err := collectPayments(context.Background(), store, charger, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Collected) != 2 {
		t.Fatalf("collected %d, want 2; a decline stopped the pass", len(result.Collected))
	}
	if len(result.Failed) != 1 || result.Failed[0].InvoiceID != "inv-declined" {
		t.Fatalf("the decline was not reported: %+v", result.Failed)
	}
	if c := store.chargeFor("inv-declined"); c == nil || c.status != "failed" {
		t.Errorf("the decline left no trail: %+v", store.charges)
	}
	if c := store.chargeFor("inv-declined"); c != nil && c.detail == "" {
		t.Error("the decline was recorded with no reason; the customer's bank will ask which code")
	}
}

// An unconfigured processor is a deployment state, not one fault per
// invoice, and reporting it per invoice buries everything else.
func TestAnUnconfiguredProcessorIsSkippedRatherThanFailedPerInvoice(t *testing.T) {
	store := &fakeStore{unpaid: []UnpaidInvoice{
		unpaid("inv-1", "cus-1", 1000, "cus_s1"),
		unpaid("inv-2", "cus-2", 2000, "cus_s2"),
	}}
	charger := &fakeCharger{errs: map[string]error{
		"inv-1": fmt.Errorf("%w: no payment processor is configured", ErrUnavailable),
		"inv-2": fmt.Errorf("%w: no payment processor is configured", ErrUnavailable),
	}}

	result, err := collectPayments(context.Background(), store, charger, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failed) != 0 {
		t.Errorf("an unconfigured processor was reported as %d invoice failures: %+v",
			len(result.Failed), result.Failed)
	}
	if len(result.Skipped) != 2 {
		t.Errorf("skipped %d, want 2: %+v", len(result.Skipped), result.Skipped)
	}
}

// The money moved and the database disagrees. Loud, not silent.
func TestAChargeThatSucceedsButCannotBeRecordedAsPaidIsReported(t *testing.T) {
	store := &fakeStore{
		unpaid:  []UnpaidInvoice{unpaid("inv-1", "cus-1", 7500, "cus_s1")},
		markErr: errors.New("connection reset"),
	}
	charger := &fakeCharger{}

	result, err := collectPayments(context.Background(), store, charger, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failed) != 1 {
		t.Fatalf("the money was taken and the invoice still says unpaid, and nothing reported it: %+v",
			result)
	}
	// Still counted as collected: it was. The failure is the bookkeeping,
	// and conflating the two would have an operator re-charging a
	// customer who has already paid.
	if len(result.Collected) != 1 {
		t.Errorf("a successful payment was not reported as collected: %+v", result.Collected)
	}
}

// A failure to write the audit row must not fail the collection -- the
// money has already moved, and returning an error would have a caller
// retry a charge that succeeded.
func TestAFailureToRecordTheTrailDoesNotFailTheCollection(t *testing.T) {
	store := &fakeStore{
		unpaid:    []UnpaidInvoice{unpaid("inv-1", "cus-1", 7500, "cus_s1")},
		recordErr: errors.New("disk full"),
	}
	charger := &fakeCharger{}

	result, err := collectPayments(context.Background(), store, charger, time.Now(), 0)
	if err != nil {
		t.Fatalf("a failed audit write failed the whole collection: %v", err)
	}
	if len(result.Collected) != 1 {
		t.Fatalf("the payment was lost: %+v", result)
	}
	if len(store.paid) != 1 {
		t.Error("the invoice was not marked paid")
	}
}

// The pass is bounded, so a backlog cannot make every run time out and
// restart from the beginning forever.
func TestThePassIsBounded(t *testing.T) {
	var many []UnpaidInvoice
	for i := 0; i < 500; i++ {
		many = append(many, unpaid(fmt.Sprintf("inv-%d", i), "cus-1", 100, "cus_s1"))
	}
	store := &fakeStore{unpaid: many}

	result, err := collectPayments(context.Background(), store, &fakeCharger{}, time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempted != 10 {
		t.Fatalf("attempted %d with a limit of 10", result.Attempted)
	}

	// And the default is applied rather than meaning "unlimited".
	result, err = collectPayments(context.Background(), store, &fakeCharger{}, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempted != defaultCollectionLimit {
		t.Fatalf("a limit of 0 attempted %d, want the default of %d",
			result.Attempted, defaultCollectionLimit)
	}
}
