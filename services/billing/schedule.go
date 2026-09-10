package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Actually sending the bills.
//
// GenerateInvoice has been reachable and correct for a while, and nothing
// called it on a schedule. A customer's period would end, and no invoice
// existed until somebody remembered to ask for one -- which for a business
// whose revenue is the invoices is the difference between a billing system
// and a billing library.
//
// The sweep is deliberately a single endpoint rather than logic inside a
// cron container. A CronJob that holds the business rule is a second place
// the rule lives, testable only by waiting for a schedule; a CronJob that
// makes one HTTP call is a trigger, and the rule stays here where the rest
// of billing is.

// BillingRunResult is what one sweep did, in enough detail to reconcile.
//
// Reported per subscription rather than as a total, because "9 invoices
// raised" is not something a finance team can check and "these 9
// subscriptions, these 9 amounts" is.
type BillingRunResult struct {
	RanAt      time.Time        `json:"ran_at"`
	Considered int              `json:"considered"`
	Invoiced   []BilledLine     `json:"invoiced"`
	Failed     []BillingFailure `json:"failed"`
}

type BilledLine struct {
	SubscriptionID string    `json:"subscription_id"`
	CustomerID     string    `json:"customer_id"`
	InvoiceID      string    `json:"invoice_id"`
	AmountCents    int       `json:"amount_cents"`
	PeriodStart    time.Time `json:"period_start"`
	PeriodEnd      time.Time `json:"period_end"`
}

type BillingFailure struct {
	SubscriptionID string `json:"subscription_id"`
	Error          string `json:"error"`
}

// SubscriptionsDueForBilling finds subscriptions whose period has ended.
//
// Cross-tenant by nature -- a billing run is not one customer's request --
// so it reads through the admin pool, and every subsequent per-subscription
// operation goes back through the tenant-scoped path.
//
// Only active subscriptions. A canceled one has nothing further to bill,
// and a paused one is paused precisely so that it does not accrue charges.
func (p *PostgresDB) SubscriptionsDueForBilling(ctx context.Context, now time.Time) ([]*Subscription, error) {
	rows, err := p.admin.QueryContext(ctx, `
		SELECT subscription_id, customer_id, plan_id, status,
		       current_period_start, current_period_end, canceled_at,
		       trial_ends_at, auto_renew, COALESCE(payment_method, ''),
		       created_at, updated_at
		  FROM subscriptions
		 WHERE status = 'active'
		   AND current_period_end <= $1
		 ORDER BY current_period_end`, now)
	if err != nil {
		return nil, fmt.Errorf("finding subscriptions due for billing: %w", err)
	}
	defer rows.Close()

	var due []*Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.SubscriptionID, &s.CustomerID, &s.PlanID, &s.Status,
			&s.CurrentPeriodStart, &s.CurrentPeriodEnd, &s.CanceledAt, &s.TrialEndsAt,
			&s.AutoRenew, &s.PaymentMethod, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("reading a subscription due for billing: %w", err)
		}
		due = append(due, &s)
	}
	return due, rows.Err()
}

