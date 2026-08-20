package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

type PostgresDB struct {
	db *sql.DB
}

func NewPostgresDB() (*PostgresDB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}
	log.Printf("billing service connected to PostgreSQL")
	return &PostgresDB{db: db}, nil
}

func (p *PostgresDB) Close() error { return p.db.Close() }

func (p *PostgresDB) CreatePlan(ctx context.Context, plan *Plan) error {
	features, err := json.Marshal(plan.Features)
	if err != nil {
		return fmt.Errorf("failed to marshal features: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO plans (plan_id, name, description, price_cents, currency, billing_cycle, signing_limit, key_limit, support_level, features, created_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, plan.PlanID, plan.Name, plan.Description, plan.Price, plan.Currency, plan.BillingCycle, plan.SigningLimit, plan.KeyLimit, plan.SupportLevel, features, plan.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert plan: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetPlan(ctx context.Context, planID string) (*Plan, error) {
	var plan Plan
	var featuresRaw []byte
	row := p.db.QueryRowContext(ctx, `
		SELECT plan_id, name, COALESCE(description, ''), price_cents, currency, billing_cycle, signing_limit, key_limit, support_level, features, created_at
		FROM plans WHERE plan_id = $1::uuid
	`, planID)
	if err := row.Scan(&plan.PlanID, &plan.Name, &plan.Description, &plan.Price, &plan.Currency, &plan.BillingCycle, &plan.SigningLimit, &plan.KeyLimit, &plan.SupportLevel, &featuresRaw, &plan.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("plan %s not found", planID)
		}
		return nil, fmt.Errorf("failed to query plan: %w", err)
	}
	if err := json.Unmarshal(featuresRaw, &plan.Features); err != nil {
		return nil, fmt.Errorf("failed to unmarshal features: %w", err)
	}
	return &plan, nil
}

func (p *PostgresDB) CreateSubscription(ctx context.Context, s *Subscription) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO subscriptions (subscription_id, customer_id, plan_id, status, current_period_start, current_period_end, trial_ends_at, auto_renew, payment_method, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
	`, s.SubscriptionID, s.CustomerID, s.PlanID, s.Status, s.CurrentPeriodStart, s.CurrentPeriodEnd, s.TrialEndsAt, s.AutoRenew, s.PaymentMethod, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert subscription: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetSubscription(ctx context.Context, subscriptionID string) (*Subscription, error) {
	var s Subscription
	var paymentMethod sql.NullString
	row := p.db.QueryRowContext(ctx, `
		SELECT subscription_id, customer_id, plan_id, status, current_period_start, current_period_end,
		       canceled_at, trial_ends_at, auto_renew, COALESCE(payment_method, ''), created_at, updated_at
		FROM subscriptions WHERE subscription_id = $1::uuid
	`, subscriptionID)
	if err := row.Scan(&s.SubscriptionID, &s.CustomerID, &s.PlanID, &s.Status, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
		&s.CanceledAt, &s.TrialEndsAt, &s.AutoRenew, &paymentMethod, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("subscription %s not found", subscriptionID)
		}
		return nil, fmt.Errorf("failed to query subscription: %w", err)
	}
	s.PaymentMethod = paymentMethod.String
	return &s, nil
}

func (p *PostgresDB) ListSubscriptions(ctx context.Context, customerID string) ([]*Subscription, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT subscription_id, customer_id, plan_id, status, current_period_start, current_period_end,
		       canceled_at, trial_ends_at, auto_renew, COALESCE(payment_method, ''), created_at, updated_at
		FROM subscriptions WHERE customer_id = $1::uuid
		ORDER BY created_at DESC
	`, customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query subscriptions: %w", err)
	}
	defer rows.Close()

	var subs []*Subscription
	for rows.Next() {
		var s Subscription
		var paymentMethod sql.NullString
		if err := rows.Scan(&s.SubscriptionID, &s.CustomerID, &s.PlanID, &s.Status, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
			&s.CanceledAt, &s.TrialEndsAt, &s.AutoRenew, &paymentMethod, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan subscription row: %w", err)
		}
		s.PaymentMethod = paymentMethod.String
		subs = append(subs, &s)
	}
	return subs, rows.Err()
}

