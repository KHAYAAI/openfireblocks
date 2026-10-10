package chains

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// CosmosNode talks to a Cosmos SDK chain's REST (LCD) endpoint.
//
// REST rather than Tendermint RPC or gRPC: it is JSON over HTTP with the same
// shape on every SDK chain, needs no generated client, and is what the other
// chains here use. Configured from COSMOS_LCD_URL; nil when unset, so the
// Cosmos routes are simply not offered on a deployment with no node.
type CosmosNode struct {
	url    string
	client *http.Client

	// ChainID overrides what the node reports. Signing for the wrong chain id
	// produces a transaction that is rejected after a ceremony, so a
	// deployment can pin it (COSMOS_CHAIN_ID) instead of trusting the node.
	ChainID string
	// FeeDenom, GasPrice and GasLimit price a MsgSend. A bank send costs far
	// less than the default limit; the surplus is not charged, only the fee
	// computed from the limit is, so the default errs towards acceptance.
	FeeDenom string
	GasPrice string // decimal per gas unit, e.g. "0.025"
	GasLimit uint64
	// Bech32Prefix is the chain's address prefix: cosmos, osmo, ...
	Bech32Prefix string
}

// NewCosmosNodeFromEnv returns a client, or nil if COSMOS_LCD_URL is unset.
func NewCosmosNodeFromEnv() *CosmosNode {
	u := os.Getenv("COSMOS_LCD_URL")
	if u == "" {
		return nil
	}
	n := &CosmosNode{
		url:          strings.TrimRight(u, "/"),
		client:       &http.Client{Timeout: 30 * time.Second},
		ChainID:      os.Getenv("COSMOS_CHAIN_ID"),
		FeeDenom:     envOr("COSMOS_FEE_DENOM", "uatom"),
		GasPrice:     envOr("COSMOS_GAS_PRICE", "0.025"),
		Bech32Prefix: envOr("COSMOS_BECH32_PREFIX", DefaultCosmosPrefix),
		GasLimit:     200_000,
	}
	if g, err := strconv.ParseUint(os.Getenv("COSMOS_GAS_LIMIT"), 10, 64); err == nil && g > 0 {
		n.GasLimit = g
	}
	return n
}

