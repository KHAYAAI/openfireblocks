package workflows

// Shared types for the transaction settlement workflow and its activities.

// TransactionRequest is the workflow input: a tenant-scoped request to settle a
// transaction on-chain.
type TransactionRequest struct {
	CustomerID   string `json:"customerId"`
	CustomerTier string `json:"customerTier"`
	ChainID      int    `json:"chainId"`
	To           string `json:"to"`
	Data         string `json:"data"`
	Value        string `json:"value"` // wei
	GasLimit     uint64 `json:"gasLimit"`
	GasPrice     string `json:"gasPrice"`
	Nonce        uint64 `json:"nonce"`
	Country      string `json:"country"`

	// Who asked for this transfer. A user id when a person initiated it
	// through the console or a user-authenticated API call; empty when a
	// machine did, with an API key. The approval step excludes this person
	// from approving their own transfer.
	InitiatedByUserID string `json:"initiatedByUserId,omitempty"`
	// Human-readable, for the approver's screen: an email or "api-key".
	InitiatedByLabel string `json:"initiatedByLabel,omitempty"`
}

// TransactionResult is the workflow output.
type TransactionResult struct {
	RequestID   string `json:"requestId"`
	TxHash      string `json:"txHash"`
	Status      string `json:"status"` // denied | signed | broadcasted | confirmed | failed
	BlockNumber int64  `json:"blockNumber"`
	Reason      string `json:"reason"`
}

// PolicyCheckResult is returned by the CheckPolicy activity.
type PolicyCheckResult struct {
	Approved         bool     `json:"approved"`
	Denials          []string `json:"denials"`
	RequiresApproval bool     `json:"requiresApproval"`
	ApprovalReasons  []string `json:"approvalReasons"`
	Reason           string   `json:"reason"`
}

// SignResult is returned by the SignTransaction activity.
type SignResult struct {
	RequestID string `json:"requestId"`
	SignedTx  string `json:"signedTx"`
	TxHash    string `json:"txHash"`
	From      string `json:"from"`
}

// BroadcastResult is returned by the BroadcastTransaction activity.
type BroadcastResult struct {
	TxHash string `json:"txHash"`
}

// MonitorResult is returned by the MonitorTransaction activity.
type MonitorResult struct {
	BlockNumber   int64 `json:"blockNumber"`
	Confirmations int64 `json:"confirmations"`
	Success       bool  `json:"success"`
}

// ---------------------------------------------------------------------------
// Approvals
// ---------------------------------------------------------------------------

// ApprovalDecisionSignal is what the gateway sends when a person decides.
// It is sent only after the decision has been recorded in the database,
// where the rules (role, not the initiator, not expired) are enforced;
// the workflow counts again, and before signing re-reads the recorded
// outcome, so a signal on its own can never cause a signature.
type ApprovalDecisionSignal struct {
	ApproverUserID string `json:"approverUserId"`
	Decision       string `json:"decision"` // approve | reject
}

// ApprovalDecisionSignalName is the signal channel for ApprovalDecisionSignal.
const ApprovalDecisionSignalName = "approval-decision"

// ApprovalStateQueryName returns ApprovalState.
const ApprovalStateQueryName = "approval-state"

// OpenApprovalRequestInput opens (idempotently) the approval record.
type OpenApprovalRequestInput struct {
	WorkflowID string             `json:"workflowId"`
	Request    TransactionRequest `json:"request"`
	Reasons    []string           `json:"reasons"`
}

// OpenApprovalRequestResult carries the terms the request was opened with,
// fixed at that moment: a later policy change does not move them.
type OpenApprovalRequestResult struct {
	ApprovalID        string `json:"approvalId"`
	RequiredApprovals int    `json:"requiredApprovals"`
	// Seconds from now until the request expires, measured by the
	// database's clock when the request was opened.
	ExpiresInSeconds int64 `json:"expiresInSeconds"`
}

// CloseApprovalRequestInput records the workflow's view of the outcome.
type CloseApprovalRequestInput struct {
	WorkflowID string `json:"workflowId"`
	Status     string `json:"status"` // approved | rejected | expired
}

// RecordedApprovalStatus is what the database says about a request.
type RecordedApprovalStatus struct {
	Status            string `json:"status"`
	DistinctApprovals int    `json:"distinctApprovals"`
	RequiredApprovals int    `json:"requiredApprovals"`
}

// ApprovalState is returned by the approval-state query.
type ApprovalState struct {
	ApprovalID        string   `json:"approvalId"`
	Status            string   `json:"status"` // pending | approved | rejected | expired
	RequiredApprovals int      `json:"requiredApprovals"`
	ApprovedBy        []string `json:"approvedBy"`
	RejectedBy        string   `json:"rejectedBy,omitempty"`
}