func (p *PostgresDB) UpdateSubscription(ctx context.Context, s *Subscription) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE subscriptions
		SET status = $2, current_period_end = $3, canceled_at = $4, auto_renew = $5, updated_at = $6
		WHERE subscription_id = $1::uuid
	`, s.SubscriptionID, s.Status, s.CurrentPeriodEnd, s.CanceledAt, s.AutoRenew, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to update subscription: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("subscription %s not found", s.SubscriptionID)
	}
	return nil
}

func (p *PostgresDB) CreateInvoice(ctx context.Context, inv *Invoice) error {
	lineItems, err := json.Marshal(inv.LineItems)
	if err != nil {
		return fmt.Errorf("failed to marshal line items: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO invoices (invoice_id, subscription_id, customer_id, amount_cents, currency, status, due_date, paid_at, line_items, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10)
	`, inv.InvoiceID, inv.SubscriptionID, inv.CustomerID, inv.Amount, inv.Currency, inv.Status, inv.DueDate, inv.PaidAt, lineItems, inv.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert invoice: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetInvoices(ctx context.Context, customerID string) ([]*Invoice, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT invoice_id, subscription_id, customer_id, amount_cents, currency, status, due_date, paid_at, line_items, created_at
		FROM invoices WHERE customer_id = $1::uuid
		ORDER BY created_at DESC
	`, customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query invoices: %w", err)
	}
	defer rows.Close()

	var invoices []*Invoice
	for rows.Next() {
		var inv Invoice
		var lineItemsRaw []byte
		if err := rows.Scan(&inv.InvoiceID, &inv.SubscriptionID, &inv.CustomerID, &inv.Amount, &inv.Currency, &inv.Status, &inv.DueDate, &inv.PaidAt, &lineItemsRaw, &inv.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan invoice row: %w", err)
		}
		if err := json.Unmarshal(lineItemsRaw, &inv.LineItems); err != nil {
			return nil, fmt.Errorf("failed to unmarshal line items: %w", err)
		}
		invoices = append(invoices, &inv)
	}
	return invoices, rows.Err()
}

func (p *PostgresDB) CreateUsageMetrics(ctx context.Context, m *UsageMetrics) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO usage_metrics (metrics_id, subscription_id, customer_id, period_start, period_end,
		                            signing_requests, key_operations, api_requests, data_transfer_gb,
		                            available_signings, available_keys, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, m.MetricsID, m.SubscriptionID, m.CustomerID, m.PeriodStart, m.PeriodEnd,
		m.SigningRequests, m.KeyOperations, m.APIRequests, m.DataTransferGB,
		m.AvailableSignings, m.AvailableKeys, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert usage metrics: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetLatestUsageMetrics(ctx context.Context, subscriptionID string) (*UsageMetrics, error) {
	var m UsageMetrics
	row := p.db.QueryRowContext(ctx, `
		SELECT metrics_id, subscription_id, customer_id, period_start, period_end,
		       signing_requests, key_operations, api_requests, data_transfer_gb,
		       available_signings, available_keys, created_at
		FROM usage_metrics WHERE subscription_id = $1::uuid
		ORDER BY created_at DESC LIMIT 1
	`, subscriptionID)
	if err := row.Scan(&m.MetricsID, &m.SubscriptionID, &m.CustomerID, &m.PeriodStart, &m.PeriodEnd,
		&m.SigningRequests, &m.KeyOperations, &m.APIRequests, &m.DataTransferGB,
		&m.AvailableSignings, &m.AvailableKeys, &m.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no usage metrics found for subscription %s", subscriptionID)
		}
		return nil, fmt.Errorf("failed to query usage metrics: %w", err)
	}
	return &m, nil
}
