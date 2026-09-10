package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ComplianceReportService generates compliance and audit reports
type ComplianceReportService struct {
	db *PostgresDB
}

// ComplianceReport represents a generated compliance report
type ComplianceReport struct {
	ReportID    string      `json:"report_id"`
	CustomerID  string      `json:"customer_id,omitempty"`
	ReportType  string      `json:"report_type"` // audit, compliance, risk, kyc
	Period      string      `json:"period"`      // monthly, quarterly, yearly
	StartDate   time.Time   `json:"start_date"`
	EndDate     time.Time   `json:"end_date"`
	Status      string      `json:"status"` // generating, completed, failed
	Data        *ReportData `json:"data,omitempty"`
	FileURL     string      `json:"file_url,omitempty"`
	GeneratedAt time.Time   `json:"generated_at"`
	ExpiresAt   time.Time   `json:"expires_at"`
}

// ReportData contains the report data
type ReportData struct {
	Summary       *ReportSummary    `json:"summary"`
	Transactions  *TransactionStats `json:"transactions,omitempty"`
	Compliance    *ComplianceStats  `json:"compliance,omitempty"`
	Risk          *RiskStats        `json:"risk,omitempty"`
	KeyManagement *KeyStats         `json:"key_management,omitempty"`
	Users         *UserStats        `json:"users,omitempty"`
	Incidents     []*Incident       `json:"incidents,omitempty"`
	AuditLogs     []*AuditLog       `json:"audit_logs,omitempty"`
}

// ReportSummary contains high-level report summary
type ReportSummary struct {
	ReportTitle    string `json:"report_title"`
	PeriodCoverage string `json:"period_coverage"`
	GeneratedDate  string `json:"generated_date"`
	Status         string `json:"status"`
	Findings       string `json:"findings"`
}

// TransactionStats contains transaction metrics
type TransactionStats struct {
	TotalTransactions   int64   `json:"total_transactions"`
	SuccessfulTxs       int64   `json:"successful_txs"`
	FailedTxs           int64   `json:"failed_txs"`
	SuccessRate         float64 `json:"success_rate"`
	TotalVolume         string  `json:"total_volume"`
	AverageAmount       string  `json:"average_amount"`
	AverageConfirmTime  int64   `json:"average_confirmation_time_seconds"`
	PeakTransactionTime string  `json:"peak_transaction_time"`
}

// ComplianceStats contains compliance metrics
type ComplianceStats struct {
	VerifiedCustomers    int     `json:"verified_customers"`
	PendingVerifications int     `json:"pending_verifications"`
	FailedVerifications  int     `json:"failed_verifications"`
	VerificationRate     float64 `json:"verification_rate"`
	RejectedTransactions int64   `json:"rejected_transactions"`
	SanctionsMatches     int     `json:"sanctions_matches"`
	HighRiskDetected     int     `json:"high_risk_detected"`
	ComplianceScore      int     `json:"compliance_score"` // 0-100
}

// RiskStats contains risk assessment metrics
type RiskStats struct {
	TotalRiskAlerts   int     `json:"total_risk_alerts"`
	HighRiskAlerts    int     `json:"high_risk_alerts"`
	MediumRiskAlerts  int     `json:"medium_risk_alerts"`
	LowRiskAlerts     int     `json:"low_risk_alerts"`
	FalsePositiveRate float64 `json:"false_positive_rate"`
	AverageRiskScore  float64 `json:"average_risk_score"`
	MaxRiskScore      float64 `json:"max_risk_score"`
}

// KeyStats contains key management metrics
type KeyStats struct {
	TotalKeys       int     `json:"total_keys"`
	ActiveKeys      int     `json:"active_keys"`
	PendingKeys     int     `json:"pending_keys"`
	CompromisedKeys int     `json:"compromised_keys"`
	KeyRotationRate float64 `json:"key_rotation_rate"` // keys rotated/month
	AverageKeyAge   int     `json:"average_key_age_days"`
	UnusedKeys      int     `json:"unused_keys_30_days"`
}

