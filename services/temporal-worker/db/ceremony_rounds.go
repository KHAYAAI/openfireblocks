package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CeremonyRoundStore handles persistence of DKG round state.
type CeremonyRoundStore struct {
	db *sql.DB
}

// NewCeremonyRoundStore creates a new ceremony round store.
func NewCeremonyRoundStore(db *sql.DB) *CeremonyRoundStore {
	return &CeremonyRoundStore{db: db}
}

// CreateRound creates a new round record in the database.
func (crs *CeremonyRoundStore) CreateRound(ctx context.Context, ceremonyId string, roundNum int) (string, error) {
	query := `
		INSERT INTO ceremony_rounds (ceremony_id, round_number, status)
		VALUES ($1, $2, $3)
		RETURNING id
	`

	var id string
	err := crs.db.QueryRowContext(ctx, query, ceremonyId, roundNum, "pending").Scan(&id)
	if err != nil {
		return "", fmt.Errorf("failed to create round: %w", err)
	}

	return id, nil
}

// GetRound retrieves a round by ID.
func (crs *CeremonyRoundStore) GetRound(ctx context.Context, id string) (map[string]interface{}, error) {
	query := `
		SELECT id, ceremony_id, round_number, status, started_at, completed_at, failed_at, error_message
		FROM ceremony_rounds
		WHERE id = $1
	`

	var ceremonyId string
	var roundNum int
	var status string
	var startedAt, completedAt, failedAt sql.NullTime
	var errorMsg sql.NullString

	err := crs.db.QueryRowContext(ctx, query, id).Scan(
		&id, &ceremonyId, &roundNum, &status, &startedAt, &completedAt, &failedAt, &errorMsg,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("round not found: %s", id)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get round: %w", err)
	}

	result := map[string]interface{}{
		"id":           id,
		"ceremonyId":   ceremonyId,
		"roundNum":     roundNum,
		"status":       status,
		"errorMessage": errorMsg.String,
	}

	if startedAt.Valid {
		result["startedAt"] = startedAt.Time
	}
	if completedAt.Valid {
		result["completedAt"] = completedAt.Time
	}
	if failedAt.Valid {
		result["failedAt"] = failedAt.Time
	}

	return result, nil
}

