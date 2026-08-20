package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

// PostgresDB is the persistence layer for the compliance service. It backs
// KYC/AML verification (kyc_aml.go, aml_kyc.go, onfido_integration.go),
// audit logging and reporting (reporting.go) against the real schema in
// infrastructure/database/migrations -- not against tables invented here.
type PostgresDB struct {
	db *sql.DB
}

// NewPostgresDB creates a new database connection, mirroring
// services/ceremony-orchestrator/db.go's pattern for consistency across services.
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

	log.Printf("compliance service connected to PostgreSQL")
	return &PostgresDB{db: db}, nil
}

func (p *PostgresDB) Close() error {
	return p.db.Close()
}

// -- Audit logs (audit_logs table) --
//
// AuditLog.Actor and .Changes have no dedicated columns in audit_logs (it
// has entity_type/entity_id/action/details/ip_address/user_agent/status) --
// both are folded into the details JSONB column rather than requiring a
// migration for two more varchar columns.

type auditLogDetails struct {
	Actor   string `json:"actor"`
	Changes string `json:"changes,omitempty"`
}

func (p *PostgresDB) CreateAuditLog(ctx context.Context, entry *AuditLog) error {
	details, err := json.Marshal(auditLogDetails{Actor: entry.Actor, Changes: entry.Changes})
	if err != nil {
		return fmt.Errorf("failed to marshal audit log details: %w", err)
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO audit_logs (log_id, customer_id, entity_type, entity_id, action, details, ip_address, user_agent, status, created_at)
		VALUES (COALESCE(NULLIF($1, ''), uuid_generate_v4()::text)::uuid, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($10, NOW()))
	`, entry.LogID, entry.CustomerID, entry.ResourceType, entry.ResourceID, entry.Action, details, entry.IPAddress, entry.UserAgent, entry.Status, nullableTime(entry.CreatedAt))
	if err != nil {
		return fmt.Errorf("failed to insert audit log: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetAuditLogs(ctx context.Context, customerID string, startDate, endDate time.Time) ([]*AuditLog, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT log_id, customer_id, entity_type, entity_id, action, details, ip_address, user_agent, status, created_at
		FROM audit_logs
		WHERE customer_id = $1 AND created_at BETWEEN $2 AND $3
		ORDER BY created_at DESC
	`, customerID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit logs: %w", err)
	}
	defer rows.Close()
	return scanAuditLogs(rows)
}

func (p *PostgresDB) GetRecentAuditLogs(ctx context.Context, customerID string, limit int) ([]*AuditLog, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT log_id, customer_id, entity_type, entity_id, action, details, ip_address, user_agent, status, created_at
		FROM audit_logs
		WHERE customer_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, customerID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent audit logs: %w", err)
	}
	defer rows.Close()
	return scanAuditLogs(rows)
}

func scanAuditLogs(rows *sql.Rows) ([]*AuditLog, error) {
	var logs []*AuditLog
	for rows.Next() {
		var l AuditLog
		var detailsRaw []byte
		var ipAddress, userAgent, status sql.NullString
		if err := rows.Scan(&l.LogID, &l.CustomerID, &l.ResourceType, &l.ResourceID, &l.Action, &detailsRaw, &ipAddress, &userAgent, &status, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan audit log row: %w", err)
		}
		l.IPAddress = ipAddress.String
		l.UserAgent = userAgent.String
		l.Status = status.String
		if len(detailsRaw) > 0 {
			var d auditLogDetails
			if err := json.Unmarshal(detailsRaw, &d); err == nil {
				l.Actor = d.Actor
				l.Changes = d.Changes
			}
		}
		logs = append(logs, &l)
	}
	return logs, rows.Err()
}

// -- Compliance reports (compliance_reports table, migration 003) --

