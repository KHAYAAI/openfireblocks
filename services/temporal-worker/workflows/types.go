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
