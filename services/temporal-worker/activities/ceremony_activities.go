package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.temporal.io/sdk/activity"

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

// ExecuteDKGRound: see dkg_round.go, which has the real implementation
// (DKGRoundCoordinator.SignalRoundStart/CollectRoundData/BroadcastRoundData
// actually calling out to parties). This file used to have a second,
// stub ExecuteDKGRound that always returned "completed" after a
// time.Sleep -- a duplicate method declaration on the same receiver, which
// doesn't even compile, so the two were never both live at once. Removed.

// SealKeyShares seals the resulting key shares in Vault after DKG completion.
func (a *Activities) SealKeyShares(ctx context.Context, req workflows.SealKeySharesRequest) (*workflows.SealKeySharesResult, error) {
	logger := activity.GetLogger(ctx)

	logger.Info("sealing key shares", "ceremonyId", req.CeremonyID, "customerId", req.CustomerID, "partyCount", len(req.PartyIDs))

	// Not implemented: this used to unconditionally return
	// SealedCount: len(req.PartyIDs), i.e. claim every share was sealed in
	// Vault without ever calling Vault. A Temporal workflow that trusts that
	// return value would proceed as if key custody were secured when it
	// isn't -- for a ceremony whose entire purpose is producing durably
	// sealed key shares, a fabricated success here is worse than an error.
	// Needs: fetch shares from the orchestrator DB (ceremony_parties table),
	// seal each into Vault at
	// secret/customers/{customerId}/ceremonies/{ceremonyId}/party/{partyId},
	// audit-log every Vault write, and confirm at least k+1 shares sealed
	// before returning success.
	return nil, fmt.Errorf("SealKeyShares is not implemented: no Vault client is wired into this activity")
}

// RequestSignatures coordinates threshold signing by collecting signatures from k+1 parties.
func (a *Activities) RequestSignatures(ctx context.Context, req workflows.RequestSignaturesRequest) (*workflows.RequestSignaturesResult, error) {
	logger := activity.GetLogger(ctx)

	logger.Info("requesting signatures", "ceremonyId", req.CeremonyID, "partyCount", len(req.PartyIDs))

	// Not implemented: this used to unconditionally return a hardcoded
	// all-zeros signature (0x00 + 64 zero bytes + 64 zero bytes + 00) as if
	// it were real. mpc-signer's real /sign endpoint exists (see
	// services/mpc-signer/main.go), but wiring genuine k+1-party fan-out and
	// threshold combination here is a real feature, not a bug fix -- and a
	// fabricated call against an unverified request/response contract would
	// just be a different flavor of the same problem: something that looks
	// done but silently isn't. A caller broadcasting a literal zero
	// signature to a blockchain is a much worse failure mode than this
	// activity returning an error.
	return nil, fmt.Errorf("RequestSignatures is not implemented: no threshold signing client is wired into this activity")
}
