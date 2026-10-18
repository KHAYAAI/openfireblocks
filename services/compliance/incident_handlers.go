package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Thresholds against which GetIncidentMetrics buckets incidents as "in
// threshold" or "out of threshold" for the /v1/incidents/metrics endpoint --
// mirrors the SOC2_CC "Incident Response Time" and SOC2_A "Mean Time to
// Recovery" targets defined in monitoring.go's NewSOC2Metrics.
const (
	defaultMTTDTarget = 15 * time.Minute
	defaultMTTRTarget = 4 * time.Hour
)

// HTTP handlers for IncidentManager. Same plain net/http + query-param-for-ID
// style as audit_handlers.go.

func (i *IncidentManager) HandleReportIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var incident SecurityIncident
	if err := json.NewDecoder(r.Body).Decode(&incident); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	reported, err := i.ReportIncident(r.Context(), &incident)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to report incident: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reported)
}

func (i *IncidentManager) HandleGetIncident(w http.ResponseWriter, r *http.Request) {
	incidentID := r.URL.Query().Get("incident_id")
	if incidentID == "" {
		http.Error(w, "incident_id query param required", http.StatusBadRequest)
		return
	}
	incident, err := i.GetIncident(r.Context(), incidentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(incident)
}

func (i *IncidentManager) HandleListIncidents(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		http.Error(w, "status query param required", http.StatusBadRequest)
		return
	}
	incidents, err := i.ListIncidents(r.Context(), IncidentStatus(status))
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list incidents: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(incidents)
}

func (i *IncidentManager) HandleUpdateIncidentStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		IncidentID string `json:"incident_id"`
		Status     string `json:"status"`
		Details    string `json:"details"`
		UpdatedBy  string `json:"updated_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := i.UpdateIncidentStatus(r.Context(), req.IncidentID, IncidentStatus(req.Status), req.Details, req.UpdatedBy); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (i *IncidentManager) HandleAcknowledgeIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		IncidentID     string `json:"incident_id"`
		AcknowledgedBy string `json:"acknowledged_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := i.AcknowledgeIncident(r.Context(), req.IncidentID, req.AcknowledgedBy); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (i *IncidentManager) HandleGetIncidentMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := i.GetIncidentMetrics(r.Context(), defaultMTTDTarget, defaultMTTRTarget)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}
