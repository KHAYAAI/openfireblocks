package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

// PostgresDB holds two connections at two different privilege levels,
// mirroring services/policy/db.go's split (the reference implementation
// for this pattern -- see docs/security/audit-checklist.md):
//
//   - admin (app_admin, BYPASSRLS): used ONLY to resolve which customer a
//     settlement_id/signing_id belongs to -- unavoidably privileged, since
//     RLS can't scope a query to a tenant that isn't known yet.
//   - tenant (app, RLS-enforced): used for every actual read/write of
//     settlement data, wrapped in withTenant so migration 011's row-level
//     security policies actually apply.
type PostgresDB struct {
	admin  *sql.DB
	tenant *sql.DB
}

func NewPostgresDB() (*PostgresDB, error) {
	adminDSN := os.Getenv("DATABASE_URL")
	if adminDSN == "" {
		adminDSN = "postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}
	tenantDSN := os.Getenv("DATABASE_TENANT_URL")
	if tenantDSN == "" {
		tenantDSN = "postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}

	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect admin pool: %w", err)
	}
	if err := admin.Ping(); err != nil {
		return nil, fmt.Errorf("admin pool ping failed: %w", err)
	}

	tenant, err := sql.Open("postgres", tenantDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect tenant pool: %w", err)
	}
	if err := tenant.Ping(); err != nil {
		return nil, fmt.Errorf("tenant pool ping failed: %w", err)
	}

	log.Printf("settlement service connected to PostgreSQL (admin + RLS-scoped tenant pools)")
	return &PostgresDB{admin: admin, tenant: tenant}, nil
}

func (p *PostgresDB) Close() error {
	tenantErr := p.tenant.Close()
	adminErr := p.admin.Close()
	if tenantErr != nil {
		return tenantErr
	}
	return adminErr
}

// withTenant runs fn inside a transaction on the RLS-scoped tenant pool
// with app.current_customer_id set via set_config() for that
// transaction's duration -- see services/policy/db.go's withTenant for
// the full rationale (SET LOCAL doesn't accept bind parameters).
func (p *PostgresDB) withTenant(ctx context.Context, customerID string, fn func(tx *sql.Tx) error) error {
	tx, err := p.tenant.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tenant transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.current_customer_id', $1, true)`, customerID); err != nil {
		return fmt.Errorf("failed to set tenant context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// resolveCustomerIDForSigningRequest looks up which customer a signing
// request belongs to. Runs on the admin pool -- see the type doc comment.
func (p *PostgresDB) resolveCustomerIDForSigningRequest(ctx context.Context, signingID string) (string, error) {
	var customerID sql.NullString
	err := p.admin.QueryRowContext(ctx, `SELECT customer_id FROM signing_requests WHERE request_id = $1::uuid`, signingID).Scan(&customerID)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("failed to resolve customer for signing request %s: %w", signingID, err)
	}
	return customerID.String, nil
}

// resolveCustomerIDForSettlement is resolveCustomerIDForSigningRequest's
// counterpart for operations addressed by settlement_id.
func (p *PostgresDB) resolveCustomerIDForSettlement(ctx context.Context, settlementID string) (string, error) {
	var customerID sql.NullString
	err := p.admin.QueryRowContext(ctx, `SELECT customer_id FROM settlements WHERE settlement_id = $1::uuid`, settlementID).Scan(&customerID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("settlement %s not found", settlementID)
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve customer for settlement %s: %w", settlementID, err)
	}
	return customerID.String, nil
}

func (p *PostgresDB) CreateSettlement(ctx context.Context, s *Settlement) error {
	// customer_id isn't part of the Settlement API surface -- derived from
	// the signing_requests row the settlement is for, which is the actual
	// source of truth for who owns this transaction. Left as NULL (the
	// column is nullable) rather than failing the whole settlement if the
	// signing_id doesn't match a known row, since settlement is not the
	// system of record for that relationship -- and a settlement with no
	// resolvable customer is inserted via the admin pool directly (no
	// tenant to scope a set_config() call to), which is the one case
	// left un-RLS'd here, matching the same fail-open-to-NULL choice the
	// original code made rather than silently dropping the settlement
	// record entirely.
	customerID, err := p.resolveCustomerIDForSigningRequest(ctx, s.SigningID)
	if err != nil {
		return err
	}

	insert := `
		INSERT INTO settlements (settlement_id, signing_id, customer_id, blockchain, transaction_hash, status, gas_used, broadcasted_at, error_message, created_at)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	args := []interface{}{s.SettlementID, s.SigningID, nullString(customerID), s.Blockchain, s.TransactionHash, s.Status, nullZeroUint64(s.GasUsed), s.BroadcastedAt, s.ErrorMessage, s.CreatedAt}

	if customerID == "" {
		_, err := p.admin.ExecContext(ctx, insert, args...)
		if err != nil {
			return fmt.Errorf("failed to insert settlement: %w", err)
		}
		return nil
	}

	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, insert, args...); err != nil {
			return fmt.Errorf("failed to insert settlement: %w", err)
		}
		return nil
	})
}

func (p *PostgresDB) UpdateSettlement(ctx context.Context, s *Settlement) error {
	customerID, err := p.resolveCustomerIDForSettlement(ctx, s.SettlementID)
	if err != nil {
		return err
	}

	return p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
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
	})
}

func (p *PostgresDB) GetSettlement(ctx context.Context, settlementID string) (*Settlement, error) {
	customerID, err := p.resolveCustomerIDForSettlement(ctx, settlementID)
	if err != nil {
		return nil, err
	}

	var s Settlement
	var txHash, errMsg sql.NullString
	var gasUsed, confirmTime sql.NullInt64
	err = p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT settlement_id, COALESCE(signing_id::text, ''), blockchain, transaction_hash, status,
			       gas_used, confirmation_time_seconds, broadcasted_at, confirmed_at, error_message, created_at
			FROM settlements WHERE settlement_id = $1::uuid
		`, settlementID)
		return row.Scan(&s.SettlementID, &s.SigningID, &s.Blockchain, &txHash, &s.Status,
			&gasUsed, &confirmTime, &s.BroadcastedAt, &s.ConfirmedAt, &errMsg, &s.CreatedAt)
	})
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("settlement %s not found", settlementID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query settlement: %w", err)
	}
	s.TransactionHash = txHash.String
	s.ErrorMessage = errMsg.String
	s.GasUsed = uint64(gasUsed.Int64)
	s.ConfirmationTime = confirmTime.Int64
	return &s, nil
}