// UserStats contains user activity metrics
type UserStats struct {
	TotalUsers          int `json:"total_users"`
	ActiveUsers         int `json:"active_users"`
	AdminUsers          int `json:"admin_users"`
	APIKeyCount         int `json:"api_key_count"`
	FailedLoginAttempts int `json:"failed_login_attempts"`
	PasswordChanges     int `json:"password_changes"`
}

// Incident represents a security incident
type Incident struct {
	IncidentID  string `json:"incident_id"`
	Type        string `json:"type"`
	Severity    string `json:"severity"` // low, medium, high, critical
	Description string `json:"description"`
	DetectedAt  string `json:"detected_at"`
	ResolvedAt  string `json:"resolved_at,omitempty"`
	Status      string `json:"status"` // open, investigating, resolved
	Notes       string `json:"notes,omitempty"`
}

// AuditLog represents an audit trail entry
type AuditLog struct {
	LogID        string    `json:"log_id"`
	CustomerID   string    `json:"customer_id,omitempty"`
	Action       string    `json:"action"`
	Actor        string    `json:"actor"`
	ResourceID   string    `json:"resource_id"`
	ResourceType string    `json:"resource_type"`
	Changes      string    `json:"changes"`
	IPAddress    string    `json:"ip_address,omitempty"`
	UserAgent    string    `json:"user_agent,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// NewComplianceReportService creates a new report service
func NewComplianceReportService(db *PostgresDB) *ComplianceReportService {
	return &ComplianceReportService{
		db: db,
	}
}

// GenerateAuditReport generates an audit report for a customer
func (s *ComplianceReportService) GenerateAuditReport(ctx context.Context, customerID string, startDate, endDate time.Time) (*ComplianceReport, error) {
	report := &ComplianceReport{
		ReportID:    uuid.New().String(),
		CustomerID:  customerID,
		ReportType:  "audit",
		Period:      "custom",
		StartDate:   startDate,
		EndDate:     endDate,
		Status:      "generating",
		GeneratedAt: time.Now(),
		ExpiresAt:   time.Now().AddDate(1, 0, 0), // 1 year expiration
	}

	// Get audit logs
	auditLogs, err := s.db.GetAuditLogs(ctx, customerID, startDate, endDate)
	if err != nil {
		log.Printf("Failed to get audit logs: %v", err)
		report.Status = "failed"
		return report, nil
	}

	// Get transaction stats
	txStats, err := s.getTransactionStats(ctx, customerID, startDate, endDate)
	if err != nil {
		log.Printf("Failed to get transaction stats: %v", err)
	}

	// Get key management stats
	keyStats, err := s.getKeyStats(ctx, customerID, startDate, endDate)
	if err != nil {
		log.Printf("Failed to get key stats: %v", err)
	}

	// Get compliance stats
	complianceStats, err := s.getComplianceStats(ctx, customerID, startDate, endDate)
	if err != nil {
		log.Printf("Failed to get compliance stats: %v", err)
	}

	findings := "No high-risk transactions or sanctions matches in this period"
	if complianceStats != nil && (complianceStats.HighRiskDetected > 0 || complianceStats.SanctionsMatches > 0) {
		findings = fmt.Sprintf("%d high-risk detection(s), %d sanctions match(es) in this period -- review required",
			complianceStats.HighRiskDetected, complianceStats.SanctionsMatches)
	}

	// Create report data
	report.Data = &ReportData{
		Summary: &ReportSummary{
			ReportTitle:    "Audit Report",
			PeriodCoverage: fmt.Sprintf("%s to %s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02")),
			GeneratedDate:  time.Now().Format("2006-01-02"),
			Status:         "completed",
			Findings:       findings,
		},
		Transactions:  txStats,
		KeyManagement: keyStats,
		Compliance:    complianceStats,
		AuditLogs:     auditLogs,
	}

	report.Status = "completed"

	// Store report
	if err := s.db.CreateComplianceReport(ctx, report); err != nil {
		log.Printf("Failed to store report: %v", err)
	}

	return report, nil
}

// GenerateComplianceReport generates a compliance report
func (s *ComplianceReportService) GenerateComplianceReport(ctx context.Context, startDate, endDate time.Time) (*ComplianceReport, error) {
	report := &ComplianceReport{
		ReportID:    uuid.New().String(),
		ReportType:  "compliance",
		Period:      "custom",
		StartDate:   startDate,
		EndDate:     endDate,
		Status:      "generating",
		GeneratedAt: time.Now(),
		ExpiresAt:   time.Now().AddDate(1, 0, 0),
	}

	// Get compliance stats across all customers
	complianceStats, err := s.db.GetGlobalComplianceStats(ctx, startDate, endDate)
	if err != nil {
		log.Printf("Failed to get compliance stats: %v", err)
		report.Status = "failed"
		return report, nil
	}

	// Get incidents
	incidents, err := s.db.GetIncidents(ctx, startDate, endDate)
	if err != nil {
		log.Printf("Failed to get incidents: %v", err)
	}

	report.Data = &ReportData{
		Summary: &ReportSummary{
			ReportTitle:    "Compliance Report",
			PeriodCoverage: fmt.Sprintf("%s to %s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02")),
			GeneratedDate:  time.Now().Format("2006-01-02"),
			Status:         "completed",
			Findings:       fmt.Sprintf("Compliance score: %d/100", complianceStats.ComplianceScore),
		},
		Compliance: complianceStats,
		Incidents:  incidents,
	}

	report.Status = "completed"

	// Store report
	if err := s.db.CreateComplianceReport(ctx, report); err != nil {
		log.Printf("Failed to store report: %v", err)
	}

	return report, nil
}

// GetAuditLogs retrieves audit logs for a customer
func (s *ComplianceReportService) GetAuditLogs(ctx context.Context, customerID string, limit int) ([]*AuditLog, error) {
	return s.db.GetRecentAuditLogs(ctx, customerID, limit)
}

// LogAuditEvent logs an audit trail event
func (s *ComplianceReportService) LogAuditEvent(ctx context.Context, log *AuditLog) error {
	log.LogID = uuid.New().String()
	log.CreatedAt = time.Now()

	if err := s.db.CreateAuditLog(ctx, log); err != nil {
		return fmt.Errorf("failed to log audit event: %w", err)
	}

	return nil
}

// Helper methods

func (s *ComplianceReportService) getTransactionStats(ctx context.Context, customerID string, startDate, endDate time.Time) (*TransactionStats, error) {
	return s.db.GetTransactionStats(ctx, customerID, startDate, endDate)
}

func (s *ComplianceReportService) getKeyStats(ctx context.Context, customerID string, startDate, endDate time.Time) (*KeyStats, error) {
	return s.db.GetKeyStats(ctx, customerID, startDate, endDate)
}

func (s *ComplianceReportService) getComplianceStats(ctx context.Context, customerID string, startDate, endDate time.Time) (*ComplianceStats, error) {
	return s.db.GetCustomerComplianceStats(ctx, customerID, startDate, endDate)
}

// HTTP Handlers

// HandleGenerateAuditReport is the HTTP handler to generate an audit report
func (s *ComplianceReportService) HandleGenerateAuditReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID := r.Header.Get("X-Customer-ID")

	if customerID == "" {
		http.Error(w, "X-Customer-ID header required", http.StatusBadRequest)
		return
	}

	startDateStr := r.URL.Query().Get("start_date")
	endDateStr := r.URL.Query().Get("end_date")

	if startDateStr == "" || endDateStr == "" {
		http.Error(w, "start_date and end_date required", http.StatusBadRequest)
		return
	}

	startDate, _ := time.Parse("2006-01-02", startDateStr)
	endDate, _ := time.Parse("2006-01-02", endDateStr)

	report, err := s.GenerateAuditReport(ctx, customerID, startDate, endDate)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate report: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

// HandleGetAuditLogs is the HTTP handler to get audit logs
func (s *ComplianceReportService) HandleGetAuditLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID := r.Header.Get("X-Customer-ID")

	if customerID == "" {
		http.Error(w, "X-Customer-ID header required", http.StatusBadRequest)
		return
	}

	logs, err := s.GetAuditLogs(ctx, customerID, 100)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch logs: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}
