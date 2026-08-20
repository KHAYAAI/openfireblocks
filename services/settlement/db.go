package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

type PostgresDB struct {
	db *sql.DB
}

func NewPostgresDB() (*PostgresDB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}
	log.Printf("settlement service connected to PostgreSQL")
	return &PostgresDB{db: db}, nil
}

func (p *PostgresDB) Close() error { return p.db.Close() }

func (p *PostgresDB) CreateSettlement(ctx context.Context, s *Settlement) error {
	// customer_id isn't part of the Settlement API surface -- derived from
	// the signing_requests row the settlement is for, which is the actual
	// source of truth for who owns this transaction. Left as NULL (the
	// column is nullable) rather than failing the whole settlement if the
	// signing_id doesn't match a known row, since settlement is not the
	// system of record for that relationship.
	var customerID sql.NullString
	_ = p.db.QueryRowContext(ctx, `SELECT customer_id FROM signing_requests WHERE request_id = $1::uuid`, s.SigningID).Scan(&customerID)

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO settlements (settlement_id, signing_id, customer_id, blockchain, transaction_hash, status, gas_used, broadcasted_at, error_message, created_at)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8, $9, $10)
	`, s.SettlementID, s.SigningID, customerID, s.Blockchain, s.TransactionHash, s.Status, nullZeroUint64(s.GasUsed), s.BroadcastedAt, s.ErrorMessage, s.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert settlement: %w", err)
	}
	return nil
}

func (p *PostgresDB) UpdateSettlement(ctx context.Context, s *Settlement) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE settlements
		SET status = $2, confirmation_time_seconds = NULLIF($3, 0), confirmed_at = $4, error_message = $5
		WHERE settlement_id = $1::uuid
	`, s.SettlementID, s.Status, s.ConfirmationTime, s.ConfirmedAt, s.ErrorMessage)
	if err != nil {
		return fmt.Errorf("failed to update settlement: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("settlement %s not found", s.SettlementID)
	}
	return nil
}

func (p *PostgresDB) GetSettlement(ctx context.Context, settlementID string) (*Settlement, error) {
	var s Settlement
	var txHash, errMsg sql.NullString
	var gasUsed, confirmTime sql.NullInt64
	row := p.db.QueryRowContext(ctx, `
		SELECT settlement_id, COALESCE(signing_id::text, ''), blockchain, transaction_hash, status,
		       gas_used, confirmation_time_seconds, broadcasted_at, confirmed_at, error_message, created_at
		FROM settlements WHERE settlement_id = $1::uuid
	`, settlementID)
	if err := row.Scan(&s.SettlementID, &s.SigningID, &s.Blockchain, &txHash, &s.Status,
		&gasUsed, &confirmTime, &s.BroadcastedAt, &s.ConfirmedAt, &errMsg, &s.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("settlement %s not found", settlementID)
		}
		return nil, fmt.Errorf("failed to query settlement: %w", err)
	}
	s.TransactionHash = txHash.String
	s.ErrorMessage = errMsg.String
	s.GasUsed = uint64(gasUsed.Int64)
	s.ConfirmationTime = confirmTime.Int64
	return &s, nil
}

func (p *PostgresDB) ListSettlements(ctx context.Context, customerID string) ([]*Settlement, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT settlement_id, COALESCE(signing_id::text, ''), blockchain, transaction_hash, status,
		       gas_used, confirmation_time_seconds, broadcasted_at, confirmed_at, error_message, created_at
		FROM settlements WHERE customer_id = $1::uuid
		ORDER BY created_at DESC
	`, customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to query settlements: %w", err)
	}
	defer rows.Close()

	var settlements []*Settlement
	for rows.Next() {
		var s Settlement
		var txHash, errMsg sql.NullString
		var gasUsed, confirmTime sql.NullInt64
		if err := rows.Scan(&s.SettlementID, &s.SigningID, &s.Blockchain, &txHash, &s.Status,
			&gasUsed, &confirmTime, &s.BroadcastedAt, &s.ConfirmedAt, &errMsg, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan settlement row: %w", err)
		}
		s.TransactionHash = txHash.String
		s.ErrorMessage = errMsg.String
		s.GasUsed = uint64(gasUsed.Int64)
		s.ConfirmationTime = confirmTime.Int64
		settlements = append(settlements, &s)
	}
	return settlements, rows.Err()
}

func nullZeroUint64(v uint64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
