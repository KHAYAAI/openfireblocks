package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Turning what the platform did into what the customer owes.
//
// This is the part of billing that did not exist. Subscribe worked,
// ChargeInvoice worked and was tested against Stripe's request format, and
// between them was nothing: RecordUsage took a map of counters from a
// caller, and no caller anywhere in the platform ever called it. No invoice
// was ever created from usage, so the only invoices that could exist were
// ones somebody wrote by hand.
//
// Usage is derived from the database rather than pushed into it. That is
// the important design decision here, and it is worth being explicit about
// why: a counter incremented at the point of work is lost on a crash,
// double-counted on a retry, and impossible to reconcile afterwards --
// three separate ways to bill a customer for the wrong number. Counting
// rows the platform already writes as its record of work is idempotent by
// construction, survives restarts, and can be re-derived and audited months
// later. It also means the invoice and the audit trail can never disagree,
// because they are the same rows.
//
// It became possible at all when signing_requests started being written.
// Before that the table was empty, so any usage query would have returned
// zero and every customer would have been billed the base rate forever.

// UsageCounts is what a subscription used during one billing period.
type UsageCounts struct {
	SigningRequests int
	KeyOperations   int
}

// CountUsage counts the work done for a customer between two instants.
//
// Completed requests only. A ceremony that failed did consume real
// resources, but charging for a signature the customer did not get is not
// a defensible line on an invoice, and a customer arguing it would be
// right.
//
// Half-open on the upper bound: a period ending at midnight on the 1st and
// the next beginning at the same instant must not both contain a request
// made at exactly midnight.
func (p *PostgresDB) CountUsage(ctx context.Context, customerID string, from, to time.Time) (UsageCounts, error) {
	var counts UsageCounts

	err := p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			  FROM signing_requests
			 WHERE customer_id = $1::uuid
			   AND status = 'completed'
			   AND created_at >= $2 AND created_at < $3`,
			customerID, from, to).Scan(&counts.SigningRequests); err != nil {
			return fmt.Errorf("counting signatures: %w", err)
		}

		// Keys created in the period, not keys held. A key is expensive
		// once -- the distributed key generation -- and free to keep, so
		// billing for creation matches what it costs to serve. Failed
		// provisioning is excluded for the same reason failed signatures
		// are.
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			  FROM key_pairs
			 WHERE customer_id = $1::uuid
			   AND status <> 'failed'
			   AND created_at >= $2 AND created_at < $3`,
			customerID, from, to).Scan(&counts.KeyOperations); err != nil {
			return fmt.Errorf("counting keys: %w", err)
		}
		return nil
	})
	return counts, err
}

// MeasureUsage records what a subscription has used this period.
func (b *BillingService) MeasureUsage(ctx context.Context, subscriptionID string) (*UsageMetrics, error) {
	subscription, err := b.db.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("subscription not found: %w", err)
	}
	plan, err := b.db.GetPlan(ctx, subscription.PlanID)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %w", err)
	}

	counts, err := b.db.CountUsage(ctx, subscription.CustomerID,
		subscription.CurrentPeriodStart, subscription.CurrentPeriodEnd)
	if err != nil {
		return nil, err
	}

	metrics := &UsageMetrics{
		MetricsID:         uuid.New().String(),
		SubscriptionID:    subscriptionID,
		CustomerID:        subscription.CustomerID,
		PeriodStart:       subscription.CurrentPeriodStart,
		PeriodEnd:         subscription.CurrentPeriodEnd,
		SigningRequests:   counts.SigningRequests,
		KeyOperations:     counts.KeyOperations,
		AvailableSignings: remaining(plan.SigningLimit, counts.SigningRequests),
		AvailableKeys:     remaining(plan.KeyLimit, counts.KeyOperations),
		CreatedAt:         time.Now(),
	}
	if err := b.db.CreateUsageMetrics(ctx, metrics); err != nil {
		return nil, fmt.Errorf("recording usage: %w", err)
	}
	return metrics, nil
}

