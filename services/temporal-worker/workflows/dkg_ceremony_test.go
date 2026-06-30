package workflows

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

// TestDKGCeremonyWorkflow tests the DKG ceremony workflow with mocked activities.
func TestDKGCeremonyWorkflow(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	// Mock RegisterParties activity
	env.OnActivity("RegisterParties", mock.Anything, mock.Anything).Return(
		&RegisterPartiesResult{RegisteredCount: 3},
		nil,
	)

	// Mock ExecuteDKGRound for all 7 rounds
	for round := 1; round <= 7; round++ {
		env.OnActivity("ExecuteDKGRound", mock.Anything, mock.MatchedBy(func(req DKGRoundRequest) bool {
			return req.RoundNum == round
		})).Return(
			&DKGRoundResult{
				RoundNum:           round,
				Status:             "completed",
				PartyCommitments:   make(map[int]string),
			},
			nil,
		)
	}

	// Mock SealKeyShares activity
	env.OnActivity("SealKeyShares", mock.Anything, mock.Anything).Return(
		&SealKeySharesResult{SealedCount: 3},
		nil,
	)

	// Execute workflow
	req := DKGCeremonyRequest{
		CustomerID:    "customer-123",
		CeremonyID:    "ceremony-456",
		ChainID:       "ethereum",
		N:             3,
		K:             2,
		PartyIDs:      []int{0, 1, 2},
		PartyEndpoints: []string{"http://party0:7000", "http://party1:7000", "http://party2:7000"},
	}

	env.ExecuteWorkflow(DKGCeremonyWorkflow, req)

	// Verify workflow completed successfully
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	var result DKGCeremonyResult
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

	// Mock RegisterParties returning fewer parties than expected
	env.OnActivity("RegisterParties", mock.Anything, mock.Anything).Return(
		&RegisterPartiesResult{RegisteredCount: 1}, // Only 1 of 3 parties registered
		nil,
	)

	// Execute workflow
	req := DKGCeremonyRequest{
		CustomerID:    "customer-123",
		CeremonyID:    "ceremony-456",
		ChainID:       "ethereum",
		N:             3,
		K:             2,
		PartyIDs:      []int{0, 1, 2},
		PartyEndpoints: []string{"http://party0:7000", "http://party1:7000", "http://party2:7000"},
	}

	env.ExecuteWorkflow(DKGCeremonyWorkflow, req)

	// Verify workflow completed but with error status
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	var result DKGCeremonyResult
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

	// Mock RequestSignatures activity
	env.OnActivity("RequestSignatures", mock.Anything, mock.Anything).Return(
		&RequestSignaturesResult{
			Signature: "0x1234567890abcdef",
		},
		nil,
	)

	// Execute workflow
	req := ThresholdSigningRequest{
		CeremonyID: "ceremony-456",
		Message:    "0xdeadbeef",
		PartyIDs:   []int{0, 1, 2},
		ChainID:    "ethereum",
	}

	env.ExecuteWorkflow(ThresholdSigningWorkflow, req)

	// Verify workflow completed successfully
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}

	var result ThresholdSigningResult
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