// AdvanceSubscriptionPeriod moves a subscription into its next period.
func (p *PostgresDB) AdvanceSubscriptionPeriod(ctx context.Context, subscriptionID, customerID string, start, end time.Time) error {
	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE subscriptions
			   SET current_period_start = $3, current_period_end = $4, updated_at = NOW()
			 WHERE subscription_id = $1::uuid AND customer_id = $2::uuid`,
			subscriptionID, customerID, start, end)
		if err != nil {
			return fmt.Errorf("advancing the billing period: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return fmt.Errorf("subscription %s: %w", subscriptionID, ErrNotFound)
		}
		return nil
	})
}

// NextPeriodEnd returns when the period starting at start should end.
//
// Calendar months rather than 30 days, because a customer on a monthly plan
// expects to be billed on the same date each month, and 30-day periods
// drift a day earlier every month until the billing date is meaningless.
// AddDate normalises overflow, so a period starting 31 January ends 3 March
// in a non-leap year -- imperfect, and the alternative (clamping to the
// last day of the month) makes the period silently shorter every time.
// Stated here so the choice is visible rather than discovered.
func NextPeriodEnd(start time.Time, billingCycle string) time.Time {
	if billingCycle == "yearly" {
		return start.AddDate(1, 0, 0)
	}
	return start.AddDate(0, 1, 0)
}

// RunBilling raises invoices for every subscription whose period has ended.
//
// Safe to run twice, three times, or concurrently with itself. That is not
// a nicety: this runs from a schedule, and schedules fire twice. Retries
// happen, an operator runs it by hand while the CronJob is running,
// yesterday's run is re-triggered after a fix. Every one of those must
// leave the customer with one invoice for the month.
//
// Two things make that true. GenerateInvoice returns the existing invoice
// for a period rather than creating a second one, enforced by a unique
// index rather than by a check this code performs. And the period is
// advanced only after the invoice for it exists, so a crash in between
// leaves the subscription in a state the next run handles identically.
//
// One subscription's failure does not stop the others. A billing run that
// aborts on the first problem bills the alphabetically-early customers and
// silently skips the rest, which is worse than billing none of them --
// nobody notices a partial run.
func (b *BillingService) RunBilling(ctx context.Context, now time.Time) (*BillingRunResult, error) {
	due, err := b.db.SubscriptionsDueForBilling(ctx, now)
	if err != nil {
		return nil, err
	}

	result := &BillingRunResult{
		RanAt:      now,
		Considered: len(due),
		Invoiced:   []BilledLine{},
		Failed:     []BillingFailure{},
	}

	for _, subscription := range due {
		plan, err := b.db.GetPlan(ctx, subscription.PlanID)
		if err != nil {
			result.Failed = append(result.Failed, BillingFailure{
				SubscriptionID: subscription.SubscriptionID,
				Error:          fmt.Sprintf("plan %s: %v", subscription.PlanID, err),
			})
			continue
		}

		invoice, err := b.GenerateInvoice(ctx, subscription.SubscriptionID)
		if err != nil {
			result.Failed = append(result.Failed, BillingFailure{
				SubscriptionID: subscription.SubscriptionID,
				Error:          err.Error(),
			})
			continue
		}

		// Only now. If this process dies here the invoice exists and the
		// period has not moved, so the next run regenerates -- gets the
		// same invoice back -- and advances. Doing it the other way round
		// would skip a customer's month entirely on a crash, which is
		// revenue nobody ever finds.
		nextStart := subscription.CurrentPeriodEnd
		nextEnd := NextPeriodEnd(nextStart, plan.BillingCycle)
		if err := b.db.AdvanceSubscriptionPeriod(ctx, subscription.SubscriptionID,
			subscription.CustomerID, nextStart, nextEnd); err != nil {
			result.Failed = append(result.Failed, BillingFailure{
				SubscriptionID: subscription.SubscriptionID,
				Error:          fmt.Sprintf("invoice %s was raised but the period did not advance: %v", invoice.InvoiceID, err),
			})
			continue
		}

		result.Invoiced = append(result.Invoiced, BilledLine{
			SubscriptionID: subscription.SubscriptionID,
			CustomerID:     subscription.CustomerID,
			InvoiceID:      invoice.InvoiceID,
			AmountCents:    invoice.Amount,
			PeriodStart:    subscription.CurrentPeriodStart,
			PeriodEnd:      subscription.CurrentPeriodEnd,
		})
	}

	log.Printf("billing run: %d due, %d invoiced, %d failed",
		result.Considered, len(result.Invoiced), len(result.Failed))
	return result, nil
}

// HandleRunBilling is the endpoint a schedule calls.
func (b *BillingService) HandleRunBilling(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result, err := b.RunBilling(r.Context(), time.Now())
	if err != nil {
		writeError(w, "billing run", err)
		return
	}

	// 207 when some subscriptions failed: the run did something, and it did
	// not do everything. A 200 would tell a schedule that failed
	// subscriptions are fine, and a 500 would hide the ones that succeeded
	// and invite a retry that re-does work already done.
	status := http.StatusOK
	if len(result.Failed) > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}
