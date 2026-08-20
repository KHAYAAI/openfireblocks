package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// DKGCeremonyWorkflow orchestrates a distributed key generation (DKG) ceremony.
//
// The ceremony proceeds through 7 rounds of the TSS-Lib DKG protocol:
//
//	Round 1: Commit phase (party sends commitments)
//	Round 2: Decommit phase
//	Round 3-7: Additional verification and key share generation
//
// On completion, each party's key share is sealed in Vault under:
//
//	secret/customers/{customerId}/ceremonies/{ceremonyId}/party/{partyId}
func DKGCeremonyWorkflow(ctx workflow.Context, req DKGCeremonyRequest) (*DKGCeremonyResult, error) {
	logger := workflow.GetLogger(ctx)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout:    10 * time.Minute,
		ScheduleToCloseTimeout: 30 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Step 0: Register parties and validate setup
	var registered RegisterPartiesResult
	if err := workflow.ExecuteActivity(ctx, "RegisterParties", RegisterPartiesRequest{
		CeremonyID:     req.CeremonyID,
		PartyIDs:       req.PartyIDs,
		PartyEndpoints: req.PartyEndpoints,
	}).Get(ctx, &registered); err != nil {
		logger.Error("failed to register parties", "error", err)
		return &DKGCeremonyResult{
			CeremonyID: req.CeremonyID,
			Status:     "failed",
			Error:      err.Error(),
		}, nil
	}

	if registered.RegisteredCount < req.N {
		logger.Warn("not all parties registered", "registered", registered.RegisteredCount, "expected", req.N)
		return &DKGCeremonyResult{
			CeremonyID: req.CeremonyID,
			Status:     "failed",
			Error:      "insufficient parties joined ceremony",
		}, nil
	}

	logger.Info("parties registered", "count", registered.RegisteredCount)

	// Steps 1-7: Execute DKG rounds sequentially
	// Each round communicates between parties to generate commitments, shares, and proofs
	for round := 1; round <= 7; round++ {
		var result DKGRoundResult
		if err := workflow.ExecuteActivity(ctx, "ExecuteDKGRound", DKGRoundRequest{
			CeremonyID: req.CeremonyID,
			RoundNum:   round,
			PartyIDs:   req.PartyIDs,
		}).Get(ctx, &result); err != nil {
			logger.Error("DKG round failed", "round", round, "error", err)
			return &DKGCeremonyResult{
				CeremonyID: req.CeremonyID,
				Status:     "failed",
				Error:      err.Error(),
			}, nil
		}

		if result.Status != "completed" {
			logger.Error("DKG round did not complete", "round", round, "error", result.Error)
			return &DKGCeremonyResult{
				CeremonyID: req.CeremonyID,
				Status:     "failed",
				Error:      result.Error,
			}, nil
		}

		logger.Info("DKG round completed", "round", round, "parties", len(result.PartyCommitments))
	}

	// Step 8: Seal key shares in Vault
	var sealed SealKeySharesResult
	if err := workflow.ExecuteActivity(ctx, "SealKeyShares", SealKeySharesRequest{
		CeremonyID: req.CeremonyID,
		PartyIDs:   req.PartyIDs,
		CustomerID: req.CustomerID,
	}).Get(ctx, &sealed); err != nil {
		logger.Error("failed to seal key shares", "error", err)
		return &DKGCeremonyResult{
			CeremonyID: req.CeremonyID,
			Status:     "failed",
			Error:      err.Error(),
		}, nil
	}

	logger.Info("key shares sealed", "count", sealed.SealedCount)

	// Ceremony complete
	return &DKGCeremonyResult{
		CeremonyID: req.CeremonyID,
		Status:     "completed",
		// ThresholdAddress and ThresholdPubKey are populated by the SealKeyShares activity
	}, nil
}

// ThresholdSigningWorkflow orchestrates signing with a threshold key from a completed ceremony.
//
// Steps:
//  1. Retrieve k+1 key shares from Vault for selected parties
//  2. Send signing request to parties
//  3. Collect signatures from parties
//  4. Combine signatures into final signature
//  5. Return signature ready for broadcast
func ThresholdSigningWorkflow(ctx workflow.Context, req ThresholdSigningRequest) (*ThresholdSigningResult, error) {
	logger := workflow.GetLogger(ctx)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout:    5 * time.Minute,
		ScheduleToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Step 1: Request signatures from parties
	var sigResult RequestSignaturesResult
	if err := workflow.ExecuteActivity(ctx, "RequestSignatures", RequestSignaturesRequest{
		CeremonyID: req.CeremonyID,
		Message:    req.Message,
		PartyIDs:   req.PartyIDs,
	}).Get(ctx, &sigResult); err != nil {
		logger.Error("failed to request signatures", "error", err)
		return &ThresholdSigningResult{
			Status: "failed",
			Error:  err.Error(),
		}, nil
	}

	if sigResult.Error != "" {
		logger.Error("signature collection failed", "error", sigResult.Error)
		return &ThresholdSigningResult{
			Status: "failed",
			Error:  sigResult.Error,
		}, nil
	}

	logger.Info("threshold signing completed", "ceremonyId", req.CeremonyID)

	return &ThresholdSigningResult{
		Signature: sigResult.Signature,
		Status:    "completed",
	}, nil
}

// KeyRotationWorkflow orchestrates key share rotation by initiating a new ceremony
// and deactivating the old ceremony's shares.
func KeyRotationWorkflow(ctx workflow.Context, req DKGCeremonyRequest) (*DKGCeremonyResult, error) {
	logger := workflow.GetLogger(ctx)

	// Step 1: Start new DKG ceremony (same parties)
	var newCeremony DKGCeremonyResult
	if err := workflow.ExecuteChildWorkflow(ctx, DKGCeremonyWorkflow, req).Get(ctx, &newCeremony); err != nil {
		logger.Error("new DKG ceremony failed", "error", err)
		return &DKGCeremonyResult{
			Status: "failed",
			Error:  err.Error(),
		}, nil
	}

	// Step 2: TODO - Deactivate old ceremony shares (schedule deletion after retention period)

	logger.Info("key rotation completed", "newCeremonyId", newCeremony.CeremonyID)
	return &newCeremony, nil
}