func (p *PostgresDB) CreateComplianceReport(ctx context.Context, report *ComplianceReport) error {
	data, err := json.Marshal(report.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal report data: %w", err)
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO compliance_reports (report_id, customer_id, report_type, period, start_date, end_date, status, data, generated_at, expires_at)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8, $9, $10)
	`, report.ReportID, report.CustomerID, report.ReportType, report.Period, report.StartDate, report.EndDate, report.Status, data, report.GeneratedAt, report.ExpiresAt)
	if err != nil {
		return fmt.Errorf("failed to insert compliance report: %w", err)
	}
	return nil
}

// -- KYC verification, generic provider path (kyc_aml.go's KYCVerification) --
//
// Backed by compliance_checks with check_type = 'kyc_verification'. See
// GetLatestOnfidoVerification below for the separate Onfido-specific path,
// which uses a different check_type so the two never collide.

const checkTypeKYCGeneric = "kyc_verification"

func (p *PostgresDB) StoreKYCVerification(ctx context.Context, v *KYCVerification) error {
	details, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal KYC verification: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO compliance_checks (check_id, customer_id, check_type, status, details, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
	`, v.VerificationID, v.CustomerID, checkTypeKYCGeneric, v.Status, details, v.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert KYC verification: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetLatestKYCVerification(ctx context.Context, customerID string) (*KYCVerification, error) {
	var detailsRaw []byte
	row := p.db.QueryRowContext(ctx, `
		SELECT details FROM compliance_checks
		WHERE customer_id = $1::uuid AND check_type = $2
		ORDER BY created_at DESC LIMIT 1
	`, customerID, checkTypeKYCGeneric)
	if err := row.Scan(&detailsRaw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query latest KYC verification: %w", err)
	}
	var v KYCVerification
	if err := json.Unmarshal(detailsRaw, &v); err != nil {
		return nil, fmt.Errorf("failed to unmarshal KYC verification: %w", err)
	}
	return &v, nil
}

// -- KYC verification, Onfido-specific path (onfido_integration.go's OnfidoKYCVerification) --

const checkTypeKYCOnfido = "kyc_verification_onfido"

func (p *PostgresDB) CreateOnfidoVerification(ctx context.Context, v *OnfidoKYCVerification) error {
	details, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal Onfido verification: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO compliance_checks (check_id, customer_id, check_type, status, details, created_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
	`, v.VerificationID, v.CustomerID, checkTypeKYCOnfido, v.Status, details, v.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert Onfido verification: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetLatestOnfidoVerification(ctx context.Context, customerID string) (*OnfidoKYCVerification, error) {
	var detailsRaw []byte
	row := p.db.QueryRowContext(ctx, `
		SELECT details FROM compliance_checks
		WHERE customer_id = $1::uuid AND check_type = $2
		ORDER BY created_at DESC LIMIT 1
	`, customerID, checkTypeKYCOnfido)
	if err := row.Scan(&detailsRaw); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no Onfido verification found for customer %s", customerID)
		}
		return nil, fmt.Errorf("failed to query latest Onfido verification: %w", err)
	}
	var v OnfidoKYCVerification
	if err := json.Unmarshal(detailsRaw, &v); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Onfido verification: %w", err)
	}
	return &v, nil
}

// UpdateCustomerKYCStatus updates the customer's denormalized current KYC
// status (migration 004). The durable event history is the
// compliance_checks rows written by CreateOnfidoVerification /
// StoreKYCVerification above; this is only the fast-lookup current state.
func (p *PostgresDB) UpdateCustomerKYCStatus(ctx context.Context, customerID, status string, verifiedAt time.Time) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE customers SET kyc_status = $2, kyc_verified_at = NULLIF($3, '0001-01-01 00:00:00+00'::timestamptz)
		WHERE customer_id = $1::uuid
	`, customerID, status, verifiedAt)
	if err != nil {
		return fmt.Errorf("failed to update customer KYC status: %w", err)
	}
	return nil
}

// -- Risk assessments (compliance_checks, check_type = 'transaction_risk_assessment') --

const checkTypeRiskAssessment = "transaction_risk_assessment"

func (p *PostgresDB) StoreRiskAssessment(ctx context.Context, customerID string, assessment *RiskAssessment) error {
	details, err := json.Marshal(assessment)
	if err != nil {
		return fmt.Errorf("failed to marshal risk assessment: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO compliance_checks (check_id, customer_id, request_id, check_type, status, details, created_at)
		VALUES (uuid_generate_v4(), $1::uuid, NULLIF($2, '')::uuid, $3, $4, $5, $6)
	`, customerID, assessment.TransactionID, checkTypeRiskAssessment, assessment.RiskLevel, details, assessment.AssessedAt)
	if err != nil {
		return fmt.Errorf("failed to insert risk assessment: %w", err)
	}
	return nil
}

// -- AML structuring signal (signing_requests) --

func (p *PostgresDB) CountRecentSigningRequests(ctx context.Context, customerID string, since time.Time) (int, error) {
	var count int
	row := p.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM signing_requests
		WHERE customer_id = $1::uuid AND created_at >= $2
	`, customerID, since)
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count recent signing requests: %w", err)
	}
	return count, nil
}

// -- Report statistics (reporting.go) --
//
// Two figures TransactionStats promises -- TotalVolume and AverageAmount --
// aren't derivable: signing_requests stores the raw transaction_data bytes,
// not a decoded amount column, so there's no SQL query that produces them
// honestly. Rather than inventing plausible-looking numbers (exactly the
// problem this function used to have), those two fields are returned as an
// explicit "not tracked" marker instead of a value.

const amountNotTracked = "not tracked: signing_requests stores raw transaction_data, not a decoded amount"

func (p *PostgresDB) GetTransactionStats(ctx context.Context, customerID string, startDate, endDate time.Time) (*TransactionStats, error) {
	var stats TransactionStats
	var avgLatencyMs sql.NullFloat64
	row := p.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status = 'completed') AS successful,
			COUNT(*) FILTER (WHERE status = 'failed') AS failed,
			AVG(latency_ms) FILTER (WHERE status = 'completed') AS avg_latency_ms
		FROM signing_requests
		WHERE customer_id = $1::uuid AND created_at BETWEEN $2 AND $3
	`, customerID, startDate, endDate)
	if err := row.Scan(&stats.TotalTransactions, &stats.SuccessfulTxs, &stats.FailedTxs, &avgLatencyMs); err != nil {
		return nil, fmt.Errorf("failed to query transaction stats: %w", err)
	}

	if stats.TotalTransactions > 0 {
		stats.SuccessRate = float64(stats.SuccessfulTxs) / float64(stats.TotalTransactions) * 100
	}
	if avgLatencyMs.Valid {
		stats.AverageConfirmTime = int64(avgLatencyMs.Float64 / 1000) // ms -> seconds
	}
	stats.TotalVolume = amountNotTracked
	stats.AverageAmount = amountNotTracked

	var peakHour sql.NullString
	peakRow := p.db.QueryRowContext(ctx, `
		SELECT to_char(date_trunc('hour', created_at), 'HH24:00') AS hour_bucket
		FROM signing_requests
		WHERE customer_id = $1::uuid AND created_at BETWEEN $2 AND $3 AND status = 'completed'
		GROUP BY hour_bucket
		ORDER BY COUNT(*) DESC
		LIMIT 1
	`, customerID, startDate, endDate)
	if err := peakRow.Scan(&peakHour); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to query peak transaction hour: %w", err)
	}
	if peakHour.Valid {
		stats.PeakTransactionTime = peakHour.String + " UTC"
	}

	return &stats, nil
}

func (p *PostgresDB) GetKeyStats(ctx context.Context, customerID string, startDate, endDate time.Time) (*KeyStats, error) {
	var stats KeyStats
	var avgAgeDays sql.NullFloat64
	row := p.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE status = 'active') AS active,
			COUNT(*) FILTER (WHERE status = 'pending_dkg') AS pending,
			COUNT(*) FILTER (WHERE status = 'compromised') AS compromised,
			AVG(EXTRACT(EPOCH FROM (NOW() - created_at)) / 86400) AS avg_age_days,
			COUNT(*) FILTER (
				WHERE status = 'active' AND key_id NOT IN (
					SELECT key_id FROM signing_requests WHERE created_at >= NOW() - INTERVAL '30 days'
				)
			) AS unused
		FROM key_pairs
		WHERE customer_id = $1::uuid AND created_at <= $2
	`, customerID, endDate)
	if err := row.Scan(&stats.TotalKeys, &stats.ActiveKeys, &stats.PendingKeys, &stats.CompromisedKeys, &avgAgeDays, &stats.UnusedKeys); err != nil {
		return nil, fmt.Errorf("failed to query key stats: %w", err)
	}
	if avgAgeDays.Valid {
		stats.AverageKeyAge = int(avgAgeDays.Float64)
	}
	// key_pairs has no rotation-event column (no rotated_at), so a rate of
	// "keys rotated per month" isn't derivable from this schema -- reported
	// as 0 rather than a fabricated figure. Wiring this up for real needs a
	// key-rotation event log, e.g. rows in dkg_ceremonies keyed by the
	// key_id being rotated.
	stats.KeyRotationRate = 0

	return &stats, nil
}

