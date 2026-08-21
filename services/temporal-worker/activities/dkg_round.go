package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.temporal.io/sdk/activity"

	"forge-crypto/temporal-worker/workflows"
)

// roundLogger is the minimal subset of go.temporal.io/sdk/log.Logger this
// package needs. Injected rather than pulled from ctx via activity.GetLogger
// inside every helper method, because activity.GetLogger panics outside a
// real Temporal activity context -- which made every one of these methods
// impossible to unit test directly (see dkg_round_test.go, which called them
// with a plain context.Background()).
type roundLogger interface {
	Warn(msg string, keyvals ...interface{})
}

type noopRoundLogger struct{}

func (noopRoundLogger) Warn(string, ...interface{}) {}

// DKGRoundCoordinator manages a single DKG round across multiple parties.
type DKGRoundCoordinator struct {
	httpClient *http.Client
	timeout    time.Duration
	logger     roundLogger
	// endpointBase resolves a party ID to its base URL. Defaults to the
	// production "http://party-N:7000" convention; tests override it to
	// point at httptest servers instead of hardcoding that hostname, which
	// never resolves outside production DNS.
	endpointBase func(partyId int) string
}

// NewDKGRoundCoordinator creates a new round coordinator. If
// MTLS_CERT_FILE/MTLS_KEY_FILE/MTLS_CA_FILE are all set (see mtls.go), the
// httpClient presents a client certificate and verifies party servers
// against the given CA, and endpointBase switches to https:// -- this is
// the transport DKG round data and, eventually, key shares cross, so it's
// the highest-value link in the platform for this. Any one of those three
// env vars unset (the default) falls back to plain HTTP, matching every
// existing deployment and test.
func NewDKGRoundCoordinator() (*DKGRoundCoordinator, error) {
	tlsConfig, mtlsEnabled, err := clientTLSConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("mTLS configuration error: %w", err)
	}

	httpClient := &http.Client{Timeout: 2 * time.Minute}
	scheme := "http"
	if mtlsEnabled {
		httpClient.Transport = &http.Transport{TLSClientConfig: tlsConfig}
		scheme = "https"
	}

	return &DKGRoundCoordinator{
		httpClient:   httpClient,
		timeout:      2 * time.Minute,
		logger:       noopRoundLogger{},
		endpointBase: func(partyId int) string { return fmt.Sprintf("%s://party-%d:7000", scheme, partyId) },
	}, nil
}

