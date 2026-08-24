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
//   - admin (app_admin, BYPASSRLS): used ONLY to resolve which customer an
//     integration_id belongs to.
//   - tenant (app, RLS-enforced): used for every actual read/write,
//     wrapped in withTenant so migration 011's row-level security
//     policies actually apply -- including on integration_webhook_logs,
//     which is scoped indirectly via a subquery join to
//     integrations.customer_id (see migration 011).
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

	log.Printf("marketplace service connected to PostgreSQL (admin + RLS-scoped tenant pools)")
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

// resolveCustomerIDForIntegration looks up which customer an integration
// belongs to. Runs on the admin pool -- see the type doc comment.
func (p *PostgresDB) resolveCustomerIDForIntegration(ctx context.Context, integrationID string) (string, error) {
	var customerID string
	err := p.admin.QueryRowContext(ctx, `SELECT customer_id FROM integrations WHERE integration_id = $1::uuid`, integrationID).Scan(&customerID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("integration %s not found", integrationID)
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve customer for integration %s: %w", integrationID, err)
	}
	return customerID, nil
}

func (p *PostgresDB) CreateIntegration(ctx context.Context, i *Integration) error {
	config, err := json.Marshal(i.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	retryPolicy, err := json.Marshal(i.RetryPolicy)
	if err != nil {
		return fmt.Errorf("failed to marshal retry policy: %w", err)
	}
	rateLimit, err := json.Marshal(i.RateLimit)
	if err != nil {
		return fmt.Errorf("failed to marshal rate limit: %w", err)
	}

	return p.withTenant(ctx, i.CustomerID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO integrations (integration_id, customer_id, name, description, type, status, config,
			                           webhook_url, webhook_secret, api_key, events, retry_policy, rate_limit,
			                           created_at, updated_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		`, i.IntegrationID, i.CustomerID, i.Name, i.Description, i.Type, i.Status, config,
			i.WebhookURL, i.WebhookSecret, i.ApiKey, pq.Array(i.Events), retryPolicy, rateLimit,
			i.CreatedAt, i.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert integration: %w", err)
		}
		return nil
	})
}

func (p *PostgresDB) GetIntegration(ctx context.Context, integrationID string) (*Integration, error) {
	customerID, err := p.resolveCustomerIDForIntegration(ctx, integrationID)
	if err != nil {
		return nil, err
	}

	var integrations []*Integration
	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT integration_id, customer_id, name, COALESCE(description, ''), type, status, config,
			       COALESCE(webhook_url, ''), COALESCE(webhook_secret, ''), COALESCE(api_key, ''), events,
			       retry_policy, rate_limit, created_at, updated_at, last_activated
			FROM integrations WHERE integration_id = $1::uuid
		`, integrationID)
		if err != nil {
			return fmt.Errorf("failed to query integration: %w", err)
		}
		defer rows.Close()
		integrations, err = scanIntegrations(rows)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(integrations) == 0 {
		return nil, fmt.Errorf("integration %s not found", integrationID)
	}
	return integrations[0], nil
}

func (p *PostgresDB) ListIntegrations(ctx context.Context, customerID string) ([]*Integration, error) {
	var integrations []*Integration
	err := p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT integration_id, customer_id, name, COALESCE(description, ''), type, status, config,
			       COALESCE(webhook_url, ''), COALESCE(webhook_secret, ''), COALESCE(api_key, ''), events,
			       retry_policy, rate_limit, created_at, updated_at, last_activated
			FROM integrations WHERE customer_id = $1::uuid
			ORDER BY created_at DESC
		`, customerID)
		if err != nil {
			return fmt.Errorf("failed to query integrations: %w", err)
		}
		defer rows.Close()
		integrations, err = scanIntegrations(rows)
		return err
	})
	if err != nil {
		return nil, err
	}
	return integrations, nil
}

func scanIntegrations(rows *sql.Rows) ([]*Integration, error) {
	var integrations []*Integration
	for rows.Next() {
		var i Integration
		var configRaw, retryPolicyRaw, rateLimitRaw []byte
		var events pq.StringArray
		if err := rows.Scan(&i.IntegrationID, &i.CustomerID, &i.Name, &i.Description, &i.Type, &i.Status, &configRaw,
			&i.WebhookURL, &i.WebhookSecret, &i.ApiKey, &events, &retryPolicyRaw, &rateLimitRaw,
			&i.CreatedAt, &i.UpdatedAt, &i.LastActivated); err != nil {
			return nil, fmt.Errorf("failed to scan integration row: %w", err)
		}
		i.Events = []string(events)
		if len(configRaw) > 0 {
			if err := json.Unmarshal(configRaw, &i.Config); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config: %w", err)
			}
		}
		if len(retryPolicyRaw) > 0 {
			if err := json.Unmarshal(retryPolicyRaw, &i.RetryPolicy); err != nil {
				return nil, fmt.Errorf("failed to unmarshal retry policy: %w", err)
			}
		}
		if len(rateLimitRaw) > 0 {
			if err := json.Unmarshal(rateLimitRaw, &i.RateLimit); err != nil {
				return nil, fmt.Errorf("failed to unmarshal rate limit: %w", err)
			}
		}
		integrations = append(integrations, &i)
	}
	return integrations, rows.Err()
}

func (p *PostgresDB) UpdateIntegration(ctx context.Context, i *Integration) error {
	customerID, err := p.resolveCustomerIDForIntegration(ctx, i.IntegrationID)
	if err != nil {
		return err
	}

	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE integrations
			SET name = $2, description = $3, webhook_url = $4, events = $5, status = $6,
			    last_activated = $7, updated_at = $8
			WHERE integration_id = $1::uuid
		`, i.IntegrationID, i.Name, i.Description, i.WebhookURL, pq.Array(i.Events), i.Status, i.LastActivated, i.UpdatedAt)
		if err != nil {
			return fmt.Errorf("failed to update integration: %w", err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return fmt.Errorf("integration %s not found", i.IntegrationID)
		}
		return nil
	})
}

func (p *PostgresDB) DeleteIntegration(ctx context.Context, integrationID string) error {
	customerID, err := p.resolveCustomerIDForIntegration(ctx, integrationID)
	if err != nil {
		return err
	}

	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM integrations WHERE integration_id = $1::uuid`, integrationID)
		if err != nil {
			return fmt.Errorf("failed to delete integration: %w", err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return fmt.Errorf("integration %s not found", integrationID)
		}
		return nil
	})
}

func (p *PostgresDB) CreateWebhookLog(ctx context.Context, l *WebhookLog) error {
	customerID, err := p.resolveCustomerIDForIntegration(ctx, l.IntegrationID)
	if err != nil {
		return err
	}

	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO integration_webhook_logs (log_id, integration_id, event_id, event_type, status_code,
			                                        response_time_ms, success, error_message, attempt, next_retry_at, created_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
		`, l.LogID, l.IntegrationID, l.EventID, l.EventType, l.StatusCode, l.ResponseTime, l.Success, l.ErrorMessage, l.Attempt, l.NextRetryAt, l.CreatedAt)
		if err != nil {
			return fmt.Errorf("failed to insert webhook log: %w", err)
		}
		return nil
	})
}
