package workflows

import (
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