// NewCosmosNode builds a node client for a given endpoint (tests, or callers
// that configure it themselves).
func NewCosmosNode(lcdURL string, client *http.Client) *CosmosNode {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &CosmosNode{url: strings.TrimRight(lcdURL, "/"), client: client, FeeDenom: "uatom", GasPrice: "0.025", GasLimit: 200_000, Bech32Prefix: DefaultCosmosPrefix}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ErrCosmosNotFound is returned for a 404: an account or transaction the node
// has no record of. Distinct from a failure, because an unfunded address is a
// normal state that a caller must explain, not an outage.
var ErrCosmosNotFound = fmt.Errorf("not found")

func (n *CosmosNode) do(ctx context.Context, method, path string, body []byte, out interface{}) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, n.url+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("cosmos node unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrCosmosNotFound
	}
	if resp.StatusCode != http.StatusOK {
		// The SDK reports a missing account as 404 on some versions and as a
		// 500 whose message says "not found" on others.
		if strings.Contains(strings.ToLower(string(raw)), "not found") {
			return ErrCosmosNotFound
		}
		return fmt.Errorf("cosmos node returned HTTP %d: %.200s", resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("cosmos node returned an unreadable response: %w", err)
	}
	return nil
}

// CosmosAccount is the part of an account a signature depends on.
type CosmosAccount struct {
	AccountNumber uint64
	Sequence      uint64
}

type lcdBaseAccount struct {
	AccountNumber string `json:"account_number"`
	Sequence      string `json:"sequence"`
}

// Account reads the account number and sequence. A vesting or module-wrapped
// account nests the base account; both shapes are handled. ErrCosmosNotFound
// means the address has never been funded.
func (n *CosmosNode) Account(ctx context.Context, address string) (*CosmosAccount, error) {
	var r struct {
		Account struct {
			lcdBaseAccount
			BaseAccount        *lcdBaseAccount `json:"base_account"`
			BaseVestingAccount *struct {
				BaseAccount lcdBaseAccount `json:"base_account"`
			} `json:"base_vesting_account"`
		} `json:"account"`
	}
	if err := n.do(ctx, http.MethodGet, "/cosmos/auth/v1beta1/accounts/"+url.PathEscape(address), nil, &r); err != nil {
		return nil, err
	}
	base := r.Account.lcdBaseAccount
	if r.Account.BaseAccount != nil {
		base = *r.Account.BaseAccount
	} else if r.Account.BaseVestingAccount != nil {
		base = r.Account.BaseVestingAccount.BaseAccount
	}
	num, err1 := strconv.ParseUint(base.AccountNumber, 10, 64)
	seq, err2 := strconv.ParseUint(orZero(base.Sequence), 10, 64)
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("the node returned an account without a usable number and sequence")
	}
	return &CosmosAccount{AccountNumber: num, Sequence: seq}, nil
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// Balance returns the base-unit balance of one denomination, "0" if none.
func (n *CosmosNode) Balance(ctx context.Context, address, denom string) (string, error) {
	var r struct {
		Balance struct {
			Amount string `json:"amount"`
		} `json:"balance"`
	}
	q := url.Values{"denom": {denom}}
	if err := n.do(ctx, http.MethodGet, "/cosmos/bank/v1beta1/balances/"+url.PathEscape(address)+"/by_denom?"+q.Encode(), nil, &r); err != nil {
		if err == ErrCosmosNotFound {
			return "0", nil
		}
		return "", err
	}
	if r.Balance.Amount == "" {
		return "0", nil
	}
	return r.Balance.Amount, nil
}

// Network returns the chain id the node reports.
func (n *CosmosNode) Network(ctx context.Context) (string, error) {
	var r struct {
		DefaultNodeInfo struct {
			Network string `json:"network"`
		} `json:"default_node_info"`
	}
	if err := n.do(ctx, http.MethodGet, "/cosmos/base/tendermint/v1beta1/node_info", nil, &r); err != nil {
		return "", err
	}
	if r.DefaultNodeInfo.Network == "" {
		return "", fmt.Errorf("the node did not report a chain id")
	}
	return r.DefaultNodeInfo.Network, nil
}

// ResolveChainID returns the pinned chain id, or asks the node. If both are
// available and disagree it refuses: signing for the wrong chain wastes a
// ceremony and, on a chain id that is valid elsewhere, is replayable there.
func (n *CosmosNode) ResolveChainID(ctx context.Context) (string, error) {
	reported, err := n.Network(ctx)
	if n.ChainID == "" {
		return reported, err
	}
	if err == nil && reported != n.ChainID {
		return "", fmt.Errorf("COSMOS_CHAIN_ID is %q but the node reports %q", n.ChainID, reported)
	}
	return n.ChainID, nil
}

// Fee computes ceil(gasLimit * gasPrice) in base units.
func (n *CosmosNode) Fee() (string, error) {
	price, ok := new(big.Rat).SetString(n.GasPrice)
	if !ok || price.Sign() < 0 {
		return "", fmt.Errorf("COSMOS_GAS_PRICE %q is not a decimal", n.GasPrice)
	}
	total := new(big.Rat).Mul(price, new(big.Rat).SetUint64(n.GasLimit))
	q, m := new(big.Int).QuoRem(total.Num(), total.Denom(), new(big.Int))
	if m.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if q.Sign() == 0 {
		q.SetInt64(1)
	}
	return q.String(), nil
}

// CosmosBroadcastResult is the node's answer to a broadcast.
type CosmosBroadcastResult struct {
	TxHash string `json:"txhash"`
	Code   uint32 `json:"code"`
	RawLog string `json:"raw_log"`
}

// Broadcast relays a TxRaw in sync mode, which returns after CheckTx. A
// non-zero code is a rejection and is returned as an error carrying the
// node's log; a zero code means accepted into the mempool, not included in a
// block -- follow it with TxStatus.
func (n *CosmosNode) Broadcast(ctx context.Context, txRaw []byte) (*CosmosBroadcastResult, error) {
	body, _ := json.Marshal(map[string]string{"tx_bytes": base64.StdEncoding.EncodeToString(txRaw), "mode": "BROADCAST_MODE_SYNC"})
	var r struct {
		TxResponse CosmosBroadcastResult `json:"tx_response"`
	}
	if err := n.do(ctx, http.MethodPost, "/cosmos/tx/v1beta1/txs", body, &r); err != nil {
		return nil, err
	}
	if r.TxResponse.Code != 0 {
		return &r.TxResponse, fmt.Errorf("the chain rejected the transaction (code %d): %s", r.TxResponse.Code, r.TxResponse.RawLog)
	}
	return &r.TxResponse, nil
}

// CosmosTxStatus is where a transaction stands.
type CosmosTxStatus struct {
	// Send is the first bank MsgSend in the transaction, when there is one.
	Send   *CosmosSendInfo `json:"send,omitempty"`
	Found  bool            `json:"found"`
	Height string          `json:"height,omitempty"`
	Code   uint32          `json:"code,omitempty"` // non-zero: included and failed
	RawLog string          `json:"raw_log,omitempty"`
	TxHash string          `json:"txhash,omitempty"`
}

// TxStatus looks a transaction up by hash. Found=false means the node has not
// seen it in a block, which is the normal state just after a broadcast and
// must not be read as failure or as success.
// CosmosSendInfo is what an included MsgSend moved.
type CosmosSendInfo struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

func (n *CosmosNode) TxStatus(ctx context.Context, hash string) (*CosmosTxStatus, error) {
	var r struct {
		Tx struct {
			Body struct {
				Messages []struct {
					Type        string `json:"@type"`
					FromAddress string `json:"from_address"`
					ToAddress   string `json:"to_address"`
					Amount      []struct {
						Denom  string `json:"denom"`
						Amount string `json:"amount"`
					} `json:"amount"`
				} `json:"messages"`
			} `json:"body"`
		} `json:"tx"`
		TxResponse struct {
			Height string `json:"height"`
			TxHash string `json:"txhash"`
			Code   uint32 `json:"code"`
			RawLog string `json:"raw_log"`
		} `json:"tx_response"`
	}
	if err := n.do(ctx, http.MethodGet, "/cosmos/tx/v1beta1/txs/"+url.PathEscape(hash), nil, &r); err != nil {
		if err == ErrCosmosNotFound {
			return &CosmosTxStatus{Found: false}, nil
		}
		return nil, err
	}
	st := &CosmosTxStatus{Found: true, Height: r.TxResponse.Height, Code: r.TxResponse.Code, RawLog: r.TxResponse.RawLog, TxHash: r.TxResponse.TxHash}
	for _, m := range r.Tx.Body.Messages {
		if m.Type == msgSendTypeURL && len(m.Amount) > 0 {
			st.Send = &CosmosSendInfo{From: m.FromAddress, To: m.ToAddress, Denom: m.Amount[0].Denom, Amount: m.Amount[0].Amount}
			break
		}
	}
	return st, nil
}