// GetCustomerComplianceStats scopes ComplianceStats to one customer, for
// GenerateAuditReport. See GetGlobalComplianceStats below for the
// platform-wide version used in GenerateComplianceReport.
func (p *PostgresDB) GetCustomerComplianceStats(ctx context.Context, customerID string, startDate, endDate time.Time) (*ComplianceStats, error) {
	return p.complianceStats(ctx, "customer_id = $1::uuid AND created_at BETWEEN $2 AND $3", customerID, startDate, endDate)
}

func (p *PostgresDB) GetGlobalComplianceStats(ctx context.Context, startDate, endDate time.Time) (*ComplianceStats, error) {
	return p.complianceStats(ctx, "created_at BETWEEN $1 AND $2", startDate, endDate)
}

func (p *PostgresDB) complianceStats(ctx context.Context, where string, args ...interface{}) (*ComplianceStats, error) {
	var stats ComplianceStats
	query := fmt.Sprintf(`
		SELECT
			COUNT(DISTINCT customer_id) FILTER (WHERE check_type IN ('%s', '%s') AND status IN ('verified', 'approved')) AS verified_customers,
			COUNT(*) FILTER (WHERE check_type IN ('%s', '%s') AND status IN ('pending', 'need_review', 'under_review')) AS pending_verifications,
			COUNT(*) FILTER (WHERE check_type IN ('%s', '%s') AND status = 'rejected') AS failed_verifications,
			COUNT(*) FILTER (WHERE check_type = '%s') AS sanctions_matches,
			COUNT(*) FILTER (WHERE check_type = '%s' AND status = 'blocked') AS high_risk_detected
		FROM compliance_checks
		WHERE %s
	`, checkTypeKYCGeneric, checkTypeKYCOnfido, checkTypeKYCGeneric, checkTypeKYCOnfido, checkTypeKYCGeneric, checkTypeKYCOnfido, checkTypeRiskAssessment, checkTypeRiskAssessment, where)

	row := p.db.QueryRowContext(ctx, query, args...)
	if err := row.Scan(&stats.VerifiedCustomers, &stats.PendingVerifications, &stats.FailedVerifications, &stats.SanctionsMatches, &stats.HighRiskDetected); err != nil {
		return nil, fmt.Errorf("failed to query compliance stats: %w", err)
	}

	total := stats.VerifiedCustomers + stats.PendingVerifications + stats.FailedVerifications
	if total > 0 {
		stats.VerificationRate = float64(stats.VerifiedCustomers) / float64(total) * 100
	}
	// ComplianceScore is a weighted judgment call (how much a failed
	// verification vs. a sanctions match should cost), not a raw query
	// result -- kept simple and documented rather than dressed up as more
	// precise than it is.
	score := 100 - stats.FailedVerifications*5 - stats.HighRiskDetected*10
	if score < 0 {
		score = 0
	}
	stats.ComplianceScore = score

	return &stats, nil
}