// remaining never reports a negative allowance. A customer 40 signatures
// past their limit has none left, not minus forty -- and a negative number
// shown in a dashboard reads as a bug.
func remaining(limit, used int) int {
	if limit <= 0 {
		return 0
	}
	if used >= limit {
		return 0
	}
	return limit - used
}

// GenerateInvoice bills a subscription for its current period.
//
// Safe to run twice. It will be driven by a schedule, and a schedule that
// fires twice -- a retry, an overlapping run, an operator doing it by hand
// -- must not bill a customer for the same month twice. The unique index
// on (subscription_id, period_start) is what actually guarantees that;
// this returns the existing invoice rather than an error, so a retry is
// indistinguishable from a first attempt.
func (b *BillingService) GenerateInvoice(ctx context.Context, subscriptionID string) (*Invoice, error) {
	subscription, err := b.db.GetSubscription(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("subscription not found: %w", err)
	}
	plan, err := b.db.GetPlan(ctx, subscription.PlanID)
	if err != nil {
		return nil, fmt.Errorf("plan not found: %w", err)
	}

	if existing, err := b.db.GetInvoiceForPeriod(ctx, subscriptionID,
		subscription.CurrentPeriodStart); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	counts, err := b.db.CountUsage(ctx, subscription.CustomerID,
		subscription.CurrentPeriodStart, subscription.CurrentPeriodEnd)
	if err != nil {
		return nil, err
	}

	items := InvoiceLineItems(plan, counts)
	total := TotalOf(items)

	invoice := &Invoice{
		InvoiceID:      uuid.New().String(),
		SubscriptionID: subscriptionID,
		CustomerID:     subscription.CustomerID,
		Amount:         total,
		Currency:       plan.Currency,
		Status:         "unpaid",
		// Net 14. Long enough that a finance team can process it, short
		// enough that an unpaid invoice is a signal rather than noise.
		DueDate:     time.Now().AddDate(0, 0, 14),
		LineItems:   items,
		PeriodStart: &subscription.CurrentPeriodStart,
		PeriodEnd:   &subscription.CurrentPeriodEnd,
		CreatedAt:   time.Now(),
	}

	if err := b.db.CreateInvoice(ctx, invoice); err != nil {
		// Another run got there first, between the check above and this
		// insert. Its invoice is as good as the one just built, so return
		// that rather than failing a retry that did nothing wrong.
		if isUniqueViolation(err) {
			return b.db.GetInvoiceForPeriod(ctx, subscriptionID, subscription.CurrentPeriodStart)
		}
		return nil, fmt.Errorf("creating the invoice: %w", err)
	}
	return invoice, nil
}

// InvoiceLineItems turns a plan and a period's usage into what the
// customer is charged for.
//
// Free of I/O so the arithmetic can be tested exhaustively: this is the
// function that decides how much money to ask somebody for, and it is the
// one place in the platform where an off-by-one is a billing dispute
// rather than a bug report.
func InvoiceLineItems(plan *Plan, counts UsageCounts) []LineItem {
	items := []LineItem{{
		Description: fmt.Sprintf("%s (%s)", plan.Name, plan.BillingCycle),
		Quantity:    1,
		UnitPrice:   plan.Price,
		Amount:      plan.Price,
	}}

	// Overage is itemised rather than folded into a total. An invoice a
	// customer cannot check is an invoice they will dispute, and "you went
	// 412 signatures over at 2 cents each" is a sentence they can verify
	// against their own records.
	//
	// A zero rate means no overage charge rather than free usage that
	// still gets a line: plans sold before overage existed agreed to no
	// such charge, and an invoice line of zero invites the question of why
	// it is there.
	if over := counts.SigningRequests - plan.SigningLimit; over > 0 && plan.OverageSigningCents > 0 {
		items = append(items, LineItem{
			Description: fmt.Sprintf("Signatures beyond the %d included", plan.SigningLimit),
			Quantity:    over,
			UnitPrice:   plan.OverageSigningCents,
			Amount:      over * plan.OverageSigningCents,
		})
	}
	if over := counts.KeyOperations - plan.KeyLimit; over > 0 && plan.OverageKeyCents > 0 {
		items = append(items, LineItem{
			Description: fmt.Sprintf("Keys beyond the %d included", plan.KeyLimit),
			Quantity:    over,
			UnitPrice:   plan.OverageKeyCents,
			Amount:      over * plan.OverageKeyCents,
		})
	}
	return items
}

