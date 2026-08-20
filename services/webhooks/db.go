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
	log.Printf("webhooks service connected to PostgreSQL")
	return &PostgresDB{db: db}, nil
}

func (p *PostgresDB) Close() error { return p.db.Close() }

func (p *PostgresDB) GetWebhooksByCustomer(ctx context.Context, customerID string) ([]*Webhook, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT webhook_id, customer_id, url, secret, events, is_active, max_retries, backoff_seconds,
		       exponential_backoff, custom_headers, created_at, updated_at
		FROM webhooks WHERE customer_id = $1::uuid
	`, customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query webhooks: %w", err)
	}
	defer rows.Close()
	return scanWebhooks(rows)
}

func (p *PostgresDB) GetWebhook(ctx context.Context, webhookID string) (*Webhook, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT webhook_id, customer_id, url, secret, events, is_active, max_retries, backoff_seconds,
		       exponential_backoff, custom_headers, created_at, updated_at
		FROM webhooks WHERE webhook_id = $1::uuid
	`, webhookID)
	if err != nil {
		return nil, fmt.Errorf("failed to query webhook: %w", err)
	}
	defer rows.Close()
	webhooks, err := scanWebhooks(rows)
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
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO webhook_deliveries (delivery_id, webhook_id, event_id, event_type, attempt, status_code,
		                                  response_time_ms, success, error_message, next_retry_at, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11)
	`, d.DeliveryID, d.WebhookID, d.EventID, d.EventType, d.Attempt, d.StatusCode, d.ResponseTime, d.Success, d.ErrorMessage, d.NextRetryAt, d.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert webhook delivery: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetWebhookDelivery(ctx context.Context, deliveryID string) (*WebhookDelivery, error) {
	var d WebhookDelivery
	var errMsg sql.NullString
	row := p.db.QueryRowContext(ctx, `
		SELECT delivery_id, webhook_id, event_id, event_type, attempt, status_code, response_time_ms,
		       success, error_message, next_retry_at, created_at
		FROM webhook_deliveries WHERE delivery_id = $1::uuid
	`, deliveryID)
	if err := row.Scan(&d.DeliveryID, &d.WebhookID, &d.EventID, &d.EventType, &d.Attempt, &d.StatusCode, &d.ResponseTime,
		&d.Success, &errMsg, &d.NextRetryAt, &d.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("delivery %s not found", deliveryID)
		}
		return nil, fmt.Errorf("failed to query webhook delivery: %w", err)
	}
	d.ErrorMessage = errMsg.String
	return &d, nil
}

func (p *PostgresDB) GetWebhookDeliveries(ctx context.Context, webhookID string, limit int) ([]*WebhookDelivery, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT delivery_id, webhook_id, event_id, event_type, attempt, status_code, response_time_ms,
		       success, error_message, next_retry_at, created_at
		FROM webhook_deliveries WHERE webhook_id = $1::uuid
		ORDER BY created_at DESC LIMIT $2
	`, webhookID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query webhook deliveries: %w", err)
	}
	defer rows.Close()

	var deliveries []*WebhookDelivery
	for rows.Next() {
		var d WebhookDelivery
		var errMsg sql.NullString
		if err := rows.Scan(&d.DeliveryID, &d.WebhookID, &d.EventID, &d.EventType, &d.Attempt, &d.StatusCode, &d.ResponseTime,
			&d.Success, &errMsg, &d.NextRetryAt, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan webhook delivery row: %w", err)
		}
		d.ErrorMessage = errMsg.String
		deliveries = append(deliveries, &d)
	}
	return deliveries, rows.Err()
}
