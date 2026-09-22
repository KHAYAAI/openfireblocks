package workflows_test

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"forge-crypto/temporal-worker/activities"
	"forge-crypto/temporal-worker/workflows"
)

// These tests run the real workflow logic against mocked activities using
// Temporal's in-memory test environment — no Temporal server or HTTP services
// required. The external test package (workflows_test) avoids an import cycle
// with the activities package.

func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	// Register the activity struct so the workflow's string activity names
	// resolve; individual methods are mocked per-test via OnActivity.
	env.RegisterActivity(activities.NewActivities("", "", "", 3, nil))
	return env
}

var sampleReq = workflows.TransactionRequest{
	CustomerID:   "demo",
	CustomerTier: "pro",
	ChainID:      11155111,
	To:           "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
	Value:        "1000",
	GasLimit:     21000,
	GasPrice:     "20000000000",
	Nonce:        0,
}

func TestWorkflow_HappyPath(t *testing.T) {
	env := newEnv(t)

	env.OnActivity("CheckPolicy", mock.Anything, mock.Anything).
		Return(&workflows.PolicyCheckResult{Approved: true}, nil)
	env.OnActivity("SignTransaction", mock.Anything, mock.Anything).
		Return(&workflows.SignResult{RequestID: "r1", SignedTx: "0xsigned", TxHash: "0xhash", From: "0xfrom"}, nil)
	env.OnActivity("BroadcastTransaction", mock.Anything, "0xsigned").
		Return(&workflows.BroadcastResult{TxHash: "0xbroadcast"}, nil)
	env.OnActivity("MonitorTransaction", mock.Anything, "0xbroadcast").
		Return(&workflows.MonitorResult{BlockNumber: 100, Confirmations: 3, Success: true}, nil)

	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, sampleReq)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result workflows.TransactionResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "confirmed", result.Status)
	require.Equal(t, "0xbroadcast", result.TxHash)
	require.Equal(t, int64(100), result.BlockNumber)
}

func TestWorkflow_PolicyDenied(t *testing.T) {
	env := newEnv(t)

	env.OnActivity("CheckPolicy", mock.Anything, mock.Anything).
		Return(&workflows.PolicyCheckResult{
			Approved: false,
			Denials:  []string{"pro tier limited to 50 ETH per transaction"},
			Reason:   "1 policy violation(s)",
		}, nil)

	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, sampleReq)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result workflows.TransactionResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "denied", result.Status)
	// Signing must never be attempted on a denied transaction.
	env.AssertNotCalled(t, "SignTransaction", mock.Anything, mock.Anything)
}

func TestWorkflow_ApprovalGranted(t *testing.T) {
	env := newEnv(t)

	env.OnActivity("CheckPolicy", mock.Anything, mock.Anything).
		Return(&workflows.PolicyCheckResult{Approved: true, RequiresApproval: true}, nil)
	env.OnActivity("SignTransaction", mock.Anything, mock.Anything).
		Return(&workflows.SignResult{SignedTx: "0xsigned", TxHash: "0xhash"}, nil)
	env.OnActivity("BroadcastTransaction", mock.Anything, "0xsigned").
		Return(&workflows.BroadcastResult{TxHash: "0xbroadcast"}, nil)
	env.OnActivity("MonitorTransaction", mock.Anything, "0xbroadcast").
		Return(&workflows.MonitorResult{BlockNumber: 5, Confirmations: 3, Success: true}, nil)

	// Deliver the approval signal shortly after the workflow starts waiting.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("approval", true)
	}, 0)

	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, sampleReq)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result workflows.TransactionResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "confirmed", result.Status)
}
