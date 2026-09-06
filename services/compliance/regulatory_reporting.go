package main

import (
	"context"
	"fmt"
	"math/big"
	"time"
)

// FinCEN defaults. See migration 013's header comment: these are the
// well-known US federal defaults, not a substitute for confirming actual
// obligations (which may differ by state, by other jurisdictions this
// platform operates in, or change) with legal/compliance counsel.
const (
	DefaultCTRThresholdUSD = 10000.0
	ctrFilingWindow        = 15 * 24 * time.Hour
	sarFilingWindow        = 30 * 24 * time.Hour
)

// RegulatoryFiling is a SAR or CTR candidate/filing.
type RegulatoryFiling struct {
	ID                    string    `json:"id"`
	FilingType            string    `json:"filing_type"` // SAR, CTR
	CustomerID            string    `json:"customer_id"`
	RelatedTransactionIDs []string  `json:"related_transaction_ids"`
	Chain                 string    `json:"chain"`
	AggregateAmountNative string    `json:"aggregate_amount_native"` // decimal string, native unit (wei, sats, ...)
	AggregateAmountUSD    *float64  `json:"aggregate_amount_usd,omitempty"`
	USDConversionRate     *float64  `json:"usd_conversion_rate,omitempty"`
	ThresholdUSD          float64   `json:"threshold_usd"`
	DetectionMethod       string    `json:"detection_method"`
	Narrative             string    `json:"narrative,omitempty"`
	Status                string    `json:"status"`
	DetectedAt            time.Time `json:"detected_at"`
	FilingDeadline        time.Time `json:"filing_deadline"`
	FiledAt               time.Time `json:"filed_at,omitempty"`
	FiledBy               string    `json:"filed_by,omitempty"`
	ConfirmationNumber    string    `json:"confirmation_number,omitempty"`
}

// NativeTransaction is the subset of signing.transactions this package
// aggregates over -- deliberately narrow (just what CTR/SAR math needs)
// rather than reusing api-gateway's TransactionRecord, since this package
// has no dependency on the api-gateway module.
type NativeTransaction struct {
	RequestID string
	Chain     string
	Amount    *big.Int // wei/sats/etc, parsed from signing.transactions.amount
	CreatedAt time.Time
}

// RegulatoryReportingService evaluates and generates SAR/CTR filings.
type RegulatoryReportingService struct {
	db *PostgresDB
}

func NewRegulatoryReportingService(db *PostgresDB) *RegulatoryReportingService {
	return &RegulatoryReportingService{db: db}
}

// CTREvaluation is the result of checking one customer/day/chain against
// the CTR threshold.
type CTREvaluation struct {
	CustomerID            string
	Chain                 string
	Day                   time.Time
	AggregateAmountNative *big.Int
	TransactionIDs        []string
	Evaluated             bool // false if no USD conversion rate was available
	Reason                string
	OverThreshold         bool
	AggregateAmountUSD    float64
	ThresholdUSD          float64
}

// EvaluateCTR aggregates one customer's native-chain-unit transaction
// volume for one calendar day and checks it against thresholdUSD. If
// usdPerNativeUnit is nil (no price oracle configured -- this codebase has
// none, see docs/security/vendor-risk-assessment.md), it returns
// Evaluated: false rather than fabricating a USD figure from an assumed
// rate. The native-unit aggregate is still computed and returned either
// way, since that part needs no external data.
func (r *RegulatoryReportingService) EvaluateCTR(
	ctx context.Context,
	customerID, chain string,
	day time.Time,
	thresholdUSD float64,
	usdPerNativeUnit *big.Float,
) (*CTREvaluation, error) {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)

	txs, err := r.db.GetNativeTransactions(ctx, customerID, chain, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("failed to load transactions for CTR evaluation: %w", err)
	}

	total := new(big.Int)
	ids := make([]string, 0, len(txs))
	for _, tx := range txs {
		total.Add(total, tx.Amount)
		ids = append(ids, tx.RequestID)
	}

	eval := &CTREvaluation{
		CustomerID:            customerID,
		Chain:                 chain,
		Day:                   dayStart,
		AggregateAmountNative: total,
		TransactionIDs:        ids,
		ThresholdUSD:          thresholdUSD,
	}

	if usdPerNativeUnit == nil {
		eval.Reason = "no price oracle configured: cannot convert native-unit aggregate to USD"
		return eval, nil
	}

	totalF := new(big.Float).SetInt(total)
	usdF := new(big.Float).Mul(totalF, usdPerNativeUnit)
	usd, _ := usdF.Float64()

	eval.Evaluated = true
	eval.AggregateAmountUSD = usd
	eval.OverThreshold = usd >= thresholdUSD
	return eval, nil
}

