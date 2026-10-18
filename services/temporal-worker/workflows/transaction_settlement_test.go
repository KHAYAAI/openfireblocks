package workflows_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

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

// --- Approvals with segregation of duties --------------------------------

const (
	initiator = "11111111-1111-1111-1111-111111111111"
	alice     = "22222222-2222-2222-2222-222222222222"
	bob       = "33333333-3333-3333-3333-333333333333"
)

func approvalReq() workflows.TransactionRequest {
	r := sampleReq
	r.InitiatedByUserID = initiator
	r.InitiatedByLabel = "initiator@example.com"
	return r
}

// approvalEnv mocks a policy that requires approval, an approval record
// needing `required` approvals and expiring in an hour, and a database
// that reports `recorded` when asked what it decided.
func approvalEnv(t *testing.T, required int, recorded workflows.RecordedApprovalStatus) *testsuite.TestWorkflowEnvironment {
	env := newEnv(t)
	env.OnActivity("CheckPolicy", mock.Anything, mock.Anything).
		Return(&workflows.PolicyCheckResult{Approved: true, RequiresApproval: true,
			ApprovalReasons: []string{"high-value transaction (> 10 ETH) requires approval"}}, nil)
	env.OnActivity("OpenApprovalRequest", mock.Anything, mock.Anything).
		Return(&workflows.OpenApprovalRequestResult{ApprovalID: "ap-1", RequiredApprovals: required, ExpiresInSeconds: 3600}, nil)
	env.OnActivity("GetRecordedApprovalStatus", mock.Anything, mock.Anything).Return(&recorded, nil).Maybe()
	env.OnActivity("CloseApprovalRequest", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("SignTransaction", mock.Anything, mock.Anything).
		Return(&workflows.SignResult{SignedTx: "0xsigned", TxHash: "0xhash"}, nil).Maybe()
	env.OnActivity("BroadcastTransaction", mock.Anything, "0xsigned").
		Return(&workflows.BroadcastResult{TxHash: "0xbroadcast"}, nil).Maybe()
	env.OnActivity("MonitorTransaction", mock.Anything, "0xbroadcast").
		Return(&workflows.MonitorResult{BlockNumber: 5, Confirmations: 3, Success: true}, nil).Maybe()
	return env
}

func decide(env *testsuite.TestWorkflowEnvironment, after time.Duration, user, decision string) {
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflows.ApprovalDecisionSignalName,
			workflows.ApprovalDecisionSignal{ApproverUserID: user, Decision: decision})
	}, after)
}

func result(t *testing.T, env *testsuite.TestWorkflowEnvironment) workflows.TransactionResult {
	t.Helper()
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var r workflows.TransactionResult
	require.NoError(t, env.GetWorkflowResult(&r))
	return r
}

func closedAs(status string) interface{} {
	return mock.MatchedBy(func(in workflows.CloseApprovalRequestInput) bool { return in.Status == status })
}

var approvedInDB = workflows.RecordedApprovalStatus{Status: "approved", DistinctApprovals: 2, RequiredApprovals: 2}

func TestTwoDistinctApproversSettleTheTransfer(t *testing.T) {
	env := approvalEnv(t, 2, approvedInDB)
	decide(env, time.Minute, alice, "approve")
	decide(env, 2*time.Minute, bob, "approve")
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, approvalReq())
	require.Equal(t, "confirmed", result(t, env).Status)
}

func TestOneApprovalIsNotEnoughWhenTwoAreRequired(t *testing.T) {
	env := approvalEnv(t, 2, approvedInDB)
	decide(env, time.Minute, alice, "approve")
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, approvalReq())
	r := result(t, env)
	require.Equal(t, "denied", r.Status)
	require.Equal(t, "approval expired", r.Reason)
	env.AssertNotCalled(t, "SignTransaction", mock.Anything, mock.Anything)
	env.AssertCalled(t, "CloseApprovalRequest", mock.Anything, closedAs("expired"))
}

// The initiator approving their own transfer counts for nothing: with the
// initiator plus one real approver, a 2-approval transfer still expires.
func TestTheInitiatorCannotApproveTheirOwnTransfer(t *testing.T) {
	env := approvalEnv(t, 2, approvedInDB)
	decide(env, time.Minute, initiator, "approve")
	decide(env, 2*time.Minute, alice, "approve")
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, approvalReq())
	require.Equal(t, "denied", result(t, env).Status)
	env.AssertNotCalled(t, "SignTransaction", mock.Anything, mock.Anything)
}

// Nor can the initiator veto it: their rejection is ignored as well.
func TestTheInitiatorsRejectionIsIgnoredToo(t *testing.T) {
	env := approvalEnv(t, 2, approvedInDB)
	decide(env, time.Minute, initiator, "reject")
	decide(env, 2*time.Minute, alice, "approve")
	decide(env, 3*time.Minute, bob, "approve")
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, approvalReq())
	require.Equal(t, "confirmed", result(t, env).Status)
}

