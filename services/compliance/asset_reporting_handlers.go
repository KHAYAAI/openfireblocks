package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"
)

// EvaluateAssetThresholds aggregates one customer's day across every asset
// they moved, in every currency, and reports which totals are over their
// threshold.
//
// The successor to EvaluateCTR, which is kept because it is what the
// native-only path uses and its behaviour is pinned by tests. The
// difference is what gets summed: EvaluateCTR reads the native value
// attached to each transaction, which is zero for every stablecoin
// transfer, so a day of token activity aggregated to nothing and no
// filing was ever raised.
func (r *RegulatoryReportingService) EvaluateAssetThresholds(
	ctx context.Context,
	customerID, chain string,
	day time.Time,
	usdPerNativeUnit *big.Float,
) (*AssetAggregation, error) {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)

	txs, err := r.db.GetAssetTransactions(ctx, customerID, chain, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("failed to load transactions for threshold evaluation: %w", err)
	}

	agg := AggregateByCurrency(txs, usdPerNativeUnit)
	agg.CustomerID = customerID
	agg.Chain = chain
	agg.Day = dayStart
	return agg, nil
}

// GenerateFilingsFor persists a draft filing for every currency over its
// threshold.
//
// One filing per currency, not one per day. A customer who moved both
// dollars and rand over their respective thresholds owes two reports, to
// two regulators, in two currencies -- and merging them into a single
// record would produce a document neither regulator can accept.
func (r *RegulatoryReportingService) GenerateFilingsFor(
	ctx context.Context,
	agg *AssetAggregation,
) ([]*RegulatoryFiling, error) {
	var filings []*RegulatoryFiling
	now := time.Now()

	for _, c := range agg.Currencies {
		if !c.OverThreshold {
			continue
		}
		total := c.Total
		filing := &RegulatoryFiling{
			ID:                    fmt.Sprintf("ctr-%s-%d", c.Currency, now.UnixNano()),
			FilingType:            "CTR",
			CustomerID:            agg.CustomerID,
			RelatedTransactionIDs: c.TransactionIDs,
			Chain:                 agg.Chain,
			// The per-asset breakdown rather than a native figure: for a
			// stablecoin day the native aggregate is zero, and writing
			// that into a filing would describe the activity as nothing.
			AggregateAmountNative: fmt.Sprintf("%v", c.ByAsset),
			AggregateAmountUSD:    &total,
			ThresholdUSD:          c.Threshold,
			DetectionMethod:       "asset_threshold_" + c.Currency,
			Narrative:             c.Narrative(agg.CustomerID, agg.Chain, agg.Day),
			Status:                "draft",
			DetectedAt:            now,
			FilingDeadline:        now.Add(ctrFilingWindow),
		}
		if err := r.db.CreateRegulatoryFiling(ctx, filing); err != nil {
			return filings, fmt.Errorf("failed to persist %s filing: %w", c.Currency, err)
		}
		filings = append(filings, filing)
	}
	return filings, nil
}

// HandleEvaluateAssetThresholds reports a day's activity per currency.
func (r *RegulatoryReportingService) HandleEvaluateAssetThresholds(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CustomerID       string   `json:"customer_id"`
		Chain            string   `json:"chain"`
		Day              string   `json:"day"`
		USDPerNativeUnit *float64 `json:"usd_per_native_unit,omitempty"`
		GenerateFilings  bool     `json:"generate_filings"`
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

	var rate *big.Float
	if body.USDPerNativeUnit != nil {
		rate = big.NewFloat(*body.USDPerNativeUnit)
	}

	agg, err := r.EvaluateAssetThresholds(req.Context(), body.CustomerID, body.Chain, day, rate)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to evaluate thresholds: %v", err), http.StatusInternalServerError)
		return
	}

	currencies := make([]map[string]interface{}, 0, len(agg.Currencies))
	for _, c := range agg.Currencies {
		currencies = append(currencies, map[string]interface{}{
			"currency":        c.Currency,
			"total":           c.Total,
			"by_asset":        c.ByAsset,
			"transaction_ids": c.TransactionIDs,
			"threshold":       c.Threshold,
			// Distinguished from "not over": a currency nobody has set a
			// threshold for has not been cleared, it has not been judged.
			"have_threshold": c.HaveThreshold,
			"over_threshold": c.OverThreshold,
		})
	}

	response := map[string]interface{}{
		"customer_id": agg.CustomerID,
		"chain":       agg.Chain,
		"day":         agg.Day,
		"currencies":  currencies,
		// Surfaced at the top level rather than buried, because these are
		// what the totals leave out. A report is only sound if what it
		// excludes is visible.
		"undecoded_count": agg.UndecodedCount,
		"undecoded_ids":   agg.UndecodedIDs,
		"unvalued_assets": agg.UnvaluedAssets,
	}

	if body.GenerateFilings {
		filings, err := r.GenerateFilingsFor(req.Context(), agg)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to generate filings: %v", err), http.StatusInternalServerError)
			return
		}
		response["filings"] = filings
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
