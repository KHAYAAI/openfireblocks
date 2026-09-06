package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// DKGCeremonyWorkflow orchestrates a distributed key generation (DKG)
// ceremony by driving mpc-party's real tss-lib protocol
// (ExecuteRealDKG activity -> services/mpc-party's /tss/keygen/*
// endpoints) rather than the old placeholder that stepped through 7
// fixed rounds against non-cryptographic stub endpoints. tss-lib's real
// protocol has no fixed round count at this layer -- each party's
// keygen.LocalParty runs however many internal rounds it needs and
// relays messages directly to its peers over HTTP; this workflow's job
// is just to tell every party to start and wait for them all to report
// completion.
//
// Each party seals its own key share in Vault as part of reporting
// "completed" (see services/mpc-party/vault_seal.go) -- this workflow
// never receives or touches key material, only confirms every party
// reports sealed=true (ExecuteRealDKG logs a warning, not a failure, if
// fewer than all report sealed, since VAULT_ADDR being unset in a given
// deployment is a valid, if less durable, configuration -- see that
// activity and vault_seal.go for the precedence).
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

	var result DKGCeremonyResult
	if err := workflow.ExecuteActivity(ctx, "ExecuteRealDKG", req).Get(ctx, &result); err != nil {
		logger.Error("real DKG ceremony failed", "error", err)
		return &DKGCeremonyResult{
			CeremonyID: req.CeremonyID,
			Status:     "failed",
			Error:      err.Error(),
		}, nil
	}

	if result.Status != "completed" {
		logger.Error("DKG ceremony did not complete", "error", result.Error)
		return &result, nil
	}

	logger.Info("DKG ceremony completed", "ceremonyId", req.CeremonyID, "address", result.ThresholdAddress)
	return &result, nil
}

// ThresholdSigningWorkflow orchestrates signing with a threshold key from
// a completed DKG ceremony by driving mpc-party's real tss-lib signing
// protocol (ExecuteRealSigning activity -> /tss/sign/* endpoints), which
// replaced the old RequestSignatures stub -- that activity correctly
// returned a "not implemented" error rather than the hardcoded
// all-zeros signature it had previously fabricated, but this workflow
// now has a real implementation to call instead of erroring out.
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

	var result ThresholdSigningResult
	if err := workflow.ExecuteActivity(ctx, "ExecuteRealSigning", req).Get(ctx, &result); err != nil {
		logger.Error("real threshold signing failed", "error", err)
		return &ThresholdSigningResult{
			Status: "failed",
			Error:  err.Error(),
		}, nil
	}

	if result.Status != "completed" {
		logger.Error("threshold signing did not complete", "error", result.Error)
		return &result, nil
	}

	logger.Info("threshold signing completed", "ceremonyId", req.CeremonyID)
	return &result, nil
}

// KeyRotationWorkflow orchestrates real key rotation: run a new DKG
// ceremony, flip key_pairs.status so the new key is the one the API
// surface (and every other caller) treats as live, and retire the old
// ceremony's Vault-sealed shares after a retention window. Replaces the
// prior version of this workflow, which only did the first of those three
// steps and left a `// TODO: Deactivate old ceremony shares` where the
// rest belonged -- see docs/security/key-rotation.md section 1 for the
// gap this closes and BalanceMigrationWorkflow (balance_migration.go) for
// the other half: this workflow retires the old KEY, it does not move
// any funds still sitting at the old key's address.
func KeyRotationWorkflow(ctx workflow.Context, req KeyRotationRequest) (*KeyRotationResult, error) {
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

	// Step 1: run the new DKG ceremony (same parties, new key material).
	var newCeremony DKGCeremonyResult
	if err := workflow.ExecuteChildWorkflow(ctx, DKGCeremonyWorkflow, req.NewCeremony).Get(ctx, &newCeremony); err != nil {
		logger.Error("new DKG ceremony failed", "error", err)
		return &KeyRotationResult{Status: "failed", Error: err.Error()}, nil
	}
	if newCeremony.Status != "completed" {
		logger.Error("new DKG ceremony did not complete", "error", newCeremony.Error)
		return &KeyRotationResult{NewCeremony: newCeremony, Status: "failed", Error: newCeremony.Error}, nil
	}

	// Step 2: flip key_pairs.status -- new key becomes active with its
	// real address/pubkey, old key (if any) becomes inactive. This is
	// immediate, unlike share retirement below: it's what makes the new
	// key the one signing requests actually use, and there's no reason
	// to delay it once the ceremony has genuinely completed.
	if req.NewKeyID != "" {
		if err := workflow.ExecuteActivity(ctx, "ActivateKeyPair", ActivateKeyPairRequest{
			KeyID:     req.NewKeyID,
			Address:   newCeremony.ThresholdAddress,
			PublicKey: newCeremony.ThresholdPubKey,
		}).Get(ctx, nil); err != nil {
			logger.Error("failed to activate new key_pairs row", "error", err)
			return &KeyRotationResult{NewCeremony: newCeremony, Status: "failed", Error: fmt.Sprintf("activate new key: %v", err)}, nil
		}
	}
	if req.OldKeyID != "" {
		if err := workflow.ExecuteActivity(ctx, "SetKeyPairStatus", SetKeyPairStatusRequest{
			KeyID: req.OldKeyID, Status: "inactive",
		}).Get(ctx, nil); err != nil {
			logger.Error("failed to mark old key_pairs row inactive", "error", err)
			return &KeyRotationResult{NewCeremony: newCeremony, Status: "failed", Error: fmt.Sprintf("deactivate old key: %v", err)}, nil
		}
	}

	// Step 3: retire the old ceremony's Vault-sealed shares -- not
	// immediately (see ShareRetention's doc comment). workflow.Sleep is a
	// durable Temporal timer: this workflow execution can genuinely sit
	// here for the whole retention window, surviving worker restarts,
	// exactly like every other timer in this codebase.
	deactivated := false
	if req.OldCeremonyID != "" && len(req.OldPartyIDs) > 0 {
		retention := req.ShareRetention
		if retention <= 0 {
			retention = 720 * time.Hour // 30 days
		}
		if err := workflow.Sleep(ctx, retention); err != nil {
			logger.Error("retention sleep interrupted before old shares could be deactivated", "error", err)
			return &KeyRotationResult{NewCeremony: newCeremony, Status: "failed", Error: fmt.Sprintf("retention sleep interrupted: %v", err)}, nil
		}
		var deactivateResult DeactivateSharesResult
		if err := workflow.ExecuteActivity(ctx, "DeactivateOldKeyShares", DeactivateSharesRequest{
			CeremonyID: req.OldCeremonyID,
			PartyIDs:   req.OldPartyIDs,
		}).Get(ctx, &deactivateResult); err != nil {
			logger.Error("failed to deactivate old key shares in Vault", "error", err)
			return &KeyRotationResult{NewCeremony: newCeremony, Status: "failed", Error: fmt.Sprintf("deactivate old shares: %v", err)}, nil
		}
		logger.Info("old key shares soft-deleted in Vault after retention window",
			"ceremonyId", req.OldCeremonyID, "deactivatedCount", deactivateResult.DeactivatedCount, "errors", deactivateResult.Errors)
		deactivated = deactivateResult.DeactivatedCount > 0
	}

	logger.Info("key rotation completed", "newCeremonyId", newCeremony.CeremonyID)
	return &KeyRotationResult{NewCeremony: newCeremony, OldSharesDeactivated: deactivated, Status: "completed"}, nil
}
