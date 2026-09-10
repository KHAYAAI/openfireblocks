package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HTTP handlers for AuditManager. Follow the same plain net/http +
// query-param-for-ID style as kyc_aml.go and onfido_integration.go -- this
// service has no router library, so IDs travel as query params rather than
// path segments.

func (a *AuditManager) HandlePlanAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Type           string `json:"type"`
		Scope          string `json:"scope"`
		ScheduledStart string `json:"scheduled_start"`
		ScheduledEnd   string `json:"scheduled_end"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	start, err := time.Parse(time.RFC3339, req.ScheduledStart)
	if err != nil {
		http.Error(w, "invalid scheduled_start (expected RFC3339)", http.StatusBadRequest)
		return
	}
	end, err := time.Parse(time.RFC3339, req.ScheduledEnd)
	if err != nil {
		http.Error(w, "invalid scheduled_end (expected RFC3339)", http.StatusBadRequest)
		return
	}

	audit, err := a.PlanAudit(r.Context(), AuditType(req.Type), AuditScope(req.Scope), start, end)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to plan audit: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(audit)
}

func (a *AuditManager) HandleGetAudit(w http.ResponseWriter, r *http.Request) {
	auditID := r.URL.Query().Get("audit_id")
	if auditID == "" {
		http.Error(w, "audit_id query param required", http.StatusBadRequest)
		return
	}
	audit, err := a.GetAudit(r.Context(), auditID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(audit)
}

func (a *AuditManager) HandleListAuditsByStatus(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		http.Error(w, "status query param required", http.StatusBadRequest)
		return
	}
	audits, err := a.GetAuditsByStatus(r.Context(), AuditStatus(status))
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list audits: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(audits)
}

func (a *AuditManager) HandleStartAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	auditID := r.URL.Query().Get("audit_id")
	if auditID == "" {
		http.Error(w, "audit_id query param required", http.StatusBadRequest)
		return
	}
	audit, err := a.StartAudit(r.Context(), auditID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(audit)
}

func (a *AuditManager) HandleCompleteAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	auditID := r.URL.Query().Get("audit_id")
	if auditID == "" {
		http.Error(w, "audit_id query param required", http.StatusBadRequest)
		return
	}
	audit, err := a.CompleteAudit(r.Context(), auditID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(audit)
}

func (a *AuditManager) HandleAddFinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var finding AuditFinding
	if err := json.NewDecoder(r.Body).Decode(&finding); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if finding.AuditID == "" {
		http.Error(w, "audit_id is required", http.StatusBadRequest)
		return
	}
	if err := a.AddFinding(r.Context(), &finding); err != nil {
		http.Error(w, fmt.Sprintf("failed to add finding: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(finding)
}

func (a *AuditManager) HandleListOpenFindings(w http.ResponseWriter, r *http.Request) {
	findings, err := a.ListOpenFindings(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list findings: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(findings)
}

func (a *AuditManager) HandleUpdateFindingStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		FindingID string `json:"finding_id"`
		Status    string `json:"status"`
		Notes     string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := a.UpdateFindingStatus(r.Context(), req.FindingID, req.Status, req.Notes); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *AuditManager) HandleGenerateAuditReport(w http.ResponseWriter, r *http.Request) {
	auditID := r.URL.Query().Get("audit_id")
	if auditID == "" {
		http.Error(w, "audit_id query param required", http.StatusBadRequest)
		return
	}
	report, err := a.GenerateAuditReport(r.Context(), auditID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(report))
}