// ExecuteDKGRound coordinates a single DKG round (1-7) across all parties.
//
// DKG Protocol (Binance TSS-Lib):
//
//	Round 1: Each party generates random coefficients and commitments
//	Round 2: Each party sends decommitments to others
//	Round 3-7: Additional validation and key share generation
//
// For each round:
//  1. Send "start round" signal to all parties
//  2. Collect "round data" from all parties in parallel
//  3. Broadcast each party's data to all other parties
//  4. Validate commitments and proofs
//  5. Persist round state
func (a *Activities) ExecuteDKGRound(ctx context.Context, req workflows.DKGRoundRequest) (*workflows.DKGRoundResult, error) {
	logger := activity.GetLogger(ctx)

	logger.Info("executing DKG round",
		"ceremonyId", req.CeremonyID,
		"round", req.RoundNum,
		"parties", len(req.PartyIDs),
	)

	coordinator, err := NewDKGRoundCoordinator()
	if err != nil {
		return nil, fmt.Errorf("failed to create round coordinator: %w", err)
	}
	coordinator.logger = logger

	// Phase 1: Signal all parties to start this round
	logger.Info("signaling parties to start round", "round", req.RoundNum)
	if err := coordinator.SignalRoundStart(ctx, req.CeremonyID, req.RoundNum, req.PartyIDs); err != nil {
		logger.Error("failed to signal round start", "round", req.RoundNum, "error", err)
		return &workflows.DKGRoundResult{
			RoundNum: req.RoundNum,
			Status:   "failed",
			Error:    err.Error(),
		}, nil
	}

	// Phase 2: Collect round data from all parties in parallel
	logger.Info("collecting round data from parties", "round", req.RoundNum)
	partyData, err := coordinator.CollectRoundData(ctx, req.CeremonyID, req.RoundNum, req.PartyIDs)
	if err != nil {
		logger.Error("failed to collect round data", "round", req.RoundNum, "error", err)
		return &workflows.DKGRoundResult{
			RoundNum: req.RoundNum,
			Status:   "failed",
			Error:    fmt.Sprintf("collection failed: %v", err),
		}, nil
	}

	if len(partyData) < len(req.PartyIDs) {
		logger.Warn("incomplete round data",
			"round", req.RoundNum,
			"collected", len(partyData),
			"expected", len(req.PartyIDs),
		)
		missing := len(req.PartyIDs) - len(partyData)
		return &workflows.DKGRoundResult{
			RoundNum: req.RoundNum,
			Status:   "failed",
			Error:    fmt.Sprintf("%d parties failed to respond", missing),
		}, nil
	}

	// Phase 3: Broadcast each party's data to all others (fan-out)
	logger.Info("broadcasting round data to all parties", "round", req.RoundNum)
	if err := coordinator.BroadcastRoundData(ctx, req.CeremonyID, req.RoundNum, partyData); err != nil {
		logger.Error("failed to broadcast round data", "round", req.RoundNum, "error", err)
		return &workflows.DKGRoundResult{
			RoundNum: req.RoundNum,
			Status:   "failed",
			Error:    fmt.Sprintf("broadcast failed: %v", err),
		}, nil
	}

	// Phase 4: Validate commitments and proofs (basic validation)
	logger.Info("validating round data", "round", req.RoundNum)
	validationErrors := coordinator.ValidateRoundData(req.RoundNum, partyData)
	if len(validationErrors) > 0 {
		logger.Error("validation errors in round", "round", req.RoundNum, "errors", validationErrors)
		return &workflows.DKGRoundResult{
			RoundNum: req.RoundNum,
			Status:   "failed",
			Error:    fmt.Sprintf("validation failed: %v errors", len(validationErrors)),
		}, nil
	}

	// Phase 5: Extract commitments for return value
	commitments := make(map[int]string)
	for partyId, data := range partyData {
		if data.Commitments != "" {
			commitments[partyId] = data.Commitments
		}
	}

	logger.Info("DKG round completed",
		"round", req.RoundNum,
		"parties", len(partyData),
		"commitments", len(commitments),
	)

	return &workflows.DKGRoundResult{
		RoundNum:         req.RoundNum,
		Status:           "completed",
		PartyCommitments: commitments,
	}, nil
}

// RoundData represents data collected from a party for a round.
type RoundData struct {
	PartyID     int    `json:"partyId"`
	RoundNum    int    `json:"roundNum"`
	Commitments string `json:"commitments"` // Base64-encoded commitments
	DLProof     string `json:"dlProof"`     // Discrete log proof (base64)
	PublicKey   string `json:"publicKey"`   // Party's public key (hex)
	Signature   string `json:"signature"`   // Proof of possession (hex)
}

// SignalRoundStart sends a "start round" signal to all parties.
func (drc *DKGRoundCoordinator) SignalRoundStart(ctx context.Context, ceremonyId string, roundNum int, partyIds []int) error {
	signal := map[string]interface{}{
		"ceremonyId": ceremonyId,
		"roundNum":   roundNum,
		"action":     "start",
	}

	for _, partyId := range partyIds {
		// TODO: Get party endpoint from database
		// For now, use placeholder endpoint
		endpoint := drc.endpointBase(partyId) + "/round"

		payload, _ := json.Marshal(signal)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")

		// Best-effort: log failures but continue
		resp, err := drc.httpClient.Do(req)
		if err != nil {
			drc.logger.Warn("failed to signal party", "partyId", partyId, "error", err)
			continue
		}
		resp.Body.Close()
	}

	return nil
}

