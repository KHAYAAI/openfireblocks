package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

// GetNativeTransactions reads signing.transactions (created by
// api-gateway's PostgresService, migration 012) for one customer/chain in
// [start, end). Amount is stored as a decimal string in the chain's native
// unit (see TransactionRecord.value in
// services/api-gateway/src/database/postgres.service.ts); parsed here into
// a *big.Int since Postgres has no arbitrary-precision integer type that
// SQL SUM could use directly on a VARCHAR column.
func (p *PostgresDB) GetNativeTransactions(ctx context.Context, customerID, chain string, start, end time.Time) ([]NativeTransaction, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT request_id, chain, amount, created_at
		FROM signing.transactions
		WHERE customer_id = $1::uuid AND chain = $2 AND created_at >= $3 AND created_at < $4
		ORDER BY created_at
	`, customerID, chain, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query signing.transactions: %w", err)
	}
	defer rows.Close()

	var txs []NativeTransaction
	for rows.Next() {
		var requestID, txChain, amountStr string
		var createdAt time.Time
		if err := rows.Scan(&requestID, &txChain, &amountStr, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan signing.transactions row: %w", err)
		}
		amount, ok := new(big.Int).SetString(amountStr, 10)
		if !ok {
			// A non-numeric amount shouldn't be silently skipped from a
			// regulatory aggregate -- that's exactly the kind of gap that
			// makes a CTR undercount real activity.
			return nil, fmt.Errorf("transaction %s has non-numeric amount %q, cannot include in aggregate", requestID, amountStr)
		}
		txs = append(txs, NativeTransaction{
			RequestID: requestID,
			Chain:     txChain,
			Amount:    amount,
			CreatedAt: createdAt,
		})
	}
	return txs, rows.Err()
}

const regulatoryFilingColumns = `filing_id, filing_type, customer_id, related_transaction_ids, chain,
	aggregate_amount_native, aggregate_amount_usd, usd_conversion_rate, threshold_usd,
	detection_method, narrative, status, detected_at, filing_deadline, filed_at, filed_by, confirmation_number`

func (p *PostgresDB) CreateRegulatoryFiling(ctx context.Context, f *RegulatoryFiling) error {
	relatedIDs, err := json.Marshal(f.RelatedTransactionIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal related transaction ids: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO regulatory_filings (`+regulatoryFilingColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, f.ID, f.FilingType, f.CustomerID, relatedIDs, f.Chain,
		f.AggregateAmountNative, f.AggregateAmountUSD, f.USDConversionRate, f.ThresholdUSD,
		f.DetectionMethod, nullString(f.Narrative), f.Status, f.DetectedAt, f.FilingDeadline,
		nullableTime(f.FiledAt), nullString(f.FiledBy), nullString(f.ConfirmationNumber))
	if err != nil {
		return fmt.Errorf("failed to insert regulatory filing: %w", err)
	}
	return nil
}

func scanRegulatoryFiling(row interface {
	Scan(dest ...interface{}) error
}) (*RegulatoryFiling, error) {
	var f RegulatoryFiling
	var relatedIDsRaw []byte
	var aggregateUSD, usdRate sql.NullFloat64
	var narrative, filedBy, confirmationNumber sql.NullString
	var filedAt sql.NullTime

	if err := row.Scan(
		&f.ID, &f.FilingType, &f.CustomerID, &relatedIDsRaw, &f.Chain,
		&f.AggregateAmountNative, &aggregateUSD, &usdRate, &f.ThresholdUSD,
		&f.DetectionMethod, &narrative, &f.Status, &f.DetectedAt, &f.FilingDeadline,
		&filedAt, &filedBy, &confirmationNumber,
	); err != nil {
		return nil, err
	}

	if len(relatedIDsRaw) > 0 {
		_ = json.Unmarshal(relatedIDsRaw, &f.RelatedTransactionIDs)
	}
	if aggregateUSD.Valid {
		f.AggregateAmountUSD = &aggregateUSD.Float64
	}
	if usdRate.Valid {
		f.USDConversionRate = &usdRate.Float64
	}
	f.Narrative, f.FiledBy, f.ConfirmationNumber = narrative.String, filedBy.String, confirmationNumber.String
	f.FiledAt = filedAt.Time

	return &f, nil
}

func (p *PostgresDB) GetRegulatoryFiling(ctx context.Context, filingID string) (*RegulatoryFiling, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+regulatoryFilingColumns+` FROM regulatory_filings WHERE filing_id = $1`, filingID)
	f, err := scanRegulatoryFiling(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("regulatory filing %s not found", filingID)
		}
		return nil, fmt.Errorf("failed to query regulatory filing: %w", err)
	}
	return f, nil
}

func (p *PostgresDB) UpdateRegulatoryFiling(ctx context.Context, f *RegulatoryFiling) error {
	res, err := p.db.ExecContext(ctx, `
		UPDATE regulatory_filings SET status = $2, filed_at = $3, filed_by = $4, confirmation_number = $5, updated_at = NOW()
		WHERE filing_id = $1
	`, f.ID, f.Status, nullableTime(f.FiledAt), nullString(f.FiledBy), nullString(f.ConfirmationNumber))
	if err != nil {
		return fmt.Errorf("failed to update regulatory filing: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("regulatory filing %s not found", f.ID)
	}
	return nil
}

func (p *PostgresDB) ListRegulatoryFilingsByStatus(ctx context.Context, status string) ([]RegulatoryFiling, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+regulatoryFilingColumns+` FROM regulatory_filings WHERE status = $1 ORDER BY filing_deadline`, status)
	if err != nil {
		return nil, fmt.Errorf("failed to query regulatory filings by status: %w", err)
	}
	defer rows.Close()
	var filings []RegulatoryFiling
	for rows.Next() {
		f, err := scanRegulatoryFiling(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan regulatory filing row: %w", err)
		}
		filings = append(filings, *f)
	}
	return filings, rows.Err()
}

// ListOverdueFilings returns filings past their deadline and not yet
// filed -- the query a compliance dashboard/alert would run.
func (p *PostgresDB) ListOverdueFilings(ctx context.Context) ([]RegulatoryFiling, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+regulatoryFilingColumns+` FROM regulatory_filings
		WHERE status != 'filed' AND status != 'not_required' AND filing_deadline < NOW()
		ORDER BY filing_deadline
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query overdue filings: %w", err)
	}
	defer rows.Close()
	var filings []RegulatoryFiling
	for rows.Next() {
		f, err := scanRegulatoryFiling(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan regulatory filing row: %w", err)
		}
		filings = append(filings, *f)
	}
	return filings, rows.Err()
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
