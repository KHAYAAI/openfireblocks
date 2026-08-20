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
	log.Printf("marketplace service connected to PostgreSQL")
	return &PostgresDB{db: db}, nil
}

func (p *PostgresDB) Close() error { return p.db.Close() }

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

	_, err = p.db.ExecContext(ctx, `
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
}

func (p *PostgresDB) GetIntegration(ctx context.Context, integrationID string) (*Integration, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT integration_id, customer_id, name, COALESCE(description, ''), type, status, config,
		       COALESCE(webhook_url, ''), COALESCE(webhook_secret, ''), COALESCE(api_key, ''), events,
		       retry_policy, rate_limit, created_at, updated_at, last_activated
		FROM integrations WHERE integration_id = $1::uuid
	`, integrationID)
	if err != nil {
		return nil, fmt.Errorf("failed to query integration: %w", err)
	}
	defer rows.Close()
	integrations, err := scanIntegrations(rows)
	if err != nil {
		return nil, err
	}
	if len(integrations) == 0 {
		return nil, fmt.Errorf("integration %s not found", integrationID)
	}
	return integrations[0], nil
}

func (p *PostgresDB) ListIntegrations(ctx context.Context, customerID string) ([]*Integration, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT integration_id, customer_id, name, COALESCE(description, ''), type, status, config,
		       COALESCE(webhook_url, ''), COALESCE(webhook_secret, ''), COALESCE(api_key, ''), events,
		       retry_policy, rate_limit, created_at, updated_at, last_activated
		FROM integrations WHERE customer_id = $1::uuid
		ORDER BY created_at DESC
	`, customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query integrations: %w", err)
	}
	defer rows.Close()
	return scanIntegrations(rows)
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
	result, err := p.db.ExecContext(ctx, `
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
}

func (p *PostgresDB) DeleteIntegration(ctx context.Context, integrationID string) error {
	result, err := p.db.ExecContext(ctx, `DELETE FROM integrations WHERE integration_id = $1::uuid`, integrationID)
	if err != nil {
		return fmt.Errorf("failed to delete integration: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("integration %s not found", integrationID)
	}
	return nil
}

func (p *PostgresDB) CreateWebhookLog(ctx context.Context, l *WebhookLog) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO integration_webhook_logs (log_id, integration_id, event_id, event_type, status_code,
		                                        response_time_ms, success, error_message, attempt, next_retry_at, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
	`, l.LogID, l.IntegrationID, l.EventID, l.EventType, l.StatusCode, l.ResponseTime, l.Success, l.ErrorMessage, l.Attempt, l.NextRetryAt, l.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert webhook log: %w", err)
	}
	return nil
}
