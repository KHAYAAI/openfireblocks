package activities

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.temporal.io/sdk/activity"

	"forge-crypto/temporal-worker/db"
	"forge-crypto/temporal-worker/workflows"
)

// RegisterParties registers parties for a DKG ceremony by making HTTP calls to each party endpoint.
func (a *Activities) RegisterParties(ctx context.Context, req workflows.RegisterPartiesRequest) (*workflows.RegisterPartiesResult, error) {
	logger := activity.GetLogger(ctx)

	registered := 0
	for i, partyID := range req.PartyIDs {
		if i >= len(req.PartyEndpoints) {
			break
		}

		endpoint := req.PartyEndpoints[i]
		logger.Info("registering party", "partyId", partyID, "endpoint", endpoint)

		// Send registration request to party
		registerReq := map[string]interface{}{
			"ceremonyId": req.CeremonyID,
			"partyId":    partyID,
		}

		payload, err := json.Marshal(registerReq)
		if err != nil {
			logger.Error("failed to marshal request", "partyId", partyID, "error", err)
			continue
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/register", bytes.NewReader(payload))
		if err != nil {
			logger.Error("failed to create request", "partyId", partyID, "error", err)
			continue
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := a.httpClient.Do(httpReq)
		if err != nil {
			logger.Error("registration request failed", "partyId", partyID, "error", err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 300 {
			logger.Error("registration returned error status", "partyId", partyID, "status", resp.StatusCode)
			continue
		}

		registered++
	}

	return &workflows.RegisterPartiesResult{
		RegisteredCount: registered,
	}, nil
}

// ExecuteDKGRound executes a single DKG round (1-7) by coordinating message exchange between parties.
func (a *Activities) ExecuteDKGRound(ctx context.Context, req workflows.DKGRoundRequest) (*workflows.DKGRoundResult, error) {
	logger := activity.GetLogger(ctx)

	logger.Info("executing DKG round", "ceremonyId", req.CeremonyID, "round", req.RoundNum)

	// TODO: Implement DKG round execution
	// This would involve:
	// 1. Sending round messages to parties
	// 2. Collecting party responses
	// 3. Validating commitments/proofs
	// 4. Saving round state to database

	// For now, return a placeholder
	time.Sleep(1 * time.Second) // Simulate round execution

	return &workflows.DKGRoundResult{
		RoundNum:           req.RoundNum,
		Status:             "completed",
		PartyCommitments:   make(map[int]string),
	}, nil
}

// SealKeyShares seals the resulting key shares in Vault after DKG completion.
func (a *Activities) SealKeyShares(ctx context.Context, req workflows.SealKeySharesRequest) (*workflows.SealKeySharesResult, error) {
	logger := activity.GetLogger(ctx)

	logger.Info("sealing key shares", "ceremonyId", req.CeremonyID, "customerId", req.CustomerID, "partyCount", len(req.PartyIDs))

	// TODO: Implement key share sealing
	// This would involve:
	// 1. Retrieving key shares from orchestrator DB (from ceremony_parties table)
	// 2. Encrypting and sealing in Vault under path:
	//    secret/customers/{customerId}/ceremonies/{ceremonyId}/party/{partyId}
	// 3. Audit logging every Vault operation (log to immudb + PostgreSQL)
	// 4. Verifying at least k+1 shares exist before returning success
	//
	// Pseudocode:
	// for partyId := range PartyIDs {
	//   share := db.GetKeyShare(req.CeremonyID, partyId)
	//   vaultClient.SealKeyShare(ctx, req.CustomerID, req.CeremonyID, partyId, share)
	//   auditLog("KEY_SHARE_SEALED", partyId, ceremonyId)
	// }

	sealed := len(req.PartyIDs) // Placeholder: assume all sealing succeeds
	return &workflows.SealKeySharesResult{
		SealedCount: sealed,
	}, nil
}

// RequestSignatures coordinates threshold signing by collecting signatures from k+1 parties.
func (a *Activities) RequestSignatures(ctx context.Context, req workflows.RequestSignaturesRequest) (*workflows.RequestSignaturesResult, error) {
	logger := activity.GetLogger(ctx)

	logger.Info("requesting signatures", "ceremonyId", req.CeremonyID, "partyCount", len(req.PartyIDs))

	// TODO: Implement multi-party signing
	// This would involve:
	// 1. Fetching key shares from Vault for selected parties
	// 2. Sending signing requests to parties in parallel
	// 3. Collecting and combining signatures
	// 4. Validating final signature

	// For now, return a placeholder
	return &workflows.RequestSignaturesResult{
		Signature: "0x" + "00" + "0000000000000000000000000000000000000000000000000000000000000000" + // placeholder R
			"0000000000000000000000000000000000000000000000000000000000000000" + // placeholder S
			"00", // placeholder V
	}, nil
}
