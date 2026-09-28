package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

// Collecting the money, which is the half of billing that was missing.
//
// RunBilling raised invoices on a schedule and stopped. ChargeInvoice
// existed, worked, and took the Stripe customer id as an argument that
// nothing stored -- so payment could only be collected through an HTTP
// call where a human supplied the id by hand. Invoices accumulated;
// nothing turned them into revenue.
//
// # Why collection is a separate pass over the invoices
//
// Not inline in RunBilling's loop, and the reason is what happens when
// Stripe is slow. Invoicing is local work against the database and
// finishes in milliseconds; charging is a network call to a third party
// that can take seconds or hang. Interleaved, one unreachable processor
// stalls the sweep, and subscriptions after it in the list get neither
// an invoice nor a charge -- the failure of the payment processor becomes
// a failure to bill at all, which is the more expensive of the two.
//
// Separated, an invoice always gets raised. Collection is then free to
// fail, and the next run retries it against the invoice that already
// exists.
//
// # Idempotency
//
// ChargeInvoice derives its Stripe idempotency key from the invoice id,
// so a second attempt at the same invoice reuses the existing payment
// intent rather than charging twice. That is what makes it safe for this
// to run from a schedule that fires twice, from an operator's hand, and
// from a retry after a crash. It is also why this does not try to be
// clever about which invoices to skip: correctness comes from the
// idempotency key, not from this function's bookkeeping.

// CollectionResult is what one collection pass did.
type CollectionResult struct {
	RanAt     time.Time         `json:"ran_at"`
	Attempted int               `json:"attempted"`
	Collected []CollectedLine   `json:"collected"`
	Skipped   []SkippedLine     `json:"skipped"`
	Failed    []CollectedFailed `json:"failed"`
}

type CollectedLine struct {
	InvoiceID       string `json:"invoice_id"`
	CustomerID      string `json:"customer_id"`
	PaymentIntentID string `json:"payment_intent_id"`
	AmountCents     int    `json:"amount_cents"`
	Status          string `json:"status"`
}

// SkippedLine is an invoice nobody will chase unless this says so.
//
// Reported rather than silently passed over, because the largest
// customers are the ones with no Stripe id -- an enterprise on an annual
// licence is invoiced and pays by transfer. Those invoices are real
// revenue that a human has to collect, and a billing run that omitted
// them from its output would be hiding the most valuable rows in it.
type SkippedLine struct {
	InvoiceID   string `json:"invoice_id"`
	CustomerID  string `json:"customer_id"`
	AmountCents int    `json:"amount_cents"`
	Reason      string `json:"reason"`
}

type CollectedFailed struct {
	InvoiceID  string `json:"invoice_id"`
	CustomerID string `json:"customer_id"`
	Error      string `json:"error"`
}

// UnpaidInvoice is an invoice and the payment identity to charge it to.
type UnpaidInvoice struct {
	Invoice          *Invoice
	StripeCustomerID string // empty when this customer pays by transfer
}

