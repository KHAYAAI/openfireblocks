package workflows_test

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	"forge-crypto/temporal-worker/activities"
	"forge-crypto/temporal-worker/workflows"
)

// TestKeyRotationWorkflow exercises the full three-step rotation this
// workflow now performs (see dkg_ceremony.go's doc comment on
// KeyRotationWorkflow for what it replaced): new DKG ceremony, activate
// the new key_pairs row / deactivate the old one, then -- after a
// (mocked-short) retention window -- soft-delete the old shares in
// Vault. Temporal's test environment auto-skips workflow.Sleep when
// nothing else is pending, so a real ShareRetention duration (even the
// 30-day default) resolves immediately here without actually waiting.
func TestKeyRotationWorkflow(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))
	env.RegisterWorkflow(workflows.DKGCeremonyWorkflow)

	env.OnActivity("ExecuteRealDKG", mock.Anything, mock.Anything).Return(
		&workflows.DKGCeremonyResult{
			CeremonyID:       "ceremony-new",
			Status:           "completed",
			ThresholdAddress: "0x2222222222222222222222222222222222222222",
			ThresholdPubKey:  "0xnewpubkey",
		},
		nil,
	)

	var activatedReq workflows.ActivateKeyPairRequest
	env.OnActivity("ActivateKeyPair", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		activatedReq = args.Get(1).(workflows.ActivateKeyPairRequest)
	}).Return(nil)

	var deactivatedKeyID string
	env.OnActivity("SetKeyPairStatus", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		deactivatedKeyID = args.Get(1).(workflows.SetKeyPairStatusRequest).KeyID
	}).Return(nil)

	var sharesReq workflows.DeactivateSharesRequest
	env.OnActivity("DeactivateOldKeyShares", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		sharesReq = args.Get(1).(workflows.DeactivateSharesRequest)
	}).Return(&workflows.DeactivateSharesResult{DeactivatedCount: 3}, nil)

	req := workflows.KeyRotationRequest{
		NewCeremony: workflows.DKGCeremonyRequest{
			CustomerID:     "customer-123",
			CeremonyID:     "ceremony-new",
			ChainID:        "ethereum",
			N:              3,
			K:              1,
			PartyIDs:       []int{1, 2, 3},
			PartyEndpoints: []string{"http://party1:7000", "http://party2:7000", "http://party3:7000"},
		},
		OldKeyID:      "old-key-id",
		NewKeyID:      "new-key-id",
		OldCeremonyID: "ceremony-old",
		OldPartyIDs:   []int{1, 2, 3},
		// Left at the 30-day default deliberately -- proving the
		// time-skip actually works, not just a short duration.
	}

	env.ExecuteWorkflow(workflows.KeyRotationWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	var result workflows.KeyRotationResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("failed to get workflow result: %v", err)
	}

	if result.Status != "completed" {
		t.Fatalf("expected status 'completed', got %s (%s)", result.Status, result.Error)
	}
	if !result.OldSharesDeactivated {
		t.Fatal("expected OldSharesDeactivated=true")
	}
	if activatedReq.KeyID != "new-key-id" || activatedReq.Address != "0x2222222222222222222222222222222222222222" {
		t.Fatalf("ActivateKeyPair was not called with the new ceremony's result: %+v", activatedReq)
	}
	if deactivatedKeyID != "old-key-id" {
		t.Fatalf("expected SetKeyPairStatus to target old-key-id, got %s", deactivatedKeyID)
	}
	if sharesReq.CeremonyID != "ceremony-old" {
		t.Fatalf("expected DeactivateOldKeyShares to target ceremony-old, got %s", sharesReq.CeremonyID)
	}
}

// TestKeyRotationWorkflow_SkipsShareDeactivationWithoutOldCeremony proves
// a first-ever key (no prior ceremony to retire) doesn't try to deactivate
// anything -- OldCeremonyID/OldKeyID left empty must be treated as "skip",
// not "call Vault with an empty ceremony ID."
func TestKeyRotationWorkflow_SkipsShareDeactivationWithoutOldCeremony(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))
	env.RegisterWorkflow(workflows.DKGCeremonyWorkflow)

	env.OnActivity("ExecuteRealDKG", mock.Anything, mock.Anything).Return(
		&workflows.DKGCeremonyResult{
			CeremonyID:       "ceremony-first",
			Status:           "completed",
			ThresholdAddress: "0x3333333333333333333333333333333333333333",
			ThresholdPubKey:  "0xfirstpubkey",
		},
		nil,
	)
	env.OnActivity("ActivateKeyPair", mock.Anything, mock.Anything).Return(nil)

	req := workflows.KeyRotationRequest{
		NewCeremony: workflows.DKGCeremonyRequest{
			CeremonyID:     "ceremony-first",
			PartyIDs:       []int{1, 2, 3},
			PartyEndpoints: []string{"http://party1:7000", "http://party2:7000", "http://party3:7000"},
			K:              1,
		},
		NewKeyID: "first-key-id",
		// OldKeyID/OldCeremonyID/OldPartyIDs deliberately left empty.
	}

	env.ExecuteWorkflow(workflows.KeyRotationWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	var result workflows.KeyRotationResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("failed to get workflow result: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected status 'completed', got %s (%s)", result.Status, result.Error)
	}
	if result.OldSharesDeactivated {
		t.Fatal("expected OldSharesDeactivated=false when there's no old ceremony to retire")
	}

	env.AssertNotCalled(t, "SetKeyPairStatus", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "DeactivateOldKeyShares", mock.Anything, mock.Anything)
}

// TestKeyRotationWorkflow_FailureOnNewCeremony proves a failed new
// ceremony aborts rotation before touching anything old -- retiring the
// old key when its replacement was never actually produced would leave
// the customer with no usable key at all.
func TestKeyRotationWorkflow_FailureOnNewCeremony(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))
	env.RegisterWorkflow(workflows.DKGCeremonyWorkflow)

	env.OnActivity("ExecuteRealDKG", mock.Anything, mock.Anything).Return(
		&workflows.DKGCeremonyResult{Status: "failed", Error: "party 2 unreachable"},
		nil,
	)

	req := workflows.KeyRotationRequest{
		NewCeremony: workflows.DKGCeremonyRequest{
			CeremonyID:     "ceremony-new",
			PartyIDs:       []int{1, 2, 3},
			PartyEndpoints: []string{"http://party1:7000", "http://party2:7000", "http://party3:7000"},
			K:              1,
		},
		OldKeyID:      "old-key-id",
		NewKeyID:      "new-key-id",
		OldCeremonyID: "ceremony-old",
		OldPartyIDs:   []int{1, 2, 3},
	}

	env.ExecuteWorkflow(workflows.KeyRotationWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	var result workflows.KeyRotationResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("failed to get workflow result: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("expected status 'failed', got %s", result.Status)
	}

	env.AssertNotCalled(t, "ActivateKeyPair", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "SetKeyPairStatus", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "DeactivateOldKeyShares", mock.Anything, mock.Anything)
}