// CollectRoundData retrieves round data from all parties in parallel.
func (drc *DKGRoundCoordinator) CollectRoundData(ctx context.Context, ceremonyId string, roundNum int, partyIds []int) (map[int]*RoundData, error) {
	data := make(map[int]*RoundData)
	dataMu := sync.Mutex{}

	var wg sync.WaitGroup
	errChan := make(chan error, len(partyIds))

	for _, partyId := range partyIds {
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()

			// Fetch round data from party
			roundData, err := drc.FetchPartyRoundData(ctx, ceremonyId, roundNum, pid)
			if err != nil {
				drc.logger.Warn("failed to fetch party data", "partyId", pid, "error", err)
				errChan <- fmt.Errorf("party %d: %w", pid, err)
				return
			}

			dataMu.Lock()
			data[pid] = roundData
			dataMu.Unlock()
		}(partyId)
	}

	wg.Wait()
	close(errChan)

	// Log collection errors but don't fail if we got most parties
	for err := range errChan {
		drc.logger.Warn("data collection error", "error", err)
	}

	return data, nil
}

// FetchPartyRoundData retrieves round data from a specific party.
func (drc *DKGRoundCoordinator) FetchPartyRoundData(ctx context.Context, ceremonyId string, roundNum, partyId int) (*RoundData, error) {
	// TODO: Get party endpoint from database based on ceremonyId + partyId
	endpoint := fmt.Sprintf("%s/round/%d/data", drc.endpointBase(partyId), roundNum)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := drc.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("party returned status %d: %s", resp.StatusCode, string(body))
	}

	var data RoundData
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	return &data, nil
}

// BroadcastRoundData sends each party's data to all other parties (fan-out).
func (drc *DKGRoundCoordinator) BroadcastRoundData(ctx context.Context, ceremonyId string, roundNum int, partyData map[int]*RoundData) error {
	// For each party, send data from all other parties
	for targetPartyId := range partyData {
		// Filter out the target party's own data
		otherData := make([]*RoundData, 0)
		for sourcePartyId, data := range partyData {
			if sourcePartyId != targetPartyId {
				otherData = append(otherData, data)
			}
		}

		// Send broadcast to target party
		endpoint := fmt.Sprintf("%s/round/%d/broadcast", drc.endpointBase(targetPartyId), roundNum)
		payload, _ := json.Marshal(map[string]interface{}{
			"ceremonyId": ceremonyId,
			"roundNum":   roundNum,
			"data":       otherData,
		})

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")

		resp, err := drc.httpClient.Do(req)
		if err != nil {
			drc.logger.Warn("failed to broadcast to party", "partyId", targetPartyId, "error", err)
			continue
		}
		resp.Body.Close()
	}

	return nil
}

// ValidateRoundData performs basic validation of round data.
// Real validation would check commitments, proofs, and consistency.
func (drc *DKGRoundCoordinator) ValidateRoundData(roundNum int, partyData map[int]*RoundData) []string {
	var errors []string

	for partyId, data := range partyData {
		// Basic validation: check required fields
		if data.Commitments == "" {
			errors = append(errors, fmt.Sprintf("party %d: missing commitments", partyId))
		}
		if data.PublicKey == "" {
			errors = append(errors, fmt.Sprintf("party %d: missing public key", partyId))
		}

		// Round-specific validation
		switch roundNum {
		case 1:
			// Round 1: Validate commitments are present
			if data.Commitments == "" {
				errors = append(errors, fmt.Sprintf("party %d: missing commitments in round 1", partyId))
			}
		case 2:
			// Round 2: Validate decommitments
			if data.DLProof == "" {
				errors = append(errors, fmt.Sprintf("party %d: missing decommitment proof in round 2", partyId))
			}
		case 3, 4, 5, 6, 7:
			// Rounds 3-7: Validate key shares and proofs
			if data.DLProof == "" && roundNum >= 5 {
				errors = append(errors, fmt.Sprintf("party %d: missing proof in round %d", partyId, roundNum))
			}
		}
	}

	return errors
}

// UpdateRoundInDatabase persists round state to PostgreSQL.
// TODO: Implement in ceremony_activities.go
// This would update ceremony_rounds table:
//
//	UPDATE ceremony_rounds SET status='completed' WHERE ceremony_id=$1 AND round_number=$2
func UpdateRoundInDatabase(ctx context.Context, ceremonyId string, roundNum int, status string) error {
	// TODO: Execute SQL update
	return nil
}
