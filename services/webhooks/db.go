package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/lib/pq"
)

// PostgresDB holds two connections at two different privilege levels,
// mirroring services/policy/db.go's split (the reference implementation
// for this pattern -- see docs/security/audit-checklist.md):
//
//   - admin (app_admin, BYPASSRLS): used ONLY to resolve which customer a
//     webhook_id/delivery_id belongs to.
//   - tenant (app, RLS-enforced): used for every actual read/write,
//     wrapped in withTenant so migration 011's row-level security
//     policies actually apply -- including on webhook_deliveries, which
//     has no customer_id column of its own and is scoped indirectly via
//     a subquery join to webhooks.customer_id (see migration 011).
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

	log.Printf("webhooks service connected to PostgreSQL (admin + RLS-scoped tenant pools)")
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

// resolveCustomerIDForWebhook looks up which customer a webhook belongs
// to. Runs on the admin pool -- see the type doc comment.
func (p *PostgresDB) resolveCustomerIDForWebhook(ctx context.Context, webhookID string) (string, error) {
	var customerID string
	err := p.admin.QueryRowContext(ctx, `SELECT customer_id FROM webhooks WHERE webhook_id = $1::uuid`, webhookID).Scan(&customerID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("webhook %s not found", webhookID)
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve customer for webhook %s: %w", webhookID, err)
	}
	return customerID, nil
}

// resolveCustomerIDForDelivery joins through webhook_deliveries.webhook_id
// to webhooks.customer_id, since a delivery record carries no customer_id
// of its own (see migration 011's indirect-scoping note on this table).
func (p *PostgresDB) resolveCustomerIDForDelivery(ctx context.Context, deliveryID string) (string, error) {
	var customerID string
	err := p.admin.QueryRowContext(ctx, `
		SELECT w.customer_id FROM webhook_deliveries d
		JOIN webhooks w ON w.webhook_id = d.webhook_id
		WHERE d.delivery_id = $1::uuid
	`, deliveryID).Scan(&customerID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("delivery %s not found", deliveryID)
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve customer for delivery %s: %w", deliveryID, err)
	}
	return customerID, nil
}

func (p *PostgresDB) GetWebhooksByCustomer(ctx context.Context, customerID string) ([]*Webhook, error) {
	var webhooks []*Webhook
	err := p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT webhook_id, customer_id, url, secret, events, is_active, max_retries, backoff_seconds,
			       exponential_backoff, custom_headers, created_at, updated_at
			FROM webhooks WHERE customer_id = $1::uuid
		`, customerID)
		if err != nil {
			return fmt.Errorf("failed to query webhooks: %w", err)
		}
		defer rows.Close()
		webhooks, err = scanWebhooks(rows)
		return err
	})
	if err != nil {
		return nil, err
	}
	return webhooks, nil
}

func (p *PostgresDB) GetWebhook(ctx context.Context, webhookID string) (*Webhook, error) {
	customerID, err := p.resolveCustomerIDForWebhook(ctx, webhookID)
	if err != nil {
		return nil, err
	}

	var webhooks []*Webhook
	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT webhook_id, customer_id, url, secret, events, is_active, max_retries, backoff_seconds,
			       exponential_backoff, custom_headers, created_at, updated_at
			FROM webhooks WHERE webhook_id = $1::uuid
		`, webhookID)
		if err != nil {
			return fmt.Errorf("failed to query webhook: %w", err)
		}
		defer rows.Close()
		webhooks, err = scanWebhooks(rows)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(webhooks) == 0 {
		return nil, fmt.Errorf("webhook %s not found", webhookID)
	}
	return webhooks[0], nil
}

func scanWebhooks(rows *sql.Rows) ([]*Webhook, error) {
	var webhooks []*Webhook
	for rows.Next() {
		var w Webhook
		var events pq.StringArray
		var customHeadersRaw []byte
		if err := rows.Scan(&w.WebhookID, &w.CustomerID, &w.URL, &w.Secret, &events, &w.IsActive, &w.MaxRetries,
			&w.BackoffSeconds, &w.ExponentialBackoff, &customHeadersRaw, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan webhook row: %w", err)
		}
		w.Events = []string(events)
		if len(customHeadersRaw) > 0 {
			if err := json.Unmarshal(customHeadersRaw, &w.CustomHeaders); err != nil {
				return nil, fmt.Errorf("failed to unmarshal custom headers: %w", err)
			}
		}
		webhooks = append(webhooks, &w)
	}
	return webhooks, rows.Err()
}

func (p *PostgresDB) CreateWebhookDelivery(ctx context.Context, d *WebhookDelivery) error {
	customerID, err := p.resolveCustomerIDForWebhook(ctx, d.WebhookID)
	if err != nil {
		return err
	}

	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO webhook_deliveries (delivery_id, webhook_id, event_id, event_type, attempt, status_code,
			                                  response_time_ms, success, error_message, next_retry_at, created_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
		`, d.DeliveryID, d.WebhookID, d.EventID, d.EventType, d.Attempt, d.StatusCode, d.ResponseTime, d.Success, d.ErrorMessage, d.NextRetryAt, d.CreatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert webhook delivery: %w", err)
		}
		return nil
	})
}

func (p *PostgresDB) GetWebhookDelivery(ctx context.Context, deliveryID string) (*WebhookDelivery, error) {
	customerID, err := p.resolveCustomerIDForDelivery(ctx, deliveryID)
	if err != nil {
		return nil, err
	}

	var d WebhookDelivery
	var errMsg sql.NullString
	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT delivery_id, webhook_id, event_id, event_type, attempt, status_code, response_time_ms,
			       success, error_message, next_retry_at, created_at
			FROM webhook_deliveries WHERE delivery_id = $1::uuid
		`, deliveryID)
		return row.Scan(&d.DeliveryID, &d.WebhookID, &d.EventID, &d.EventType, &d.Attempt, &d.StatusCode, &d.ResponseTime,
			&d.Success, &errMsg, &d.NextRetryAt, &d.CreatedAt)
	})
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("delivery %s not found", deliveryID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query webhook delivery: %w", err)
	}
	d.ErrorMessage = errMsg.String
	return &d, nil
}

func (p *PostgresDB) GetWebhookDeliveries(ctx context.Context, webhookID string, limit int) ([]*WebhookDelivery, error) {
	customerID, err := p.resolveCustomerIDForWebhook(ctx, webhookID)
	if err != nil {
		return nil, err
	}

	var deliveries []*WebhookDelivery
	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT delivery_id, webhook_id, event_id, event_type, attempt, status_code, response_time_ms,
			       success, error_message, next_retry_at, created_at
			FROM webhook_deliveries WHERE webhook_id = $1::uuid
			ORDER BY created_at DESC LIMIT $2
		`, webhookID, limit)
		if err != nil {
			return fmt.Errorf("failed to query webhook deliveries: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var d WebhookDelivery
			var errMsg sql.NullString
			if err := rows.Scan(&d.DeliveryID, &d.WebhookID, &d.EventID, &d.EventType, &d.Attempt, &d.StatusCode, &d.ResponseTime,
				&d.Success, &errMsg, &d.NextRetryAt, &d.CreatedAt); err != nil {
				return fmt.Errorf("failed to scan webhook delivery row: %w", err)
			}
			d.ErrorMessage = errMsg.String
			deliveries = append(deliveries, &d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return deliveries, nil
}
