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

// PostgresDB holds two connections at two different privilege levels,
// mirroring services/policy/db.go's split (the reference implementation
// for this pattern -- see docs/security/audit-checklist.md):
//
//   - admin (app_admin, BYPASSRLS): used for plans (not customer-scoped
//     data at all -- migration 011 doesn't apply RLS to the plans table,
//     since a plan like "pro tier" is platform-wide, not per-tenant) and
//     to resolve which customer a subscription_id belongs to.
//   - tenant (app, RLS-enforced): used for every actual read/write of
//     subscription/invoice/usage-metrics data, wrapped in withTenant so
//     migration 011's row-level security policies actually apply.
type PostgresDB struct {
	admin  *sql.DB
	tenant *sql.DB
}

func NewPostgresDB() (*PostgresDB, error) {
	adminDSN := os.Getenv("DATABASE_URL")
	if adminDSN == "" {
		adminDSN = "postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}
	tenantDSN := os.Getenv("DATABASE_TENANT_URL")
	if tenantDSN == "" {
		tenantDSN = "postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}

	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect admin pool: %w", err)
	}
	if err := admin.Ping(); err != nil {
		return nil, fmt.Errorf("admin pool ping failed: %w", err)
	}

	tenant, err := sql.Open("postgres", tenantDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect tenant pool: %w", err)
	}
	if err := tenant.Ping(); err != nil {
		return nil, fmt.Errorf("tenant pool ping failed: %w", err)
	}

	log.Printf("billing service connected to PostgreSQL (admin + RLS-scoped tenant pools)")
	return &PostgresDB{admin: admin, tenant: tenant}, nil
}

func (p *PostgresDB) Close() error {
	tenantErr := p.tenant.Close()
	adminErr := p.admin.Close()
	if tenantErr != nil {
		return tenantErr
	}
	return adminErr
}

// withTenant runs fn inside a transaction on the RLS-scoped tenant pool
// with app.current_customer_id set via set_config() for that
// transaction's duration -- see services/policy/db.go's withTenant.
func (p *PostgresDB) withTenant(ctx context.Context, customerID string, fn func(tx *sql.Tx) error) error {
	tx, err := p.tenant.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tenant transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.current_customer_id', $1, true)`, customerID); err != nil {
		return fmt.Errorf("failed to set tenant context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// resolveCustomerIDForSubscription looks up which customer a subscription
// belongs to. Runs on the admin pool -- see the type doc comment.
func (p *PostgresDB) resolveCustomerIDForSubscription(ctx context.Context, subscriptionID string) (string, error) {
	var customerID string
	err := p.admin.QueryRowContext(ctx, `SELECT customer_id FROM subscriptions WHERE subscription_id = $1::uuid`, subscriptionID).Scan(&customerID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("subscription %s not found", subscriptionID)
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve customer for subscription %s: %w", subscriptionID, err)
	}
	return customerID, nil
}

// Plans are not customer-scoped (no RLS policy applies to the plans
// table -- see migration 011), so these use the admin pool directly; that's
// not a tenant-isolation gap, plans are platform-wide catalog data.

func (p *PostgresDB) CreatePlan(ctx context.Context, plan *Plan) error {
	features, err := json.Marshal(plan.Features)
	if err != nil {
		return fmt.Errorf("failed to marshal features: %w", err)
	}
	_, err = p.admin.ExecContext(ctx, `
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
	row := p.admin.QueryRowContext(ctx, `
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
	return p.withTenant(ctx, s.CustomerID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO subscriptions (subscription_id, customer_id, plan_id, status, current_period_start, current_period_end, trial_ends_at, auto_renew, payment_method, created_at, updated_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
		`, s.SubscriptionID, s.CustomerID, s.PlanID, s.Status, s.CurrentPeriodStart, s.CurrentPeriodEnd, s.TrialEndsAt, s.AutoRenew, s.PaymentMethod, s.CreatedAt, s.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert subscription: %w", err)
		}
		return nil
	})
}

func (p *PostgresDB) GetSubscription(ctx context.Context, subscriptionID string) (*Subscription, error) {
	customerID, err := p.resolveCustomerIDForSubscription(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}

	var s Subscription
	var paymentMethod sql.NullString
	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT subscription_id, customer_id, plan_id, status, current_period_start, current_period_end,
			       canceled_at, trial_ends_at, auto_renew, COALESCE(payment_method, ''), created_at, updated_at
			FROM subscriptions WHERE subscription_id = $1::uuid
		`, subscriptionID)
		return row.Scan(&s.SubscriptionID, &s.CustomerID, &s.PlanID, &s.Status, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
			&s.CanceledAt, &s.TrialEndsAt, &s.AutoRenew, &paymentMethod, &s.CreatedAt, &s.UpdatedAt)
	})
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("subscription %s not found", subscriptionID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query subscription: %w", err)
	}
	s.PaymentMethod = paymentMethod.String
	return &s, nil
}

func (p *PostgresDB) ListSubscriptions(ctx context.Context, customerID string) ([]*Subscription, error) {
	var subs []*Subscription
	err := p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT subscription_id, customer_id, plan_id, status, current_period_start, current_period_end,
			       canceled_at, trial_ends_at, auto_renew, COALESCE(payment_method, ''), created_at, updated_at
			FROM subscriptions WHERE customer_id = $1::uuid
			ORDER BY created_at DESC
		`, customerID)
		if err != nil {
			return fmt.Errorf("failed to query subscriptions: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var s Subscription
			var paymentMethod sql.NullString
			if err := rows.Scan(&s.SubscriptionID, &s.CustomerID, &s.PlanID, &s.Status, &s.CurrentPeriodStart, &s.CurrentPeriodEnd,
				&s.CanceledAt, &s.TrialEndsAt, &s.AutoRenew, &paymentMethod, &s.CreatedAt, &s.UpdatedAt); err != nil {
				return fmt.Errorf("failed to scan subscription row: %w", err)
			}
			s.PaymentMethod = paymentMethod.String
			subs = append(subs, &s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return subs, nil
}

func (p *PostgresDB) UpdateSubscription(ctx context.Context, s *Subscription) error {
	customerID, err := p.resolveCustomerIDForSubscription(ctx, s.SubscriptionID)
	if err != nil {
		return err
	}

	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
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
	})
}

func (p *PostgresDB) CreateInvoice(ctx context.Context, inv *Invoice) error {
	lineItems, err := json.Marshal(inv.LineItems)
	if err != nil {
		return fmt.Errorf("failed to marshal line items: %w", err)
	}
	return p.withTenant(ctx, inv.CustomerID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO invoices (invoice_id, subscription_id, customer_id, amount_cents, currency, status, due_date, paid_at, line_items, created_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10)
		`, inv.InvoiceID, inv.SubscriptionID, inv.CustomerID, inv.Amount, inv.Currency, inv.Status, inv.DueDate, inv.PaidAt, lineItems, inv.CreatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert invoice: %w", err)
		}
		return nil
	})
}

