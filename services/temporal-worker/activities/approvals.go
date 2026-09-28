package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"forge-crypto/temporal-worker/workflows"
)

// The approval record lives in Postgres, beside the rules that govern it
// (migration 023): who may decide, that the initiator may not, that each
// person decides once, and that approved means enough distinct people
// said so. These activities open that record, read back what it decided,
// and close it on expiry. They never write a decision -- only a person,
// through the gateway, does that.

var errNoDatabase = errors.New("approvals need the database (DATABASE_URL); refusing to proceed without one")

// OpenApprovalRequest opens the approval record for a settlement, with
// terms taken from the tenant's approval policy at this moment. Idempotent
// on the workflow id: an activity retry returns the record the first
// attempt opened, with the same terms.
//
// Without a database this fails rather than skipping approval: a transfer
// the policy said needs approval is never signed because the approval
// step could not run.
func (a *Activities) OpenApprovalRequest(ctx context.Context, in workflows.OpenApprovalRequestInput) (*workflows.OpenApprovalRequestResult, error) {
	if a.db == nil {
		return nil, errNoDatabase
	}
	summary, err := json.Marshal(map[string]interface{}{
		"chainId":  in.Request.ChainID,
		"to":       in.Request.To,
		"valueWei": in.Request.Value,
		"data":     in.Request.Data,
		"country":  in.Request.Country,
		"reasons":  in.Reasons,
	})
	if err != nil {
		return nil, err
	}

	var initiator interface{}
	if in.Request.InitiatedByUserID != "" {
		initiator = in.Request.InitiatedByUserID
	}
	label := in.Request.InitiatedByLabel
	if label == "" {
		label = "api-key"
	}

	// Defaults when the tenant has set no policy: two approvals (dual
	// control) within an hour.
	if _, err := a.db.ExecContext(ctx, `
		INSERT INTO approval_requests
		  (customer_id, workflow_id, initiated_by_user_id, initiated_by_label,
		   required_approvals, summary, expires_at)
		SELECT $1::uuid, $2, $3::uuid, $4,
		       COALESCE(p.required_approvals, 2), $5::jsonb,
		       NOW() + make_interval(mins => COALESCE(p.window_minutes, 60))
		  FROM (SELECT 1) AS one
		  LEFT JOIN approval_policies p ON p.customer_id = $1::uuid
		ON CONFLICT (workflow_id) DO NOTHING`,
		in.Request.CustomerID, in.WorkflowID, initiator, label, string(summary)); err != nil {
		return nil, fmt.Errorf("opening the approval request: %w", err)
	}

	var out workflows.OpenApprovalRequestResult
	var expiresIn float64
	if err := a.db.QueryRowContext(ctx, `
		SELECT approval_id::text, required_approvals,
		       GREATEST(EXTRACT(EPOCH FROM expires_at - NOW()), 0)
		  FROM approval_requests WHERE workflow_id = $1`, in.WorkflowID,
	).Scan(&out.ApprovalID, &out.RequiredApprovals, &expiresIn); err != nil {
		return nil, fmt.Errorf("reading the approval request back: %w", err)
	}
	out.ExpiresInSeconds = int64(expiresIn)
	return &out, nil
}

// GetRecordedApprovalStatus reads the outcome the database recorded,
// counting approvals itself rather than trusting the status column alone.
func (a *Activities) GetRecordedApprovalStatus(ctx context.Context, workflowID string) (*workflows.RecordedApprovalStatus, error) {
	if a.db == nil {
		return nil, errNoDatabase
	}
	var out workflows.RecordedApprovalStatus
	err := a.db.QueryRowContext(ctx, `
		SELECT r.status, r.required_approvals,
		       (SELECT COUNT(DISTINCT d.user_id) FROM approval_decisions d
		         WHERE d.approval_id = r.approval_id AND d.decision = 'approve'
		           AND d.user_id IS DISTINCT FROM r.initiated_by_user_id)
		  FROM approval_requests r WHERE r.workflow_id = $1`, workflowID,
	).Scan(&out.Status, &out.RequiredApprovals, &out.DistinctApprovals)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no approval request recorded for %s", workflowID)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseApprovalRequest records an outcome the decisions did not.
// Approval and an approver's rejection are recorded by the decisions
// themselves (the database moves the status when the deciding row is
// written). What is left is expiry, and the workflow refusing because the
// signals it received disagree with the record -- both close a request
// that is still pending.
func (a *Activities) CloseApprovalRequest(ctx context.Context, in workflows.CloseApprovalRequestInput) error {
	if a.db == nil {
		return errNoDatabase
	}
	if in.Status != "expired" && in.Status != "rejected" {
		return nil
	}
	_, err := a.db.ExecContext(ctx, `
		UPDATE approval_requests SET status = $2, decided_at = NOW()
		 WHERE workflow_id = $1 AND status = 'pending'`, in.WorkflowID, in.Status)
	return err
}