// TotalOf sums an invoice's line items.
func TotalOf(items []LineItem) int {
	total := 0
	for _, item := range items {
		total += item.Amount
	}
	return total
}

// HandleMeasureUsage recomputes and records a subscription's usage.
func (b *BillingService) HandleMeasureUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	subscriptionID := r.URL.Query().Get("subscription_id")
	if err := requireUUID("subscription_id", subscriptionID); err != nil {
		writeError(w, "measure usage", err)
		return
	}

	metrics, err := b.MeasureUsage(r.Context(), subscriptionID)
	if err != nil {
		writeError(w, "measure usage", err)
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}

// HandleGenerateInvoice raises the invoice for a subscription's period.
func (b *BillingService) HandleGenerateInvoice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	subscriptionID := r.URL.Query().Get("subscription_id")
	if err := requireUUID("subscription_id", subscriptionID); err != nil {
		writeError(w, "generate invoice", err)
		return
	}

	invoice, err := b.GenerateInvoice(r.Context(), subscriptionID)
	if err != nil {
		writeError(w, "generate invoice", err)
		return
	}
	writeJSON(w, http.StatusOK, invoice)
}

// HandleListInvoices returns a customer's invoices.
func (b *BillingService) HandleListInvoices(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if err := requireUUID("customer_id", customerID); err != nil {
		writeError(w, "list invoices", err)
		return
	}

	invoices, err := b.GetInvoices(r.Context(), customerID)
	if err != nil {
		writeError(w, "list invoices", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"invoices": invoices})
}

// HandleChargeInvoice collects payment for one invoice.
//
// Separate from generation on purpose. Raising an invoice is a bookkeeping
// act and is safe to repeat; taking somebody's money is neither, and the
// two should not be able to happen by accident together.
func (b *BillingService) HandleChargeInvoice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		InvoiceID        string `json:"invoice_id"`
		CustomerID       string `json:"customer_id"`
		StripeCustomerID string `json:"stripe_customer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "charge invoice", fmt.Errorf("%w: %v", ErrInvalidInput, err))
		return
	}
	if err := requireUUID("invoice_id", req.InvoiceID); err != nil {
		writeError(w, "charge invoice", err)
		return
	}
	if err := requireUUID("customer_id", req.CustomerID); err != nil {
		writeError(w, "charge invoice", err)
		return
	}

	ctx := r.Context()
	invoices, err := b.GetInvoices(ctx, req.CustomerID)
	if err != nil {
		writeError(w, "charge invoice", err)
		return
	}
	var invoice *Invoice
	for _, candidate := range invoices {
		if candidate.InvoiceID == req.InvoiceID {
			invoice = candidate
			break
		}
	}
	if invoice == nil {
		writeError(w, "charge invoice", fmt.Errorf("invoice %s: %w", req.InvoiceID, ErrNotFound))
		return
	}

	intent, err := b.ChargeInvoice(ctx, invoice, req.StripeCustomerID)
	if err != nil {
		writeError(w, "charge invoice", err)
		return
	}

	// Marked paid only once Stripe says the money moved. An intent that
	// needs further action -- 3D Secure, a bank redirect -- has not
	// collected anything yet, and recording it as paid would lose the
	// difference between "we asked" and "they paid".
	if intent.Status == "succeeded" {
		now := time.Now()
		if err := b.db.MarkInvoicePaid(ctx, invoice.InvoiceID, invoice.CustomerID, now); err != nil {
			// The money moved. Failing the request would invite a retry
			// that charges nothing (the idempotency key sees to that) but
			// still reports failure, so this is logged and surfaced in the
			// response instead.
			log.Printf("charged invoice %s but could not mark it paid: %v", invoice.InvoiceID, err)
		} else {
			invoice.Status = "paid"
			invoice.PaidAt = &now
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"invoice":        invoice,
		"payment_intent": intent,
	})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