func (p *PostgresDB) GetInvoices(ctx context.Context, customerID string) ([]*Invoice, error) {
	var invoices []*Invoice
	err := p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT invoice_id, subscription_id, customer_id, amount_cents, currency, status, due_date, paid_at, line_items, created_at
			FROM invoices WHERE customer_id = $1::uuid
			ORDER BY created_at DESC
		`, customerID)
		if err != nil {
			return fmt.Errorf("failed to query invoices: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var inv Invoice
			var lineItemsRaw []byte
			if err := rows.Scan(&inv.InvoiceID, &inv.SubscriptionID, &inv.CustomerID, &inv.Amount, &inv.Currency, &inv.Status, &inv.DueDate, &inv.PaidAt, &lineItemsRaw, &inv.CreatedAt); err != nil {
				return fmt.Errorf("failed to scan invoice row: %w", err)
			}
			if err := json.Unmarshal(lineItemsRaw, &inv.LineItems); err != nil {
				return fmt.Errorf("failed to unmarshal line items: %w", err)
			}
			invoices = append(invoices, &inv)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return invoices, nil
}

func (p *PostgresDB) CreateUsageMetrics(ctx context.Context, m *UsageMetrics) error {
	return p.withTenant(ctx, m.CustomerID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
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
	})
}

func (p *PostgresDB) GetLatestUsageMetrics(ctx context.Context, subscriptionID string) (*UsageMetrics, error) {
	customerID, err := p.resolveCustomerIDForSubscription(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}

	var m UsageMetrics
	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT metrics_id, subscription_id, customer_id, period_start, period_end,
			       signing_requests, key_operations, api_requests, data_transfer_gb,
			       available_signings, available_keys, created_at
			FROM usage_metrics WHERE subscription_id = $1::uuid
			ORDER BY created_at DESC LIMIT 1
		`, subscriptionID)
		return row.Scan(&m.MetricsID, &m.SubscriptionID, &m.CustomerID, &m.PeriodStart, &m.PeriodEnd,
			&m.SigningRequests, &m.KeyOperations, &m.APIRequests, &m.DataTransferGB,
			&m.AvailableSignings, &m.AvailableKeys, &m.CreatedAt)
	})
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no usage metrics found for subscription %s", subscriptionID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query usage metrics: %w", err)
	}
	return &m, nil
}
