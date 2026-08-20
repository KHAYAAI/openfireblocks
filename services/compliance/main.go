package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	db, err := NewPostgresDB()
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	onfidoAPIKey := os.Getenv("ONFIDO_API_KEY")
	onfido := NewOnfidoService(onfidoAPIKey, db)
	kycaml := NewKYCAMLService(db)
	reports := NewComplianceReportService(db)
	audits := NewAuditManager(db)
	incidents := NewIncidentManager(db)
	monitor := NewComplianceMonitor(db)

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"compliance"}`))
	})

	mux.HandleFunc("/v1/kyc/onfido/start", onfido.HandleKYCStart)
	mux.HandleFunc("/v1/kyc/onfido/status", onfido.HandleKYCStatus)

	mux.HandleFunc("/v1/kyc/verify", kycaml.HandleKYCVerification)
	mux.HandleFunc("/v1/kyc/status", kycaml.HandleVerificationStatus)
	mux.HandleFunc("/v1/aml/verify-transaction", kycaml.HandleTransactionVerification)

	mux.HandleFunc("/v1/reports/audit", reports.HandleGenerateAuditReport)
	mux.HandleFunc("/v1/reports/audit-logs", reports.HandleGetAuditLogs)

	mux.HandleFunc("/v1/audits/plan", audits.HandlePlanAudit)
	mux.HandleFunc("/v1/audits/get", audits.HandleGetAudit)
	mux.HandleFunc("/v1/audits/list", audits.HandleListAuditsByStatus)
	mux.HandleFunc("/v1/audits/start", audits.HandleStartAudit)
	mux.HandleFunc("/v1/audits/complete", audits.HandleCompleteAudit)
	mux.HandleFunc("/v1/audits/findings/add", audits.HandleAddFinding)
	mux.HandleFunc("/v1/audits/findings/open", audits.HandleListOpenFindings)
	mux.HandleFunc("/v1/audits/findings/status", audits.HandleUpdateFindingStatus)
	mux.HandleFunc("/v1/audits/report", audits.HandleGenerateAuditReport)

	mux.HandleFunc("/v1/incidents/report", incidents.HandleReportIncident)
	mux.HandleFunc("/v1/incidents/get", incidents.HandleGetIncident)
	mux.HandleFunc("/v1/incidents/list", incidents.HandleListIncidents)
	mux.HandleFunc("/v1/incidents/status", incidents.HandleUpdateIncidentStatus)
	mux.HandleFunc("/v1/incidents/acknowledge", incidents.HandleAcknowledgeIncident)
	mux.HandleFunc("/v1/incidents/metrics", incidents.HandleGetIncidentMetrics)

	mux.HandleFunc("/v1/compliance/metrics/record", monitor.HandleRecordMetric)
	mux.HandleFunc("/v1/compliance/metrics/category", monitor.HandleGetMetricsByCategory)
	mux.HandleFunc("/v1/compliance/alerts", monitor.HandleGetAlerts)
	mux.HandleFunc("/v1/compliance/alerts/acknowledge", monitor.HandleAcknowledgeAlert)
	mux.HandleFunc("/v1/compliance/alerts/resolve", monitor.HandleResolveAlert)
	mux.HandleFunc("/v1/compliance/dashboard", monitor.HandleGenerateDashboard)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("compliance service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
