package workflows_test

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"

	"forge-crypto/temporal-worker/activities"
	"forge-crypto/temporal-worker/workflows"
)

// TestDKGCeremonyWorkflow tests the DKG ceremony workflow with mocked activities.
func TestDKGCeremonyWorkflow(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))

	// Mock RegisterParties activity
	env.OnActivity("RegisterParties", mock.Anything, mock.Anything).Return(
		&workflows.RegisterPartiesResult{RegisteredCount: 3},
		nil,
	)

	// Mock ExecuteDKGRound for all 7 rounds
	for round := 1; round <= 7; round++ {
		env.OnActivity("ExecuteDKGRound", mock.Anything, mock.MatchedBy(func(req workflows.DKGRoundRequest) bool {
			return req.RoundNum == round
		})).Return(
			&workflows.DKGRoundResult{
				RoundNum:         round,
				Status:           "completed",
				PartyCommitments: make(map[int]string),
			},
			nil,
		)
	}

	// Mock SealKeyShares activity
	env.OnActivity("SealKeyShares", mock.Anything, mock.Anything).Return(
		&workflows.SealKeySharesResult{SealedCount: 3},
		nil,
	)

	// Execute workflow
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

	// Verify workflow completed successfully
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
}

// TestDKGCeremonyWorkflow_FailureOnRegisterParties tests workflow failure when party registration fails.
func TestDKGCeremonyWorkflow_FailureOnRegisterParties(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))

	// Mock RegisterParties returning fewer parties than expected
	env.OnActivity("RegisterParties", mock.Anything, mock.Anything).Return(
		&workflows.RegisterPartiesResult{RegisteredCount: 1}, // Only 1 of 3 parties registered
		nil,
	)

	// Execute workflow
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

	// Verify workflow completed but with error status
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
}

// TestThresholdSigningWorkflow tests the threshold signing workflow.
func TestThresholdSigningWorkflow(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))

	// Mock RequestSignatures activity
	env.OnActivity("RequestSignatures", mock.Anything, mock.Anything).Return(
		&workflows.RequestSignaturesResult{
			Signature: "0x1234567890abcdef",
		},
		nil,
	)

	// Execute workflow
	req := workflows.ThresholdSigningRequest{
		CeremonyID: "ceremony-456",
		Message:    "0xdeadbeef",
		PartyIDs:   []int{0, 1, 2},
		ChainID:    "ethereum",
	}

	env.ExecuteWorkflow(workflows.ThresholdSigningWorkflow, req)

	// Verify workflow completed successfully
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