// GenerateCTR persists a CTR draft from a completed evaluation. Only
// meaningful to call when eval.Evaluated is true and eval.OverThreshold --
// callers should check that before calling this, but it's not enforced
// here so a compliance officer can also generate a draft manually for an
// evaluation the system couldn't itself complete (e.g. no price oracle).
func (r *RegulatoryReportingService) GenerateCTR(ctx context.Context, eval *CTREvaluation, rate *big.Float) (*RegulatoryFiling, error) {
	now := time.Now()
	filing := &RegulatoryFiling{
		ID:                    fmt.Sprintf("ctr-%d", now.UnixNano()),
		FilingType:            "CTR",
		CustomerID:            eval.CustomerID,
		RelatedTransactionIDs: eval.TransactionIDs,
		Chain:                 eval.Chain,
		AggregateAmountNative: eval.AggregateAmountNative.String(),
		ThresholdUSD:          eval.ThresholdUSD,
		DetectionMethod:       "ctr_threshold",
		Status:                "draft",
		DetectedAt:            now,
		FilingDeadline:        now.Add(ctrFilingWindow),
	}
	if eval.Evaluated {
		usd := eval.AggregateAmountUSD
		filing.AggregateAmountUSD = &usd
	}
	if rate != nil {
		r64, _ := rate.Float64()
		filing.USDConversionRate = &r64
	}

	if err := r.db.CreateRegulatoryFiling(ctx, filing); err != nil {
		return nil, fmt.Errorf("failed to persist CTR draft: %w", err)
	}
	return filing, nil
}

// StructuringCandidate flags a customer whose transaction pattern in the
// window looks like structuring (multiple transactions individually under
// thresholdUSD, aggregating over it) -- a heuristic that warrants human
// review, not an automatic SAR filing. This mirrors the structuring signal
// already used for the transaction-risk-assessment path in aml_kyc.go's
// countSuspiciousTransactions, but produces a full SAR-shaped draft
// instead of just a count.
type StructuringCandidate struct {
	CustomerID     string
	Chain          string
	WindowStart    time.Time
	WindowEnd      time.Time
	TransactionIDs []string
	TxCount        int
}

// DetectStructuring scans a customer's transactions in [windowStart,
// windowEnd) on one chain and flags it as a candidate if there are at
// least minTxCount transactions -- the "many small transactions" pattern
// structuring relies on. It does not attempt USD conversion (the same
// price-oracle gap as EvaluateCTR); a human reviewer supplies the
// USD-value judgment when they write the SAR narrative.
func (r *RegulatoryReportingService) DetectStructuring(
	ctx context.Context,
	customerID, chain string,
	windowStart, windowEnd time.Time,
	minTxCount int,
) (*StructuringCandidate, error) {
	txs, err := r.db.GetNativeTransactions(ctx, customerID, chain, windowStart, windowEnd)
	if err != nil {
		return nil, fmt.Errorf("failed to load transactions for structuring detection: %w", err)
	}
	if len(txs) < minTxCount {
		return nil, nil
	}

	ids := make([]string, 0, len(txs))
	for _, tx := range txs {
		ids = append(ids, tx.RequestID)
	}

	return &StructuringCandidate{
		CustomerID:     customerID,
		Chain:          chain,
		WindowStart:    windowStart,
		WindowEnd:      windowEnd,
		TransactionIDs: ids,
		TxCount:        len(txs),
	}, nil
}

// GenerateSAR persists a SAR draft. narrative is required by FinCEN Form
// 111 and by this function -- a SAR with no description of the suspicious
// activity isn't fileable, so this refuses to create a bare draft the way
// GenerateCTR's threshold-crossing case can stand on the numbers alone.
func (r *RegulatoryReportingService) GenerateSAR(
	ctx context.Context,
	candidate *StructuringCandidate,
	narrative string,
) (*RegulatoryFiling, error) {
	if narrative == "" {
		return nil, fmt.Errorf("narrative is required to generate a SAR draft")
	}

	now := time.Now()
	filing := &RegulatoryFiling{
		ID:                    fmt.Sprintf("sar-%d", now.UnixNano()),
		FilingType:            "SAR",
		CustomerID:            candidate.CustomerID,
		RelatedTransactionIDs: candidate.TransactionIDs,
		Chain:                 candidate.Chain,
		AggregateAmountNative: "0", // structuring detection doesn't sum amounts, see DetectStructuring's doc comment
		DetectionMethod:       "structuring_heuristic",
		Narrative:             narrative,
		Status:                "pending_review", // SAR always needs a human before it can move to ready_to_file
		DetectedAt:            now,
		FilingDeadline:        now.Add(sarFilingWindow),
	}

	if err := r.db.CreateRegulatoryFiling(ctx, filing); err != nil {
		return nil, fmt.Errorf("failed to persist SAR draft: %w", err)
	}
	return filing, nil
}

// MarkFiled records that a filing was actually submitted (out-of-band,
// via FinCEN's BSA E-Filing system -- nothing in this codebase files
// anything itself). confirmationNumber is FinCEN's own reference number.
func (r *RegulatoryReportingService) MarkFiled(ctx context.Context, filingID, filedBy, confirmationNumber string) error {
	filing, err := r.db.GetRegulatoryFiling(ctx, filingID)
	if err != nil {
		return err
	}
	if filing.FilingType == "SAR" && filing.Narrative == "" {
		return fmt.Errorf("cannot mark a SAR filed without a narrative")
	}
	filing.Status = "filed"
	filing.FiledAt = time.Now()
	filing.FiledBy = filedBy
	filing.ConfirmationNumber = confirmationNumber
	return r.db.UpdateRegulatoryFiling(ctx, filing)
}
