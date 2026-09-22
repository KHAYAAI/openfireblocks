package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// HTTP handlers for ComplianceMonitor. Same plain net/http + query-param-for-ID
// style as audit_handlers.go / incident_handlers.go.

func (c *ComplianceMonitor) HandleRecordMetric(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var metric ComplianceMetric
	if err := json.NewDecoder(r.Body).Decode(&metric); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := c.RecordMetric(r.Context(), &metric); err != nil {
		http.Error(w, fmt.Sprintf("failed to record metric: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metric)
}

func (c *ComplianceMonitor) HandleGetMetricsByCategory(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	if category == "" {
		http.Error(w, "category query param required", http.StatusBadRequest)
		return
	}
	metrics, err := c.GetMetricsByCategory(r.Context(), category)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get metrics: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

func (c *ComplianceMonitor) HandleGetAlerts(w http.ResponseWriter, r *http.Request) {
	alerts, err := c.GetAlerts(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get alerts: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alerts)
}

func (c *ComplianceMonitor) HandleAcknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alertID := r.URL.Query().Get("alert_id")
	if alertID == "" {
		http.Error(w, "alert_id query param required", http.StatusBadRequest)
		return
	}
	if err := c.AcknowledgeAlert(r.Context(), alertID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ComplianceMonitor) HandleResolveAlert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	alertID := r.URL.Query().Get("alert_id")
	if alertID == "" {
		http.Error(w, "alert_id query param required", http.StatusBadRequest)
		return
	}
	if err := c.ResolveAlert(r.Context(), alertID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ComplianceMonitor) HandleGenerateDashboard(w http.ResponseWriter, r *http.Request) {
	dashboard, err := c.GenerateDashboard(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to generate dashboard: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dashboard)
}
