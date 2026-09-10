package workflows_test

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	"forge-crypto/temporal-worker/activities"
	"forge-crypto/temporal-worker/workflows"
)

// TestDKGCeremonyWorkflow tests the DKG ceremony workflow with a mocked
// ExecuteRealDKG activity -- the workflow itself is now a thin wrapper
// around that one activity (see dkg_ceremony.go's doc comment for why:
// tss-lib's real protocol relays messages directly between mpc-party
// processes, not through this orchestrator round by round).
func TestDKGCeremonyWorkflow(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))

	env.OnActivity("ExecuteRealDKG", mock.Anything, mock.Anything).Return(
		&workflows.DKGCeremonyResult{
			CeremonyID:       "ceremony-456",
			Status:           "completed",
			ThresholdAddress: "0x1234567890123456789012345678901234567890",
			ThresholdPubKey:  "0xdeadbeef",
		},
		nil,
	)

	req := workflows.DKGCeremonyRequest{
		CustomerID:     "customer-123",
		CeremonyID:     "ceremony-456",
		ChainID:        "ethereum",
		N:              3,
		K:              2,
		PartyIDs:       []int{0, 1, 2},
		PartyEndpoints: []string{"http://party0:7000", "http://party1:7000", "http://party2:7000"},
	}

	env.ExecuteWorkflow(workflows.DKGCeremonyWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	var result workflows.DKGCeremonyResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("failed to get workflow result: %v", err)
	}

	if result.Status != "completed" {
		t.Fatalf("expected status 'completed', got %s", result.Status)
	}
	if result.CeremonyID != "ceremony-456" {
		t.Fatalf("expected ceremonyId 'ceremony-456', got %s", result.CeremonyID)
	}
	if result.ThresholdAddress == "" {
		t.Fatal("expected a non-empty ThresholdAddress")
	}
}

// TestDKGCeremonyWorkflow_FailureOnRealDKG tests that the workflow
// surfaces a failed ExecuteRealDKG result (e.g. a party unreachable, or
// the DKG protocol itself failing) as a failed workflow result rather
// than papering over it.
func TestDKGCeremonyWorkflow_FailureOnRealDKG(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))

	env.OnActivity("ExecuteRealDKG", mock.Anything, mock.Anything).Return(
		&workflows.DKGCeremonyResult{
			CeremonyID: "ceremony-456",
			Status:     "failed",
			Error:      "party 1: unreachable",
		},
		nil,
	)

	req := workflows.DKGCeremonyRequest{
		CustomerID:     "customer-123",
		CeremonyID:     "ceremony-456",
		ChainID:        "ethereum",
		N:              3,
		K:              2,
		PartyIDs:       []int{0, 1, 2},
		PartyEndpoints: []string{"http://party0:7000", "http://party1:7000", "http://party2:7000"},
	}

	env.ExecuteWorkflow(workflows.DKGCeremonyWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result workflows.DKGCeremonyResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("failed to get workflow result: %v", err)
	}

	if result.Status != "failed" {
		t.Fatalf("expected status 'failed', got %s", result.Status)
	}
	if result.Error == "" {
		t.Fatal("expected a non-empty error message")
	}
}

// TestThresholdSigningWorkflow tests the threshold signing workflow with
// a mocked ExecuteRealSigning activity.
func TestThresholdSigningWorkflow(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))

	env.OnActivity("ExecuteRealSigning", mock.Anything, mock.Anything).Return(
		&workflows.ThresholdSigningResult{
			Status:    "completed",
			Signature: "0x1234567890abcdef",
		},
		nil,
	)

	req := workflows.ThresholdSigningRequest{
		CeremonyID:     "ceremony-456",
		Message:        "0xdeadbeef",
		PartyIDs:       []int{0, 1, 2},
		PartyEndpoints: []string{"http://party0:7000", "http://party1:7000", "http://party2:7000"},
		ChainID:        "ethereum",
	}

	env.ExecuteWorkflow(workflows.ThresholdSigningWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	var result workflows.ThresholdSigningResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("failed to get workflow result: %v", err)
	}

	if result.Status != "completed" {
		t.Fatalf("expected status 'completed', got %s", result.Status)
	}
	if result.Signature != "0x1234567890abcdef" {
		t.Fatalf("expected signature '0x1234567890abcdef', got %s", result.Signature)
	}
}

// TestThresholdSigningWorkflow_FailureOnRealSigning tests that a failed
// ExecuteRealSigning result surfaces as a failed workflow result.
func TestThresholdSigningWorkflow_FailureOnRealSigning(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))

	env.OnActivity("ExecuteRealSigning", mock.Anything, mock.Anything).Return(
		&workflows.ThresholdSigningResult{
			Status: "failed",
			Error:  "party 2: committee member unreachable",
		},
		nil,
	)

	req := workflows.ThresholdSigningRequest{
		CeremonyID:     "ceremony-456",
		Message:        "0xdeadbeef",
		PartyIDs:       []int{0, 1, 2},
		PartyEndpoints: []string{"http://party0:7000", "http://party1:7000", "http://party2:7000"},
		ChainID:        "ethereum",
	}

	env.ExecuteWorkflow(workflows.ThresholdSigningWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result workflows.ThresholdSigningResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("failed to get workflow result: %v", err)
	}

	if result.Status != "failed" {
		t.Fatalf("expected status 'failed', got %s", result.Status)
	}
}