func (p *PostgresDB) ListSettlements(ctx context.Context, customerID string) ([]*Settlement, error) {
	var settlements []*Settlement
	err := p.withTenant(ctx, customerID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT settlement_id, COALESCE(signing_id::text, ''), blockchain, transaction_hash, status,
			       gas_used, confirmation_time_seconds, broadcasted_at, confirmed_at, error_message, created_at
			FROM settlements WHERE customer_id = $1::uuid
			ORDER BY created_at DESC
		`, customerID)
		if err != nil {
			return fmt.Errorf("failed to query settlements: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var s Settlement
			var txHash, errMsg sql.NullString
			var gasUsed, confirmTime sql.NullInt64
			if err := rows.Scan(&s.SettlementID, &s.SigningID, &s.Blockchain, &txHash, &s.Status,
				&gasUsed, &confirmTime, &s.BroadcastedAt, &s.ConfirmedAt, &errMsg, &s.CreatedAt); err != nil {
				return fmt.Errorf("failed to scan settlement row: %w", err)
			}
			s.TransactionHash = txHash.String
			s.ErrorMessage = errMsg.String
			s.GasUsed = uint64(gasUsed.Int64)
			s.ConfirmationTime = confirmTime.Int64
			settlements = append(settlements, &s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return settlements, nil
}

func nullZeroUint64(v uint64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func nullString(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}
