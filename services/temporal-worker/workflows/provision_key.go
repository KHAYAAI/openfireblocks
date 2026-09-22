package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ProvisionKeyRequest is the workflow input for ProvisionKeyWorkflow: this
// is what services/api-gateway/src/keys/keys.service.ts's createKey starts
// now, instead of leaving a `// TODO: Trigger DKG ceremony workflow` and a
// key_pairs row that never gets a real address. See that file's doc
// comment and docs/security/key-rotation.md for the gap this closes: every
// other real DKG/rotation/signing workflow in this codebase was, until
// this, only reachable by constructing its request struct directly (a
// test, or a script) -- none of it was reachable from the actual
// customer-facing API.
type ProvisionKeyRequest struct {
	Ceremony DKGCeremonyRequest `json:"ceremony"`

	// KeyID is the key_pairs.key_id row the caller already created
	// (status 'pending_dkg') before starting this workflow -- mirrors
	// KeyRotationRequest.NewKeyID. Left empty to run a DKG ceremony
	// without touching key_pairs at all (e.g. a test/script caller that
	// manages its own bookkeeping).
	KeyID string `json:"keyId,omitempty"`
}

// ProvisionKeyResult is the workflow output.
type ProvisionKeyResult struct {
	Ceremony DKGCeremonyResult `json:"ceremony"`
	Status   string            `json:"status"` // completed | failed
	Error    string            `json:"error,omitempty"`
}

// ProvisionKeyWorkflow runs a real DKG ceremony for a brand-new key and
// keeps both key_pairs and dkg_ceremonies (see
// infrastructure/database/migrations/001_initial_schema.sql) truthful
// about it: dkg_ceremonies.status moves initiated -> in_progress ->
// completed/failed, and key_pairs.status/address/public_key are only ever
// set once the real ceremony has actually completed -- the same
// ActivateKeyPair activity KeyRotationWorkflow uses for a new key's other
// path (rotation).
//
// A ceremony-status tracking-table write failing does not fail this
// workflow or leave the caller without their real key: the actual DKG
// result is what matters, not the bookkeeping row describing it (logged,
// not propagated -- same standard DeactivateOldKeyShares holds its own
// per-party failures to in dkg_ceremony.go).
func ProvisionKeyWorkflow(ctx workflow.Context, req ProvisionKeyRequest) (*ProvisionKeyResult, error) {
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

	if req.Ceremony.CeremonyID != "" {
		if err := workflow.ExecuteActivity(ctx, "SetCeremonyStatus", SetCeremonyStatusRequest{
			CeremonyID: req.Ceremony.CeremonyID, Status: "in_progress",
		}).Get(ctx, nil); err != nil {
			logger.Warn("failed to mark ceremony in_progress (continuing -- this is bookkeeping, not the ceremony itself)", "error", err)
		}
	}

	var ceremony DKGCeremonyResult
	if err := workflow.ExecuteChildWorkflow(ctx, DKGCeremonyWorkflow, req.Ceremony).Get(ctx, &ceremony); err != nil {
		logger.Error("DKG ceremony failed", "error", err)
		markCeremonyFailed(ctx, req.Ceremony.CeremonyID, err.Error())
		return &ProvisionKeyResult{Status: "failed", Error: err.Error()}, nil
	}
	if ceremony.Status != "completed" {
		logger.Error("DKG ceremony did not complete", "error", ceremony.Error)
		markCeremonyFailed(ctx, req.Ceremony.CeremonyID, ceremony.Error)
		return &ProvisionKeyResult{Ceremony: ceremony, Status: "failed", Error: ceremony.Error}, nil
	}

	if req.KeyID != "" {
		if err := workflow.ExecuteActivity(ctx, "ActivateKeyPair", ActivateKeyPairRequest{
			KeyID:     req.KeyID,
			Address:   ceremony.ThresholdAddress,
			PublicKey: ceremony.ThresholdPubKey,
		}).Get(ctx, nil); err != nil {
			logger.Error("DKG completed but failed to activate key_pairs row", "error", err)
			markCeremonyFailed(ctx, req.Ceremony.CeremonyID, err.Error())
			return &ProvisionKeyResult{Ceremony: ceremony, Status: "failed", Error: err.Error()}, nil
		}
	}

	if req.Ceremony.CeremonyID != "" {
		if err := workflow.ExecuteActivity(ctx, "SetCeremonyStatus", SetCeremonyStatusRequest{
			CeremonyID: req.Ceremony.CeremonyID, Status: "completed",
		}).Get(ctx, nil); err != nil {
			logger.Warn("failed to mark ceremony completed (continuing -- key was provisioned successfully)", "error", err)
		}
	}

	logger.Info("key provisioning completed", "ceremonyId", ceremony.CeremonyID, "keyId", req.KeyID, "address", ceremony.ThresholdAddress)
	return &ProvisionKeyResult{Ceremony: ceremony, Status: "completed"}, nil
}

func markCeremonyFailed(ctx workflow.Context, ceremonyID, errMsg string) {
	if ceremonyID == "" {
		return
	}
	logger := workflow.GetLogger(ctx)
	if err := workflow.ExecuteActivity(ctx, "SetCeremonyStatus", SetCeremonyStatusRequest{
		CeremonyID: ceremonyID, Status: "failed", ErrorMessage: errMsg,
	}).Get(ctx, nil); err != nil {
		logger.Warn("failed to mark ceremony failed (continuing)", "error", err)
	}
}