// UnpaidInvoices lists what is outstanding.
//
// Cross-tenant by nature, like SubscriptionsDueForBilling, so it reads
// through the admin pool.
//
// Ordered oldest first. If a run is cut short -- a timeout, a restart,
// a rate limit -- the invoices that have been outstanding longest are the
// ones that got attempted, rather than an arbitrary slice.
func (p *PostgresDB) UnpaidInvoices(ctx context.Context, limit int) ([]UnpaidInvoice, error) {
	rows, err := p.admin.QueryContext(ctx, `
		SELECT i.invoice_id, i.subscription_id, i.customer_id, i.amount_cents, i.currency,
		       i.status, i.due_date, i.created_at,
		       COALESCE(c.stripe_customer_id, '')
		FROM invoices i
		JOIN customers c ON c.customer_id = i.customer_id
		WHERE i.status <> 'paid'
		ORDER BY i.created_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing unpaid invoices: %w", err)
	}
	defer rows.Close()

	var out []UnpaidInvoice
	for rows.Next() {
		var inv Invoice
		var stripeID string
		if err := rows.Scan(&inv.InvoiceID, &inv.SubscriptionID, &inv.CustomerID,
			&inv.Amount, &inv.Currency, &inv.Status, &inv.DueDate, &inv.CreatedAt,
			&stripeID); err != nil {
			return nil, fmt.Errorf("scanning an unpaid invoice: %w", err)
		}
		out = append(out, UnpaidInvoice{Invoice: &inv, StripeCustomerID: stripeID})
	}
	return out, rows.Err()
}

// RecordCharge writes what a collection attempt did.
//
// Every attempt, including the failures and the skips. A payments trail
// that records only successes cannot answer "why was this customer never
// charged", which is the question actually asked -- and it is asked
// months later, by someone reconciling, with no access to the logs from
// the day it happened.
func (p *PostgresDB) RecordCharge(ctx context.Context, invoiceID, customerID, paymentIntentID string,
	amountCents int, currency, status, detail string) error {
	var intent interface{}
	if paymentIntentID != "" {
		intent = paymentIntentID
	}
	_, err := p.admin.ExecContext(ctx, `
		INSERT INTO invoice_charges
		  (invoice_id, customer_id, payment_intent_id, amount_cents, currency, status, detail)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)`,
		invoiceID, customerID, intent, amountCents, currency, status, detail)
	if err != nil {
		return fmt.Errorf("recording the charge attempt for invoice %s: %w", invoiceID, err)
	}
	return nil
}

// SetStripeCustomerID attaches a payment identity to a customer.
func (p *PostgresDB) SetStripeCustomerID(ctx context.Context, customerID, stripeCustomerID string) error {
	res, err := p.admin.ExecContext(ctx,
		`UPDATE customers SET stripe_customer_id = NULLIF($2, '') WHERE customer_id = $1::uuid`,
		customerID, stripeCustomerID)
	if err != nil {
		return fmt.Errorf("setting the stripe customer id: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: no customer %s", ErrNotFound, customerID)
	}
	return nil
}

// defaultCollectionLimit bounds one pass.
//
// A limit rather than "all of them" because this runs behind an HTTP
// request from a schedule, and a backlog of thousands would exceed any
// sensible timeout and get retried from the start forever, never
// finishing. Oldest-first ordering means successive runs make progress.
const defaultCollectionLimit = 200

// collectionStore is the slice of the database this pass touches.
//
// An interface at exactly this seam, and only here, so the decisions
// below can be tested. Those decisions are the ones that lose money when
// they are wrong -- marking an invoice paid on a payment that has not
// settled writes off the amount; not marking one that has charges the
// customer again next month -- and until this existed they could only be
// exercised against a live Postgres and a live Stripe, which in practice
// meant not at all.
//
// Deliberately not a repository abstraction over the whole service. The
// rest of billing is fine talking to *PostgresDB directly; widening this
// would be indirection bought for nothing.
type collectionStore interface {
	UnpaidInvoices(ctx context.Context, limit int) ([]UnpaidInvoice, error)
	RecordCharge(ctx context.Context, invoiceID, customerID, paymentIntentID string,
		amountCents int, currency, status, detail string) error
	MarkInvoicePaid(ctx context.Context, invoiceID, customerID string, paidAt time.Time) error
}

// invoiceCharger is the payment side, for the same reason.
type invoiceCharger interface {
	ChargeInvoice(ctx context.Context, invoice *Invoice, stripeCustomerID string) (*PaymentIntent, error)
}

// CollectPayments charges every outstanding invoice it can.
//
// One invoice's failure never stops the rest, for the same reason
// RunBilling works that way: a pass that aborts on the first decline
// collects from the alphabetically-early customers and silently skips
// everyone after, and nobody notices a partial run.
func (b *BillingService) CollectPayments(ctx context.Context, now time.Time, limit int) (*CollectionResult, error) {
	return collectPayments(ctx, b.db, b, now, limit)
}

func collectPayments(ctx context.Context, store collectionStore, charger invoiceCharger,
	now time.Time, limit int) (*CollectionResult, error) {
	if limit <= 0 {
		limit = defaultCollectionLimit
	}

	outstanding, err := store.UnpaidInvoices(ctx, limit)
	if err != nil {
		return nil, err
	}

	result := &CollectionResult{
		RanAt:     now,
		Attempted: len(outstanding),
		Collected: []CollectedLine{},
		Skipped:   []SkippedLine{},
		Failed:    []CollectedFailed{},
	}

	for _, item := range outstanding {
		inv := item.Invoice

		if item.StripeCustomerID == "" {
			// Not a failure. This is how the annual-licence customers
			// work, and they are most of the revenue.
			const reason = "no payment processor identity on file; this invoice is collected manually"
			result.Skipped = append(result.Skipped, SkippedLine{
				InvoiceID:   inv.InvoiceID,
				CustomerID:  inv.CustomerID,
				AmountCents: inv.Amount,
				Reason:      reason,
			})
			recordChargeQuietly(ctx, store, inv, "", "skipped", reason)
			continue
		}

		intent, err := charger.ChargeInvoice(ctx, inv, item.StripeCustomerID)
		if err != nil {
			// A processor that is not configured is a deployment state,
			// not a per-invoice fault, and reporting it once per unpaid
			// invoice would bury everything else in the result.
			if errors.Is(err, ErrUnavailable) {
				result.Skipped = append(result.Skipped, SkippedLine{
					InvoiceID:   inv.InvoiceID,
					CustomerID:  inv.CustomerID,
					AmountCents: inv.Amount,
					Reason:      "no payment processor is configured",
				})
				continue
			}
			result.Failed = append(result.Failed, CollectedFailed{
				InvoiceID:  inv.InvoiceID,
				CustomerID: inv.CustomerID,
				Error:      err.Error(),
			})
			recordChargeQuietly(ctx, store, inv, "", "failed", err.Error())
			continue
		}

		status := intent.Status
		result.Collected = append(result.Collected, CollectedLine{
			InvoiceID:       inv.InvoiceID,
			CustomerID:      inv.CustomerID,
			PaymentIntentID: intent.ID,
			AmountCents:     inv.Amount,
			Status:          status,
		})

		// Marked paid only on a terminal success. A PaymentIntent that
		// still requires action -- 3D Secure, a mandate -- has not taken
		// the money, and marking it paid would stop the next run
		// retrying it and quietly write off the amount.
		if status == "succeeded" {
			recordChargeQuietly(ctx, store, inv, intent.ID, "succeeded", "")
			if err := store.MarkInvoicePaid(ctx, inv.InvoiceID, inv.CustomerID, now); err != nil {
				// The money is taken and the invoice still says unpaid.
				// Reported loudly: the idempotency key means the next run
				// will not double-charge, but a human should know the
				// database and the processor disagree.
				result.Failed = append(result.Failed, CollectedFailed{
					InvoiceID:  inv.InvoiceID,
					CustomerID: inv.CustomerID,
					Error: fmt.Sprintf(
						"payment %s succeeded but the invoice could not be marked paid: %v",
						intent.ID, err),
				})
			}
			continue
		}
		recordChargeQuietly(ctx, store, inv, intent.ID, "requires_action",
			fmt.Sprintf("payment intent is %s", status))
	}

	log.Printf("collection run: %d attempted, %d collected, %d skipped, %d failed",
		result.Attempted, len(result.Collected), len(result.Skipped), len(result.Failed))
	return result, nil
}

// recordChargeQuietly writes the audit row and logs if it cannot.
//
// A failure to record must not fail the collection: the money has
// already moved, and returning an error here would make a caller retry a
// charge that succeeded. The row is the trail, not the transaction.
func recordChargeQuietly(ctx context.Context, store collectionStore, inv *Invoice, intentID, status, detail string) {
	if err := store.RecordCharge(ctx, inv.InvoiceID, inv.CustomerID, intentID,
		inv.Amount, inv.Currency, status, detail); err != nil {
		log.Printf("could not record the %s charge attempt for invoice %s: %v",
			status, inv.InvoiceID, err)
	}
}

// HandleCollectPayments is the endpoint a schedule calls after the
// billing run.
//
// Separate from /v1/billing/run rather than chained onto it, so an
// operator can re-attempt collection without re-running invoicing, and
// so a payment processor outage cannot stop invoices being raised.
func (b *BillingService) HandleCollectPayments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := defaultCollectionLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	result, err := b.CollectPayments(r.Context(), time.Now(), limit)
	if err != nil {
		writeError(w, "collection run", err)
		return
	}

	// 207 when some invoices failed, matching HandleRunBilling: the run
	// did something and did not do everything. A skip is not a failure --
	// an invoice with no processor identity is collected by a human, which
	// is a normal outcome, not a degraded one.
	status := http.StatusOK
	if len(result.Failed) > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

// HandleSetStripeCustomer attaches a payment identity to a customer.
//
// Admin-only by placement: it is mounted alongside the other operator
// endpoints rather than on the customer-facing surface. A tenant able to
// set its own Stripe customer id could point its invoices at somebody
// else's payment method.
func (b *BillingService) HandleSetStripeCustomer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		CustomerID       string `json:"customer_id"`
		StripeCustomerID string `json:"stripe_customer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.CustomerID == "" {
		http.Error(w, "customer_id is required", http.StatusBadRequest)
		return
	}
	if err := b.db.SetStripeCustomerID(r.Context(), req.CustomerID, req.StripeCustomerID); err != nil {
		writeError(w, "setting the stripe customer id", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"customer_id":        req.CustomerID,
		"stripe_customer_id": req.StripeCustomerID,
	})
}
