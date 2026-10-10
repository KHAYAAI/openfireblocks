package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func timeDurationFromNs(ns int64) time.Duration {
	return time.Duration(ns)
}

// -- Compliance metrics (compliance_metrics table, migration 010) --
//
// RecordMetric (monitoring.go) always writes a fresh reading rather than
// mutating an existing one -- a metric is a point-in-time measurement, and
// history of past readings is exactly what a SOC 2 auditor wants to see, so
// this is a plain INSERT, not an upsert.

func (p *PostgresDB) CreateComplianceMetric(ctx context.Context, m *ComplianceMetric) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO compliance_metrics (metric_id, name, category, value, unit, threshold, status, measured_at, description, target)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, m.ID, m.Name, m.Category, m.Value, m.Unit, m.Threshold, m.Status, nullableTime(m.MeasuredAt), m.Description, m.Target)
	if err != nil {
		return fmt.Errorf("failed to insert compliance metric: %w", err)
	}
	return nil
}

const complianceMetricColumns = `metric_id, name, category, value, unit, threshold, status, measured_at, description, target`

func scanComplianceMetric(row interface {
	Scan(dest ...interface{}) error
}) (*ComplianceMetric, error) {
	var m ComplianceMetric
	var unit, description sql.NullString
	if err := row.Scan(&m.ID, &m.Name, &m.Category, &m.Value, &unit, &m.Threshold, &m.Status, &m.MeasuredAt, &description, &m.Target); err != nil {
		return nil, err
	}
	m.Unit, m.Description = unit.String, description.String
	return &m, nil
}

func (p *PostgresDB) GetComplianceMetric(ctx context.Context, metricID string) (*ComplianceMetric, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+complianceMetricColumns+` FROM compliance_metrics WHERE metric_id = $1`, metricID)
	m, err := scanComplianceMetric(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("compliance metric %s not found", metricID)
		}
		return nil, fmt.Errorf("failed to query compliance metric: %w", err)
	}
	return m, nil
}

// GetLatestComplianceMetricsByCategory returns the most recent reading of
// each distinct metric name in the category (not every historical row --
// GetMetricsByCategory in monitoring.go is used for "current state"
// dashboards, and a category can have many past readings of the same named
// metric).
func (p *PostgresDB) GetLatestComplianceMetricsByCategory(ctx context.Context, category string) ([]ComplianceMetric, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT DISTINCT ON (name) `+complianceMetricColumns+`
		FROM compliance_metrics
		WHERE category = $1
		ORDER BY name, measured_at DESC
	`, category)
	if err != nil {
		return nil, fmt.Errorf("failed to query compliance metrics by category: %w", err)
	}
	defer rows.Close()
	var metrics []ComplianceMetric
	for rows.Next() {
		m, err := scanComplianceMetric(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan compliance metric row: %w", err)
		}
		metrics = append(metrics, *m)
	}
	return metrics, rows.Err()
}

// GetLatestComplianceMetrics returns the most recent reading of every
// distinct metric name, across all categories -- backs GenerateDashboard.
func (p *PostgresDB) GetLatestComplianceMetrics(ctx context.Context) ([]ComplianceMetric, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT DISTINCT ON (name) `+complianceMetricColumns+`
		FROM compliance_metrics
		ORDER BY name, measured_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest compliance metrics: %w", err)
	}
	defer rows.Close()
	var metrics []ComplianceMetric
	for rows.Next() {
		m, err := scanComplianceMetric(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan compliance metric row: %w", err)
		}
		metrics = append(metrics, *m)
	}
	return metrics, rows.Err()
}

// -- Compliance alerts (compliance_alerts table) --

func (p *PostgresDB) CreateComplianceAlert(ctx context.Context, a *ComplianceAlert) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO compliance_alerts (alert_id, metric_id, severity, message, recommended_action, created_at, resolved_at, status)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8)
	`, a.ID, a.MetricID, a.Severity, a.Message, a.RecommendedAction, nullableTime(a.CreatedAt), nullableTime(a.ResolvedAt), a.Status)
	if err != nil {
		return fmt.Errorf("failed to insert compliance alert: %w", err)
	}
	return nil
}

const complianceAlertColumns = `alert_id, COALESCE(metric_id, ''), severity, message, recommended_action, created_at, resolved_at, status`

func scanComplianceAlert(row interface {
	Scan(dest ...interface{}) error
}) (*ComplianceAlert, error) {
	var a ComplianceAlert
	var message, recommendedAction sql.NullString
	var resolvedAt sql.NullTime
	if err := row.Scan(&a.ID, &a.MetricID, &a.Severity, &message, &recommendedAction, &a.CreatedAt, &resolvedAt, &a.Status); err != nil {
		return nil, err
	}
	a.Message, a.RecommendedAction = message.String, recommendedAction.String
	a.ResolvedAt = resolvedAt.Time
	return &a, nil
}

func (p *PostgresDB) GetComplianceAlert(ctx context.Context, alertID string) (*ComplianceAlert, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+complianceAlertColumns+` FROM compliance_alerts WHERE alert_id = $1`, alertID)
	a, err := scanComplianceAlert(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("compliance alert %s not found", alertID)
		}
		return nil, fmt.Errorf("failed to query compliance alert: %w", err)
	}
	return a, nil
}

func (p *PostgresDB) UpdateComplianceAlertStatus(ctx context.Context, alertID, status string, resolvedAt time.Time) error {
	res, err := p.db.ExecContext(ctx, `
		UPDATE compliance_alerts SET status = $2, resolved_at = $3 WHERE alert_id = $1
	`, alertID, status, nullableTime(resolvedAt))
	if err != nil {
		return fmt.Errorf("failed to update compliance alert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("compliance alert %s not found", alertID)
	}
	return nil
}

func (p *PostgresDB) ListOpenComplianceAlerts(ctx context.Context) ([]ComplianceAlert, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+complianceAlertColumns+` FROM compliance_alerts WHERE status IN ('open', 'acknowledged') ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query open compliance alerts: %w", err)
	}
	defer rows.Close()
	var alerts []ComplianceAlert
	for rows.Next() {
		a, err := scanComplianceAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan compliance alert row: %w", err)
		}
		alerts = append(alerts, *a)
	}
	return alerts, rows.Err()
}
