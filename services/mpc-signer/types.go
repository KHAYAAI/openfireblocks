package main

// types.go defines the request/response contract for the MPC signer's HTTP API.
// These mirror the DTOs used by the NestJS api-gateway so the two services
// can communicate over JSON without a shared schema registry (Phase 0).

// SignRequest is an unsigned Ethereum transaction the caller wants signed.
type SignRequest struct {
	ChainID  int    `json:"chainId"`  // 11155111 for Sepolia, 1 for mainnet
	To       string `json:"to"`       // 0x-prefixed recipient address
	Data     string `json:"data"`     // 0x-prefixed call data, or "" / "0x" for a plain transfer
	Value    string `json:"value"`    // amount in wei as a base-10 string ("0" allowed)
	GasLimit uint64 `json:"gasLimit"` // gas units, >= 21000
	GasPrice string `json:"gasPrice"` // wei per gas as a base-10 string
	Nonce    uint64 `json:"nonce"`    // sender account nonce
}

// SignResponse is returned on a successful signing.
type SignResponse struct {
	RequestID  string `json:"requestId"`  // UUID for correlating audit events
	SignedTx   string `json:"signedTx"`   // 0x-prefixed RLP-encoded signed transaction
	TxHash     string `json:"txHash"`     // 0x-prefixed transaction hash (keccak256 of the signed tx)
	From       string `json:"from"`       // signer address derived from the shared key
	Status     string `json:"status"`     // always "signed" on success
	AuditLogID uint64 `json:"auditLogId"` // immudb transaction id of the audit record
}

// ErrorResponse is returned for any failure so the caller always gets a request id.
type ErrorResponse struct {
	Error      string `json:"error"`
	RequestID  string `json:"requestId"`
	AuditLogID uint64 `json:"auditLogId,omitempty"`
}
