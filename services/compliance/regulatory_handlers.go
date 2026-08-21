package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"
)

// HTTP handlers for RegulatoryReportingService. Same plain net/http +
// query-param-for-ID style as audit_handlers.go / incident_handlers.go.

func (r *RegulatoryReportingService) HandleEvaluateCTR(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CustomerID       string  `json:"customer_id"`
		Chain            string  `json:"chain"`
		Day              string  `json:"day"` // RFC3339
		ThresholdUSD     float64 `json:"threshold_usd"`
		USDPerNativeUnit *string `json:"usd_per_native_unit,omitempty"` // decimal string; omit if no price oracle available
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	day, err := time.Parse(time.RFC3339, body.Day)
	if err != nil {
		http.Error(w, "invalid day (expected RFC3339)", http.StatusBadRequest)
		return
	}
	threshold := body.ThresholdUSD
	if threshold == 0 {
		threshold = DefaultCTRThresholdUSD
	}

	var rate *big.Float
	if body.USDPerNativeUnit != nil {
		parsed, ok := new(big.Float).SetString(*body.USDPerNativeUnit)
		if !ok {
			http.Error(w, "invalid usd_per_native_unit", http.StatusBadRequest)
			return
		}
		rate = parsed
	}

	eval, err := r.EvaluateCTR(req.Context(), body.CustomerID, body.Chain, day, threshold, rate)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to evaluate CTR: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"customer_id":             eval.CustomerID,
		"chain":                   eval.Chain,
		"day":                     eval.Day,
		"aggregate_amount_native": eval.AggregateAmountNative.String(),
		"transaction_ids":         eval.TransactionIDs,
		"evaluated":               eval.Evaluated,
		"reason":                  eval.Reason,
		"over_threshold":          eval.OverThreshold,
		"aggregate_amount_usd":    eval.AggregateAmountUSD,
		"threshold_usd":           eval.ThresholdUSD,
	})
}

func (r *RegulatoryReportingService) HandleGenerateCTR(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CustomerID            string   `json:"customer_id"`
		Chain                 string   `json:"chain"`
		Day                   string   `json:"day"`
		AggregateAmountNative string   `json:"aggregate_amount_native"`
		TransactionIDs        []string `json:"transaction_ids"`
		ThresholdUSD          float64  `json:"threshold_usd"`
		AggregateAmountUSD    *float64 `json:"aggregate_amount_usd,omitempty"`
		USDConversionRate     *float64 `json:"usd_conversion_rate,omitempty"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	amount, ok := new(big.Int).SetString(body.AggregateAmountNative, 10)
	if !ok {
		http.Error(w, "invalid aggregate_amount_native", http.StatusBadRequest)
		return
	}
	day, err := time.Parse(time.RFC3339, body.Day)
	if err != nil {
		http.Error(w, "invalid day (expected RFC3339)", http.StatusBadRequest)
		return
	}

	eval := &CTREvaluation{
		CustomerID:            body.CustomerID,
		Chain:                 body.Chain,
		Day:                   day,
		AggregateAmountNative: amount,
		TransactionIDs:        body.TransactionIDs,
		ThresholdUSD:          body.ThresholdUSD,
	}
	if body.AggregateAmountUSD != nil {
		eval.Evaluated = true
		eval.AggregateAmountUSD = *body.AggregateAmountUSD
		eval.OverThreshold = *body.AggregateAmountUSD >= body.ThresholdUSD
	}
	var rate *big.Float
	if body.USDConversionRate != nil {
		rate = big.NewFloat(*body.USDConversionRate)
	}

	filing, err := r.GenerateCTR(req.Context(), eval, rate)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to generate CTR: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(filing)
}

func (r *RegulatoryReportingService) HandleDetectStructuring(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CustomerID  string `json:"customer_id"`
		Chain       string `json:"chain"`
		WindowStart string `json:"window_start"`
		WindowEnd   string `json:"window_end"`
		MinTxCount  int    `json:"min_tx_count"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	start, err := time.Parse(time.RFC3339, body.WindowStart)
	if err != nil {
		http.Error(w, "invalid window_start (expected RFC3339)", http.StatusBadRequest)
		return
	}
	end, err := time.Parse(time.RFC3339, body.WindowEnd)
	if err != nil {
		http.Error(w, "invalid window_end (expected RFC3339)", http.StatusBadRequest)
		return
	}
	minTxCount := body.MinTxCount
	if minTxCount == 0 {
		minTxCount = 3
	}

	candidate, err := r.DetectStructuring(req.Context(), body.CustomerID, body.Chain, start, end, minTxCount)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to detect structuring: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if candidate == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"flagged": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"flagged": true, "candidate": candidate})
}

func (r *RegulatoryReportingService) HandleGenerateSAR(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CustomerID     string   `json:"customer_id"`
		Chain          string   `json:"chain"`
		WindowStart    string   `json:"window_start"`
		WindowEnd      string   `json:"window_end"`
		TransactionIDs []string `json:"transaction_ids"`
		Narrative      string   `json:"narrative"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	start, err := time.Parse(time.RFC3339, body.WindowStart)
	if err != nil {
		http.Error(w, "invalid window_start (expected RFC3339)", http.StatusBadRequest)
		return
	}
	end, err := time.Parse(time.RFC3339, body.WindowEnd)
	if err != nil {
		http.Error(w, "invalid window_end (expected RFC3339)", http.StatusBadRequest)
		return
	}

	candidate := &StructuringCandidate{
		CustomerID:     body.CustomerID,
		Chain:          body.Chain,
		WindowStart:    start,
		WindowEnd:      end,
		TransactionIDs: body.TransactionIDs,
		TxCount:        len(body.TransactionIDs),
	}
	filing, err := r.GenerateSAR(req.Context(), candidate, body.Narrative)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to generate SAR: %v", err), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(filing)
}

func (r *RegulatoryReportingService) HandleMarkFiled(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		FilingID           string `json:"filing_id"`
		FiledBy            string `json:"filed_by"`
		ConfirmationNumber string `json:"confirmation_number"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := r.MarkFiled(req.Context(), body.FilingID, body.FiledBy, body.ConfirmationNumber); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *RegulatoryReportingService) HandleListByStatus(w http.ResponseWriter, req *http.Request) {
	status := req.URL.Query().Get("status")
	if status == "" {
		http.Error(w, "status query param required", http.StatusBadRequest)
		return
	}
	filings, err := r.db.ListRegulatoryFilingsByStatus(req.Context(), status)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list filings: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(filings)
}

func (r *RegulatoryReportingService) HandleListOverdue(w http.ResponseWriter, req *http.Request) {
	filings, err := r.db.ListOverdueFilings(req.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list overdue filings: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(filings)
}
