package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

// PostgresDB handles ceremony persistence.
type PostgresDB struct {
	db *sql.DB
}

// NewPostgresDB creates a new database connection.
func NewPostgresDB() (*PostgresDB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/openfireblocks?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}

	log.Printf("connected to PostgreSQL: %s", dsn)
	return &PostgresDB{db: db}, nil
}

// CreateCeremony creates a new ceremony record in the database.
func (db *PostgresDB) CreateCeremony(ctx context.Context, ceremony *Ceremony) (string, error) {
	query := `
		INSERT INTO ceremonies (customer_id, chain_id, n, k, status, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`

	metadata := map[string]interface{}{
		"created_by": "ceremony-orchestrator",
	}

	var id string
	err := db.db.QueryRowContext(ctx, query,
		ceremony.CustomerID,
		ceremony.ChainID,
		ceremony.N,
		ceremony.K,
		ceremony.Status,
		metadata,
	).Scan(&id, &ceremony.CreatedAt)

	if err != nil {
		return "", fmt.Errorf("failed to create ceremony: %w", err)
	}

	log.Printf("created ceremony: id=%s, customer=%s, chain=%s, n=%d, k=%d", id, ceremony.CustomerID, ceremony.ChainID, ceremony.N, ceremony.K)
	return id, nil
}

// GetCeremony retrieves a ceremony by ID.
func (db *PostgresDB) GetCeremony(ctx context.Context, id string) (*Ceremony, error) {
	query := `
		SELECT id, customer_id, chain_id, n, k, status, threshold_address, threshold_public_key,
		       created_at, started_at, completed_at, failed_at, error_message, metadata
		FROM ceremonies
		WHERE id = $1
	`

	ceremony := &Ceremony{}
	err := db.db.QueryRowContext(ctx, query, id).Scan(
		&ceremony.ID,
		&ceremony.CustomerID,
		&ceremony.ChainID,
		&ceremony.N,
		&ceremony.K,
		&ceremony.Status,
		&ceremony.ThresholdAddress,
		&ceremony.ThresholdPubKey,
		&ceremony.CreatedAt,
		&ceremony.StartedAt,
		&ceremony.CompletedAt,
		&ceremony.FailedAt,
		&ceremony.ErrorMessage,
		&ceremony.Metadata,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("ceremony not found: %s", id)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get ceremony: %w", err)
	}

	// Load parties
	parties, err := db.GetCeremonyParties(ctx, id)
	if err == nil {
		ceremony.Parties = parties
	}

	return ceremony, nil
}

// GetCeremonyParties retrieves all parties in a ceremony.
func (db *PostgresDB) GetCeremonyParties(ctx context.Context, ceremonyId string) ([]CeremonyParty, error) {
	query := `
		SELECT id, ceremony_id, party_id, party_endpoint, status, public_key,
		       key_share_sealed_at, joined_at, failed_at, error_message, metadata
		FROM ceremony_parties
		WHERE ceremony_id = $1
		ORDER BY party_id
	`

	rows, err := db.db.QueryContext(ctx, query, ceremonyId)
	if err != nil {
		return nil, fmt.Errorf("failed to query ceremony parties: %w", err)
	}
	defer rows.Close()

	var parties []CeremonyParty
	for rows.Next() {
		party := CeremonyParty{}
		err := rows.Scan(
			&party.ID,
			&party.CeremonyID,
			&party.PartyID,
			&party.PartyEndpoint,
			&party.Status,
			&party.PublicKey,
			&party.KeyShareSealedAt,
			&party.JoinedAt,
			&party.FailedAt,
			&party.ErrorMessage,
			&party.Metadata,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan party: %w", err)
		}
		parties = append(parties, party)
	}

	return parties, nil
}

// RegisterParty registers a new party in a ceremony.
func (db *PostgresDB) RegisterParty(ctx context.Context, ceremonyId string, partyId int, endpoint string) error {
	query := `
		INSERT INTO ceremony_parties (ceremony_id, party_id, party_endpoint, status, joined_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (ceremony_id, party_id) DO UPDATE
		SET status = $4, party_endpoint = $3
	`

	_, err := db.db.ExecContext(ctx, query, ceremonyId, partyId, endpoint, "pending")
	if err != nil {
		return fmt.Errorf("failed to register party: %w", err)
	}

	log.Printf("registered party: ceremony=%s, party=%d, endpoint=%s", ceremonyId, partyId, endpoint)
	return nil
}

// UpdateCeremonyStatus updates the ceremony status.
func (db *PostgresDB) UpdateCeremonyStatus(ctx context.Context, ceremonyId string, status string) error {
	query := `
		UPDATE ceremonies
		SET status = $1
		WHERE id = $2
	`

	_, err := db.db.ExecContext(ctx, query, status, ceremonyId)
	if err != nil {
		return fmt.Errorf("failed to update ceremony status: %w", err)
	}

	return nil
}

// UpdateCeremonyCompletion marks a ceremony as completed with threshold address.
func (db *PostgresDB) UpdateCeremonyCompletion(ctx context.Context, ceremonyId string, thresholdAddr, thresholdPubKey string) error {
	query := `
		UPDATE ceremonies
		SET status = $1, completed_at = NOW(), threshold_address = $3, threshold_public_key = $4
		WHERE id = $2
	`

	_, err := db.db.ExecContext(ctx, query, "completed", ceremonyId, thresholdAddr, thresholdPubKey)
	if err != nil {
		return fmt.Errorf("failed to mark ceremony as completed: %w", err)
	}

	return nil
}

// UpdateCeremonyFailure marks a ceremony as failed with error message.
func (db *PostgresDB) UpdateCeremonyFailure(ctx context.Context, ceremonyId string, errorMsg string) error {
	query := `
		UPDATE ceremonies
		SET status = $1, failed_at = NOW(), error_message = $3
		WHERE id = $2
	`

	_, err := db.db.ExecContext(ctx, query, "failed", ceremonyId, errorMsg)
	if err != nil {
		return fmt.Errorf("failed to mark ceremony as failed: %w", err)
	}

	return nil
}

// ListCeremonies lists all ceremonies for a customer.
func (db *PostgresDB) ListCeremonies(ctx context.Context, customerId string, limit int) ([]Ceremony, error) {
	query := `
		SELECT id, customer_id, chain_id, n, k, status, threshold_address,
		       created_at, started_at, completed_at, failed_at
		FROM ceremonies
		WHERE customer_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := db.db.QueryContext(ctx, query, customerId, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list ceremonies: %w", err)
	}
	defer rows.Close()

	var ceremonies []Ceremony
	for rows.Next() {
		ceremony := Ceremony{}
		err := rows.Scan(
			&ceremony.ID,
			&ceremony.CustomerID,
			&ceremony.ChainID,
			&ceremony.N,
			&ceremony.K,
			&ceremony.Status,
			&ceremony.ThresholdAddress,
			&ceremony.CreatedAt,
			&ceremony.StartedAt,
			&ceremony.CompletedAt,
			&ceremony.FailedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan ceremony: %w", err)
		}
		ceremonies = append(ceremonies, ceremony)
	}

	return ceremonies, nil
}

// Close closes the database connection.
func (db *PostgresDB) Close() error {
	return db.db.Close()
}