func TestTheSamePersonApprovingTwiceCountsOnce(t *testing.T) {
	env := approvalEnv(t, 2, approvedInDB)
	decide(env, time.Minute, alice, "approve")
	decide(env, 2*time.Minute, alice, "approve")
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, approvalReq())
	require.Equal(t, "denied", result(t, env).Status)
	env.AssertNotCalled(t, "SignTransaction", mock.Anything, mock.Anything)
}

func TestOneRejectionRejects(t *testing.T) {
	env := approvalEnv(t, 2, approvedInDB)
	decide(env, time.Minute, alice, "approve")
	decide(env, 2*time.Minute, bob, "reject")
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, approvalReq())
	r := result(t, env)
	require.Equal(t, "denied", r.Status)
	require.Equal(t, "approval rejected", r.Reason)
	env.AssertNotCalled(t, "SignTransaction", mock.Anything, mock.Anything)
}

// Signals are not enough. If the database -- where roles and identities
// are checked -- did not record the approvals, the workflow refuses, and
// closes the request as rejected.
func TestSignalsTheDatabaseDidNotRecordCannotCauseASignature(t *testing.T) {
	env := approvalEnv(t, 2, workflows.RecordedApprovalStatus{Status: "pending", DistinctApprovals: 0, RequiredApprovals: 2})
	decide(env, time.Minute, alice, "approve")
	decide(env, 2*time.Minute, bob, "approve")
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, approvalReq())
	require.Equal(t, "denied", result(t, env).Status)
	env.AssertNotCalled(t, "SignTransaction", mock.Anything, mock.Anything)
	env.AssertCalled(t, "CloseApprovalRequest", mock.Anything, closedAs("rejected"))
}

// A zero from the approval record is never read as "no approval needed".
func TestAZeroApprovalRequirementStillNeedsOneApproval(t *testing.T) {
	env := approvalEnv(t, 0, workflows.RecordedApprovalStatus{Status: "approved", DistinctApprovals: 1, RequiredApprovals: 1})
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, approvalReq())
	require.Equal(t, "denied", result(t, env).Status)
	env.AssertNotCalled(t, "SignTransaction", mock.Anything, mock.Anything)
}

// A machine-initiated transfer has no initiator to exclude; any two
// approvers settle it.
func TestAnAPIKeyInitiatedTransferNeedsTheSameQuorum(t *testing.T) {
	env := approvalEnv(t, 2, approvedInDB)
	decide(env, time.Minute, alice, "approve")
	decide(env, 2*time.Minute, bob, "approve")
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, sampleReq)
	require.Equal(t, "confirmed", result(t, env).Status)
}

func TestTheApprovalStateCanBeQueriedWhileWaiting(t *testing.T) {
	env := approvalEnv(t, 2, approvedInDB)
	decide(env, time.Minute, alice, "approve")
	env.RegisterDelayedCallback(func() {
		v, err := env.QueryWorkflow(workflows.ApprovalStateQueryName)
		require.NoError(t, err)
		var st workflows.ApprovalState
		require.NoError(t, v.Get(&st))
		require.Equal(t, "pending", st.Status)
		require.Equal(t, 2, st.RequiredApprovals)
		require.Equal(t, []string{alice}, st.ApprovedBy)
	}, 90*time.Second)
	decide(env, 2*time.Minute, bob, "approve")
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, approvalReq())
	require.Equal(t, "confirmed", result(t, env).Status)
}

// Workflows started before segregation of duties replay down the path
// they were started on: one boolean signal.
func TestWorkflowsStartedBeforeSegregationOfDutiesKeepTheirOldPath(t *testing.T) {
	env := newEnv(t)
	env.OnGetVersion("segregation-of-duties", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	env.OnActivity("CheckPolicy", mock.Anything, mock.Anything).
		Return(&workflows.PolicyCheckResult{Approved: true, RequiresApproval: true}, nil)
	env.OnActivity("SignTransaction", mock.Anything, mock.Anything).
		Return(&workflows.SignResult{SignedTx: "0xsigned", TxHash: "0xhash"}, nil)
	env.OnActivity("BroadcastTransaction", mock.Anything, "0xsigned").
		Return(&workflows.BroadcastResult{TxHash: "0xbroadcast"}, nil)
	env.OnActivity("MonitorTransaction", mock.Anything, "0xbroadcast").
		Return(&workflows.MonitorResult{BlockNumber: 5, Confirmations: 3, Success: true}, nil)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("approval", true) }, 0)
	env.ExecuteWorkflow(workflows.TransactionSettlementWorkflow, sampleReq)
	require.Equal(t, "confirmed", result(t, env).Status)
	env.AssertNotCalled(t, "OpenApprovalRequest", mock.Anything, mock.Anything)
}
