package workflows_test

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	"forge-crypto/temporal-worker/activities"
	"forge-crypto/temporal-worker/workflows"
)

// TestProvisionKeyWorkflow exercises the path
// services/api-gateway/src/keys/keys.service.ts's createKey now starts:
// ceremony status -> in_progress, real DKG, key_pairs activation, ceremony
// status -> completed.
func TestProvisionKeyWorkflow(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))
	env.RegisterWorkflow(workflows.DKGCeremonyWorkflow)

	env.OnActivity("ExecuteRealDKG", mock.Anything, mock.Anything).Return(
		&workflows.DKGCeremonyResult{
			CeremonyID:       "ceremony-provision",
			Status:           "completed",
			ThresholdAddress: "0x1111111111111111111111111111111111111111",
			ThresholdPubKey:  "0xprovisionpubkey",
		},
		nil,
	)

	var statuses []string
	env.OnActivity("SetCeremonyStatus", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		statuses = append(statuses, args.Get(1).(workflows.SetCeremonyStatusRequest).Status)
	}).Return(nil)

	var activatedReq workflows.ActivateKeyPairRequest
	env.OnActivity("ActivateKeyPair", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		activatedReq = args.Get(1).(workflows.ActivateKeyPairRequest)
	}).Return(nil)

	req := workflows.ProvisionKeyRequest{
		Ceremony: workflows.DKGCeremonyRequest{
			CustomerID:     "customer-123",
			CeremonyID:     "ceremony-provision",
			ChainID:        "ethereum",
			N:              3,
			K:              1,
			PartyIDs:       []int{1, 2, 3},
			PartyEndpoints: []string{"http://party-1:7000", "http://party-2:7000", "http://party-3:7000"},
		},
		KeyID: "key-id-123",
	}

	env.ExecuteWorkflow(workflows.ProvisionKeyWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	var result workflows.ProvisionKeyResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("failed to get workflow result: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected status 'completed', got %s (%s)", result.Status, result.Error)
	}
	if activatedReq.KeyID != "key-id-123" || activatedReq.Address != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("ActivateKeyPair was not called with the ceremony result: %+v", activatedReq)
	}
	if len(statuses) != 2 || statuses[0] != "in_progress" || statuses[1] != "completed" {
		t.Fatalf("expected SetCeremonyStatus(in_progress) then SetCeremonyStatus(completed), got %v", statuses)
	}
}

// TestProvisionKeyWorkflow_FailureMarksCeremonyFailed proves a failed DKG
// still marks the ceremony 'failed' (so the customer-facing API can show a
// real failure, not "initiated" forever) and does not call ActivateKeyPair.
func TestProvisionKeyWorkflow_FailureMarksCeremonyFailed(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))
	env.RegisterWorkflow(workflows.DKGCeremonyWorkflow)

	env.OnActivity("ExecuteRealDKG", mock.Anything, mock.Anything).Return(
		&workflows.DKGCeremonyResult{Status: "failed", Error: "party 3 unreachable"},
		nil,
	)

	var statuses []string
	var lastErrMsg string
	env.OnActivity("SetCeremonyStatus", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		r := args.Get(1).(workflows.SetCeremonyStatusRequest)
		statuses = append(statuses, r.Status)
		lastErrMsg = r.ErrorMessage
	}).Return(nil)

	req := workflows.ProvisionKeyRequest{
		Ceremony: workflows.DKGCeremonyRequest{
			CeremonyID:     "ceremony-fail",
			PartyIDs:       []int{1, 2, 3},
			PartyEndpoints: []string{"http://party-1:7000", "http://party-2:7000", "http://party-3:7000"},
			K:              1,
		},
		KeyID: "key-id-456",
	}

	env.ExecuteWorkflow(workflows.ProvisionKeyWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	var result workflows.ProvisionKeyResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("failed to get workflow result: %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("expected status 'failed', got %s", result.Status)
	}
	if len(statuses) != 2 || statuses[0] != "in_progress" || statuses[1] != "failed" {
		t.Fatalf("expected SetCeremonyStatus(in_progress) then SetCeremonyStatus(failed), got %v", statuses)
	}
	if lastErrMsg == "" {
		t.Fatal("expected a non-empty error message on the failed ceremony status update")
	}

	env.AssertNotCalled(t, "ActivateKeyPair", mock.Anything, mock.Anything)
}
