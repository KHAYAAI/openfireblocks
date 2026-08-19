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

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("compliance service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
