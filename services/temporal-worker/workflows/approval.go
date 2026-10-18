package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"
)

// awaitApprovals is the approval step with segregation of duties.
//
// It opens an approval record (whose terms -- how many approvals, until
// when -- are fixed by the tenant's policy at that moment), then counts
// decisions as they arrive:
//
//   - the initiator's own decision is ignored, whatever it says;
//   - each person counts once;
//   - one rejection rejects;
//   - the required number of distinct approvals approves;
//   - reaching the expiry without either expires.
//
// Approval here is necessary but not sufficient. Before returning
// "approved" it re-reads the outcome the database recorded, and both must
// agree. The database is where decisions are checked against roles and
// identities; the workflow cannot see those. A signal alone -- sent by
// anything with access to Temporal -- therefore cannot cause a signature.
func awaitApprovals(ctx workflow.Context, req TransactionRequest, reasons []string) (string, error) {
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

	var opened OpenApprovalRequestResult
	if err := workflow.ExecuteActivity(ctx, "OpenApprovalRequest", OpenApprovalRequestInput{
		WorkflowID: workflowID,
		Request:    req,
		Reasons:    reasons,
	}).Get(ctx, &opened); err != nil {
		return "", err
	}

	required := opened.RequiredApprovals
	if required < 1 {
		// Never trust a zero from anywhere: an approval step that needs no
		// approvals is not one.
		required = 1
	}

	state := ApprovalState{
		ApprovalID:        opened.ApprovalID,
		Status:            "pending",
		RequiredApprovals: required,
		ApprovedBy:        []string{},
	}
	if err := workflow.SetQueryHandler(ctx, ApprovalStateQueryName, func() (ApprovalState, error) {
		return state, nil
	}); err != nil {
		return "", err
	}

	seen := map[string]bool{}
	signals := workflow.GetSignalChannel(ctx, ApprovalDecisionSignalName)
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	expired := false
	timer := workflow.NewTimer(timerCtx, time.Duration(opened.ExpiresInSeconds)*time.Second)

	for state.Status == "pending" {
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(signals, func(c workflow.ReceiveChannel, _ bool) {
			var d ApprovalDecisionSignal
			c.Receive(ctx, &d)
			applyDecision(ctx, &state, seen, req.InitiatedByUserID, d)
		})
		selector.AddFuture(timer, func(workflow.Future) { expired = true })
		selector.Select(ctx)
		if expired && state.Status == "pending" {
			state.Status = "expired"
		}
	}
	cancelTimer()

	outcome := state.Status
	if outcome == "approved" {
		var recorded RecordedApprovalStatus
		if err := workflow.ExecuteActivity(ctx, "GetRecordedApprovalStatus", workflowID).Get(ctx, &recorded); err != nil {
			return "", err
		}
		if recorded.Status != "approved" || recorded.DistinctApprovals < required {
			// The workflow counted enough approvals and the database does
			// not agree. Something sent signals the database never
			// accepted. Refuse, loudly.
			workflow.GetLogger(ctx).Error("approval signals disagree with the recorded decisions; refusing to sign",
				"recordedStatus", recorded.Status, "recordedApprovals", recorded.DistinctApprovals, "required", required)
			outcome = "rejected"
			state.Status = "rejected"
		}
	}

	if err := workflow.ExecuteActivity(ctx, "CloseApprovalRequest", CloseApprovalRequestInput{
		WorkflowID: workflowID,
		Status:     outcome,
	}).Get(ctx, nil); err != nil {
		return "", err
	}
	return outcome, nil
}

func applyDecision(ctx workflow.Context, state *ApprovalState, seen map[string]bool, initiator string, d ApprovalDecisionSignal) {
	log := workflow.GetLogger(ctx)
	switch {
	case d.ApproverUserID == "":
		log.Warn("approval decision without an approver; ignored")
		return
	case initiator != "" && d.ApproverUserID == initiator:
		log.Warn("the initiator's own decision was ignored (segregation of duties)", "user", d.ApproverUserID)
		return
	case seen[d.ApproverUserID]:
		return // each person decides once
	}
	seen[d.ApproverUserID] = true

	switch d.Decision {
	case "reject":
		state.RejectedBy = d.ApproverUserID
		state.Status = "rejected"
	case "approve":
		state.ApprovedBy = append(state.ApprovedBy, d.ApproverUserID)
		if len(state.ApprovedBy) >= state.RequiredApprovals {
			state.Status = "approved"
		}
	default:
		log.Warn(fmt.Sprintf("unknown approval decision %q; ignored", d.Decision))
		delete(seen, d.ApproverUserID)
	}
}
