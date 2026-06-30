package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TransactionSettlementWorkflow orchestrates the full settlement lifecycle:
//
//	policy check -> MPC sign -> broadcast -> monitor confirmations
//
// It is durable and fault-tolerant: each step is a retryable Temporal activity,
// so a crash or transient failure resumes from the last completed step rather
// than re-signing or double-broadcasting.
//
// Activities are referenced by name to keep this package free of an import
// cycle with the activities package; the worker registers the Activities struct
// whose method names match these strings.
func TransactionSettlementWorkflow(ctx workflow.Context, req TransactionRequest) (*TransactionResult, error) {
	logger := workflow.GetLogger(ctx)

	// Idempotent identifier for this settlement (stable across retries).
	requestID := workflow.GetInfo(ctx).WorkflowExecution.ID

	ao := workflow.ActivityOptions{
		StartToCloseTimeout:    2 * time.Minute,
		ScheduleToCloseTimeout: 15 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    5,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Step 1: policy evaluation. A denial is a terminal, successful workflow
	// outcome (not an error) — the transaction was correctly refused.
	var policy PolicyCheckResult
	if err := workflow.ExecuteActivity(ctx, "CheckPolicy", req).Get(ctx, &policy); err != nil {
		return nil, err
	}
	if !policy.Approved {
		logger.Info("transaction denied by policy", "denials", policy.Denials)
		return &TransactionResult{
			RequestID: requestID,
			Status:    "denied",
			Reason:    policy.Reason,
		}, nil
	}

	// Step 1b: high-value transactions wait for a human approval signal before
	// signing. The api-gateway/admin sends an "approval" signal; if none arrives
	// within the window the workflow ends as denied.
	if policy.RequiresApproval {
		approved, err := waitForApproval(ctx)
		if err != nil {
			return nil, err
		}
		if !approved {
			return &TransactionResult{
				RequestID: requestID,
				Status:    "denied",
				Reason:    "manual approval not granted within window",
			}, nil
		}
	}

	// Step 2: MPC sign.
	var signed SignResult
	if err := workflow.ExecuteActivity(ctx, "SignTransaction", req).Get(ctx, &signed); err != nil {
		return nil, err
	}

	// Step 3: broadcast.
	var broadcast BroadcastResult
	if err := workflow.ExecuteActivity(ctx, "BroadcastTransaction", signed.SignedTx).Get(ctx, &broadcast); err != nil {
		return &TransactionResult{
			RequestID: requestID,
			TxHash:    signed.TxHash,
			Status:    "signed",
			Reason:    "signed but broadcast failed: " + err.Error(),
		}, err
	}

	// Step 4: monitor confirmations.
	var monitor MonitorResult
	if err := workflow.ExecuteActivity(ctx, "MonitorTransaction", broadcast.TxHash).Get(ctx, &monitor); err != nil {
		return &TransactionResult{
			RequestID: requestID,
			TxHash:    broadcast.TxHash,
			Status:    "broadcasted",
			Reason:    "broadcast ok but monitoring failed: " + err.Error(),
		}, err
	}

	status := "confirmed"
	if !monitor.Success {
		status = "failed"
	}
	return &TransactionResult{
		RequestID:   requestID,
		TxHash:      broadcast.TxHash,
		Status:      status,
		BlockNumber: monitor.BlockNumber,
		Reason:      "settled",
	}, nil
}

// waitForApproval blocks until an "approval" signal arrives or the approval
// window elapses. The signal payload is a bool (true = approve, false = reject).
func waitForApproval(ctx workflow.Context) (bool, error) {
	const approvalWindow = time.Hour

	var approved bool
	signalReceived := false

	selector := workflow.NewSelector(ctx)
	ch := workflow.GetSignalChannel(ctx, "approval")
	selector.AddReceive(ch, func(c workflow.ReceiveChannel, _ bool) {
		c.Receive(ctx, &approved)
		signalReceived = true
	})

	timer := workflow.NewTimer(ctx, approvalWindow)
	selector.AddFuture(timer, func(workflow.Future) {
		// timeout: leave approved=false
	})

	selector.Select(ctx)
	_ = signalReceived
	return approved, nil
}
