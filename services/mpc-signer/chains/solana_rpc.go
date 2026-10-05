package chains

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// SolanaRPC is a JSON-RPC client for a Solana node.
//
// Configured from SOLANA_RPC_URL, and nil when that is unset, for the same
// reason BitcoinRPC is: an unconfigured node means the Solana routes are not
// offered, which is honest, rather than a client that accepts requests and
// fails every one like an outage.
//
// Only the five calls a custody platform needs: a recent blockhash to build
// against, a balance to refuse an overdraft before a ceremony, a fee quote,
// a relay, and a status read to follow the transaction to finality.
type SolanaRPC struct {
	url        string
	commitment string
	client     *http.Client
}

// NewSolanaRPCFromEnv returns a client, or nil if no node is configured.
//
// SOLANA_COMMITMENT picks how settled a read must be (processed, confirmed,
// finalized). It defaults to "confirmed", which is what wallets use for
// balances and blockhashes: "finalized" lags by roughly 13 seconds of
// blockhash validity, which matters when the blockhash lives for about 60.
func NewSolanaRPCFromEnv() *SolanaRPC {
	url := os.Getenv("SOLANA_RPC_URL")
	if url == "" {
		return nil
	}
	commitment := os.Getenv("SOLANA_COMMITMENT")
	if commitment == "" {
		commitment = "confirmed"
	}
	return &SolanaRPC{url: url, commitment: commitment, client: &http.Client{Timeout: 30 * time.Second}}
}

// NewSolanaRPC builds a client for a given endpoint; used by tests and by
// callers that configure the endpoint themselves.
func NewSolanaRPC(url, commitment string, client *http.Client) *SolanaRPC {
	if commitment == "" {
		commitment = "confirmed"
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &SolanaRPC{url: url, commitment: commitment, client: client}
}

type solanaRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *solanaRPCError) Error() string {
	return fmt.Sprintf("solana rpc error %d: %s", e.Code, e.Message)
}

func (s *SolanaRPC) call(ctx context.Context, method string, params []interface{}, out interface{}) error {
	body, err := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("solana node unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("reading solana response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("solana node returned HTTP %d: %.200s", resp.StatusCode, raw)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *solanaRPCError `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("solana node returned an unreadable response: %w", err)
	}
	if env.Error != nil {
		return env.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// LatestBlockhash returns a blockhash to build a transaction against and the
// last block height at which it is still valid.
func (s *SolanaRPC) LatestBlockhash(ctx context.Context) (blockhash string, lastValidBlockHeight uint64, err error) {
	var r struct {
		Value struct {
			Blockhash            string `json:"blockhash"`
			LastValidBlockHeight uint64 `json:"lastValidBlockHeight"`
		} `json:"value"`
	}
	if err := s.call(ctx, "getLatestBlockhash", []interface{}{map[string]string{"commitment": s.commitment}}, &r); err != nil {
		return "", 0, err
	}
	if r.Value.Blockhash == "" {
		return "", 0, fmt.Errorf("solana node returned no blockhash")
	}
	return r.Value.Blockhash, r.Value.LastValidBlockHeight, nil
}

// Balance returns an account's lamports. An account that does not exist has
// a balance of zero, which is what the node reports.
func (s *SolanaRPC) Balance(ctx context.Context, address string) (uint64, error) {
	var r struct {
		Value uint64 `json:"value"`
	}
	if err := s.call(ctx, "getBalance", []interface{}{address, map[string]string{"commitment": s.commitment}}, &r); err != nil {
		return 0, err
	}
	return r.Value, nil
}

// FeeForMessage asks the node what a serialized message will cost. A nil
// result from the node (the blockhash expired) is an error, not a zero fee.
func (s *SolanaRPC) FeeForMessage(ctx context.Context, message []byte) (uint64, error) {
	var r struct {
		Value *uint64 `json:"value"`
	}
	if err := s.call(ctx, "getFeeForMessage", []interface{}{base64.StdEncoding.EncodeToString(message), map[string]string{"commitment": s.commitment}}, &r); err != nil {
		return 0, err
	}
	if r.Value == nil {
		return 0, fmt.Errorf("the node could not price the message; its blockhash has probably expired")
	}
	return *r.Value, nil
}

// SendTransaction relays a fully signed transaction and returns its
// signature. Preflight is left ON: the node simulates first and refuses a
// transaction that would fail, which is cheaper than finding out on chain.
func (s *SolanaRPC) SendTransaction(ctx context.Context, tx []byte) (string, error) {
	var sig string
	err := s.call(ctx, "sendTransaction", []interface{}{
		base64.StdEncoding.EncodeToString(tx),
		map[string]interface{}{"encoding": "base64", "preflightCommitment": s.commitment},
	}, &sig)
	return sig, err
}

// SolanaSignatureStatus is where a transaction stands.
type SolanaSignatureStatus struct {
	Found              bool            `json:"found"`
	Slot               uint64          `json:"slot,omitempty"`
	Confirmations      *uint64         `json:"confirmations,omitempty"`
	ConfirmationStatus string          `json:"confirmation_status,omitempty"` // processed, confirmed, finalized
	Err                json.RawMessage `json:"err,omitempty"`                 // non-null when the transaction ran and failed
}

// SignatureStatus reports a transaction's confirmation. Found=false means
// the node does not know it -- not yet landed, or expired -- which callers
// must not read as success.
func (s *SolanaRPC) SignatureStatus(ctx context.Context, signature string) (*SolanaSignatureStatus, error) {
	var r struct {
		Value []*struct {
			Slot               uint64          `json:"slot"`
			Confirmations      *uint64         `json:"confirmations"`
			Err                json.RawMessage `json:"err"`
			ConfirmationStatus string          `json:"confirmationStatus"`
		} `json:"value"`
	}
	if err := s.call(ctx, "getSignatureStatuses", []interface{}{[]string{signature}, map[string]bool{"searchTransactionHistory": true}}, &r); err != nil {
		return nil, err
	}
	if len(r.Value) == 0 || r.Value[0] == nil {
		return &SolanaSignatureStatus{Found: false}, nil
	}
	v := r.Value[0]
	st := &SolanaSignatureStatus{Found: true, Slot: v.Slot, Confirmations: v.Confirmations, ConfirmationStatus: v.ConfirmationStatus}
	if len(v.Err) > 0 && string(v.Err) != "null" {
		st.Err = v.Err
	}
	return st, nil
}
