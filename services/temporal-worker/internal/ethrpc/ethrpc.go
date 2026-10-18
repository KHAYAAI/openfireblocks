// Package ethrpc is the Ethereum JSON-RPC calls this platform makes.
//
// It replaces go-ethereum's ethclient, which was pulling in the entire
// node implementation -- consensus, state, the EVM, a database layer -- to
// make seven HTTP requests. The methods here are those seven and nothing
// else, which is both the licensing point (see docs/LICENSING.md) and a
// reasonable design point on its own.
//
// Errors distinguish three situations that callers genuinely need to tell
// apart: the node could not be reached, the node answered with an error,
// and the node answered "no such thing". A transaction with no receipt is
// the third of those, and treating it as the first would make a pending
// transaction look like an outage.
package ethrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// ErrNotFound is returned when the node has no record of what was asked
// for -- most often a receipt for a transaction that is still pending.
//
// A sentinel rather than a string match: "not found" is a normal state in
// the confirmation loop, and a caller distinguishing it by message would
// break the first time a node phrased it differently.
var ErrNotFound = errors.New("not found")

// Client is a JSON-RPC client for one endpoint.
type Client struct {
	url  string
	http *http.Client
}

// Dial returns a client for an endpoint. It performs no I/O: there is
// nothing to connect, and a constructor that reached out to the network
// would make every service's startup depend on a node being up.
func Dial(url string) (*Client, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("an Ethereum RPC endpoint must be an http(s) URL, got %q", url)
	}
	return &Client{
		url: url,
		// A bounded timeout, because an unresponsive node must not hold a
		// settlement request open indefinitely.
		http: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (c *Client) call(ctx context.Context, method string, params ...interface{}) (json.RawMessage, error) {
	if params == nil {
		params = []interface{}{}
	}
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: 1})
	if err != nil {
		return nil, fmt.Errorf("encoding %s request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: the Ethereum node could not be reached: %w", method, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: reading the node's reply: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: the node returned HTTP %d: %s",
			method, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out rpcResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: the node's reply is not JSON-RPC: %w", method, err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%s: the node rejected the request: %s (code %d)",
			method, out.Error.Message, out.Error.Code)
	}
	// A JSON null result means "no such thing", not an error. eth_getTransactionReceipt
	// answers that way for every transaction that has not been mined yet.
	if len(out.Result) == 0 || string(out.Result) == "null" {
		return nil, ErrNotFound
	}
	return out.Result, nil
}

func (c *Client) hexBig(ctx context.Context, method string, params ...interface{}) (*big.Int, error) {
	raw, err := c.call(ctx, method, params...)
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: expected a quantity, got %s", method, raw)
	}
	v, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("%s: %q is not a hex quantity", method, s)
	}
	return v, nil
}

// ChainID is the chain the endpoint is serving.
//
// Worth checking against what the caller expected. An endpoint quietly
// pointed at the wrong network accepts transactions, reports balances and
// returns receipts -- all for a chain the customer's money is not on.
func (c *Client) ChainID(ctx context.Context) (*big.Int, error) {
	return c.hexBig(ctx, "eth_chainId")
}

// BlockNumber is the height of the chain's head, used to count
// confirmations.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	v, err := c.hexBig(ctx, "eth_blockNumber")
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// BalanceAt returns an account's balance in wei at the latest block.
func (c *Client) BalanceAt(ctx context.Context, address string) (*big.Int, error) {
	return c.hexBig(ctx, "eth_getBalance", address, "latest")
}

// PendingNonceAt returns the next nonce, counting transactions already in
// the mempool.
//
// "pending", not "latest": a second transaction built on the latest
// confirmed nonce while a first is still pending would reuse that nonce
// and replace the first rather than following it.
func (c *Client) PendingNonceAt(ctx context.Context, address string) (uint64, error) {
	v, err := c.hexBig(ctx, "eth_getTransactionCount", address, "pending")
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// SuggestGasPrice asks the node what a transaction should pay.
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return c.hexBig(ctx, "eth_gasPrice")
}

// EstimateGas asks the node how much gas a call would consume.
func (c *Client) EstimateGas(ctx context.Context, from, to, data, value string) (uint64, error) {
	call := map[string]string{"from": from, "to": to}
	if data != "" {
		call["data"] = data
	}
	if value != "" {
		call["value"] = value
	}
	v, err := c.hexBig(ctx, "eth_estimateGas", call, "latest")
	if err != nil {
		return 0, err
	}
	return v.Uint64(), nil
}

// SendRawTransaction broadcasts signed bytes and returns the node's hash
// for them.
//
// The hash is returned rather than computed so that a disagreement is
// visible. It should always equal the Keccak hash of the bytes sent; a
// node that says otherwise has done something to the transaction, and the
// caller is better off knowing than assuming.
func (c *Client) SendRawTransaction(ctx context.Context, rawHex string) (string, error) {
	if !strings.HasPrefix(rawHex, "0x") {
		rawHex = "0x" + rawHex
	}
	raw, err := c.call(ctx, "eth_sendRawTransaction", rawHex)
	if err != nil {
		return "", err
	}
	var hash string
	if err := json.Unmarshal(raw, &hash); err != nil {
		return "", fmt.Errorf("eth_sendRawTransaction: expected a hash, got %s", raw)
	}
	return hash, nil
}

// Receipt is the part of a transaction receipt this platform reads.
type Receipt struct {
	// Status is 1 for success and 0 for a transaction that was mined and
	// reverted. The distinction matters: a reverted transaction is on
	// chain, paid its gas, and moved nothing.
	Status      uint64
	BlockNumber uint64
	GasUsed     uint64
	TxHash      string
}

type rawReceipt struct {
	Status      string `json:"status"`
	BlockNumber string `json:"blockNumber"`
	GasUsed     string `json:"gasUsed"`
	TxHash      string `json:"transactionHash"`
}

// TransactionReceipt returns a mined transaction's receipt, or ErrNotFound
// while it is still pending.
func (c *Client) TransactionReceipt(ctx context.Context, txHash string) (*Receipt, error) {
	raw, err := c.call(ctx, "eth_getTransactionReceipt", txHash)
	if err != nil {
		return nil, err
	}
	var r rawReceipt
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("eth_getTransactionReceipt: unreadable receipt: %w", err)
	}
	return &Receipt{
		Status:      hexUint(r.Status),
		BlockNumber: hexUint(r.BlockNumber),
		GasUsed:     hexUint(r.GasUsed),
		TxHash:      r.TxHash,
	}, nil
}

// Call performs a read-only contract call.
func (c *Client) Call(ctx context.Context, to, data string) (string, error) {
	raw, err := c.call(ctx, "eth_call", map[string]string{"to": to, "data": data}, "latest")
	if err != nil {
		return "", err
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("eth_call: expected hex data, got %s", raw)
	}
	return out, nil
}

func hexUint(s string) uint64 {
	v, ok := new(big.Int).SetString(strings.TrimPrefix(s, "0x"), 16)
	if !ok {
		return 0
	}
	return v.Uint64()
}