// GetIncidents backs reporting.go's audit/compliance reports. It reads the
// same security_incidents table incident_response.go's IncidentManager now
// writes to, and reshapes each row into the lightweight Incident type this
// report uses (distinct from SecurityIncident -- see the type doc comment
// in reporting.go).
func (p *PostgresDB) GetIncidents(ctx context.Context, startDate, endDate time.Time) ([]*Incident, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+securityIncidentColumns+`
		FROM security_incidents
		WHERE reported_at BETWEEN $1 AND $2
		ORDER BY reported_at DESC
	`, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to query incidents: %w", err)
	}
	defer rows.Close()

	var incidents []*Incident
	for rows.Next() {
		si, err := scanSecurityIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan incident row: %w", err)
		}
		incident := &Incident{
			IncidentID:  si.ID,
			Type:        string(si.Type),
			Severity:    string(si.Severity),
			Description: si.Description,
			Status:      string(si.Status),
		}
		if !si.DiscoveredAt.IsZero() {
			incident.DetectedAt = si.DiscoveredAt.Format(time.RFC3339)
		}
		if !si.ResolvedAt.IsZero() {
			incident.ResolvedAt = si.ResolvedAt.Format(time.RFC3339)
		}
		incidents = append(incidents, incident)
	}
	return incidents, rows.Err()
}

func nullableTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}