// StartRound marks a round as in_progress.
func (crs *CeremonyRoundStore) StartRound(ctx context.Context, ceremonyId string, roundNum int) error {
	query := `
		UPDATE ceremony_rounds
		SET status = $1, started_at = NOW()
		WHERE ceremony_id = $2 AND round_number = $3
	`

	result, err := crs.db.ExecContext(ctx, query, "in_progress", ceremonyId, roundNum)
	if err != nil {
		return fmt.Errorf("failed to start round: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("round not found: ceremony=%s, round=%d", ceremonyId, roundNum)
	}

	return nil
}

// CompleteRound marks a round as completed.
func (crs *CeremonyRoundStore) CompleteRound(ctx context.Context, ceremonyId string, roundNum int) error {
	query := `
		UPDATE ceremony_rounds
		SET status = $1, completed_at = NOW()
		WHERE ceremony_id = $2 AND round_number = $3
	`

	_, err := crs.db.ExecContext(ctx, query, "completed", ceremonyId, roundNum)
	if err != nil {
		return fmt.Errorf("failed to complete round: %w", err)
	}

	return nil
}

// FailRound marks a round as failed with an error message.
func (crs *CeremonyRoundStore) FailRound(ctx context.Context, ceremonyId string, roundNum int, errorMsg string) error {
	query := `
		UPDATE ceremony_rounds
		SET status = $1, failed_at = NOW(), error_message = $4
		WHERE ceremony_id = $2 AND round_number = $3
	`

	_, err := crs.db.ExecContext(ctx, query, "failed", ceremonyId, roundNum, errorMsg)
	if err != nil {
		return fmt.Errorf("failed to mark round as failed: %w", err)
	}

	return nil
}

// GetRoundsByCeremony retrieves all rounds for a ceremony.
func (crs *CeremonyRoundStore) GetRoundsByCeremony(ctx context.Context, ceremonyId string) ([]map[string]interface{}, error) {
	query := `
		SELECT id, ceremony_id, round_number, status, started_at, completed_at, failed_at, error_message
		FROM ceremony_rounds
		WHERE ceremony_id = $1
		ORDER BY round_number ASC
	`

	rows, err := crs.db.QueryContext(ctx, query, ceremonyId)
	if err != nil {
		return nil, fmt.Errorf("failed to query rounds: %w", err)
	}
	defer rows.Close()

	var rounds []map[string]interface{}
	for rows.Next() {
		var id, status string
		var ceremonyId string
		var roundNum int
		var startedAt, completedAt, failedAt sql.NullTime
		var errorMsg sql.NullString

		err := rows.Scan(&id, &ceremonyId, &roundNum, &status, &startedAt, &completedAt, &failedAt, &errorMsg)
		if err != nil {
			return nil, fmt.Errorf("failed to scan round: %w", err)
		}

		round := map[string]interface{}{
			"id":        id,
			"roundNum":  roundNum,
			"status":    status,
		}

		if startedAt.Valid {
			round["startedAt"] = startedAt.Time
		}
		if completedAt.Valid {
			round["completedAt"] = completedAt.Time
		}
		if failedAt.Valid {
			round["failedAt"] = failedAt.Time
		}
		if errorMsg.Valid {
			round["errorMessage"] = errorMsg.String
		}

		rounds = append(rounds, round)
	}

	return rounds, nil
}

// GetLatestRound retrieves the most recent round for a ceremony.
func (crs *CeremonyRoundStore) GetLatestRound(ctx context.Context, ceremonyId string) (map[string]interface{}, error) {
	query := `
		SELECT id, ceremony_id, round_number, status, started_at, completed_at, failed_at, error_message
		FROM ceremony_rounds
		WHERE ceremony_id = $1
		ORDER BY round_number DESC
		LIMIT 1
	`

	var id, status string
	var roundNum int
	var startedAt, completedAt, failedAt sql.NullTime
	var errorMsg sql.NullString

	err := crs.db.QueryRowContext(ctx, query, ceremonyId).Scan(
		&id, &ceremonyId, &roundNum, &status, &startedAt, &completedAt, &failedAt, &errorMsg,
	)

	if err == sql.ErrNoRows {
		return nil, nil // No rounds yet
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get latest round: %w", err)
	}

	result := map[string]interface{}{
		"id":       id,
		"roundNum": roundNum,
		"status":   status,
	}

	if startedAt.Valid {
		result["startedAt"] = startedAt.Time
	}
	if completedAt.Valid {
		result["completedAt"] = completedAt.Time
	}
	if failedAt.Valid {
		result["failedAt"] = failedAt.Time
	}
	if errorMsg.Valid {
		result["errorMessage"] = errorMsg.String
	}

	return result, nil
}

// SaveRoundData saves round data (commitments, proofs, etc.) to the database.
type RoundDataRecord struct {
	RoundID       string
	PartyID       int
	Commitments   []byte // JSONB
	DLProof       []byte // JSONB
	PublicKey     string
	Signature     string
	SavedAt       time.Time
}

// SaveRoundData persists party data for a round.
func (crs *CeremonyRoundStore) SaveRoundData(ctx context.Context, roundId string, partyId int, data map[string]interface{}) error {
	// TODO: Create ceremony_round_data table
	// INSERT INTO ceremony_round_data (round_id, party_id, commitments, dl_proof, public_key, signature)
	// VALUES ($1, $2, $3, $4, $5, $6)

	return nil
}

// GetRoundData retrieves all party data for a round.
func (crs *CeremonyRoundStore) GetRoundData(ctx context.Context, roundId string) (map[int]map[string]interface{}, error) {
	// TODO: Query ceremony_round_data table
	return make(map[int]map[string]interface{}), nil
}
