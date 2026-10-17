package chains

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"
)

// Talking to a Bitcoin node.
//
// Bitcoin has no equivalent of an Ethereum JSON-RPC provider you can point at
// and query an address balance from. There is no balance, and a node does not
// index addresses by default -- it indexes unspent outputs. So the two things
// a custody platform needs from a node, "what can this address spend" and
// "please relay these bytes", are answered by two quite different calls with
// quite different costs.

// BitcoinRPC is a JSON-RPC client for bitcoind.
type BitcoinRPC struct {
	url    string
	user   string
	pass   string
	client *http.Client
}

// NewBitcoinRPCFromEnv returns a client, or nil if no node is configured.
//
// Nil rather than a client that fails on use: the caller has to decide what
// an unconfigured node means, and for most of this service it means the
// Bitcoin transaction routes are simply not offered. A client that accepted
// requests and failed on every one would look like an outage instead of a
// deployment that was never given a node.
func NewBitcoinRPCFromEnv() *BitcoinRPC {
	url := os.Getenv("BITCOIN_RPC_URL")
	if url == "" {
		return nil
	}
	return &BitcoinRPC{
		url:  url,
		user: os.Getenv("BITCOIN_RPC_USER"),
		pass: os.Getenv("BITCOIN_RPC_PASSWORD"),
		client: &http.Client{
			// scantxoutset walks the entire UTXO set and is slow by nature;
			// on mainnet it is tens of seconds. A short timeout here would
			// turn a working node into an unexplained failure.
			Timeout: 120 * time.Second,
		},
	}
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("bitcoind error %d: %s", e.Code, e.Message)
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (c *BitcoinRPC) call(ctx context.Context, method string, params []interface{}, out interface{}) error {
	if params == nil {
		params = []interface{}{}
	}
	body, err := json.Marshal(rpcRequest{JSONRPC: "1.0", ID: "openfireblocks", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encoding the %s request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building the %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("calling %s: %w", method, err)
	}
	defer resp.Body.Close()

	var decoded rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		// bitcoind returns a JSON body even for most errors, so a body that
		// will not decode usually means the request never reached it --
		// wrong URL, a proxy, or an auth failure answered in HTML.
		return fmt.Errorf("calling %s: HTTP %d with an undecodable body: %w", method, resp.StatusCode, err)
	}
	if decoded.Error != nil {
		return decoded.Error
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(decoded.Result, out); err != nil {
		return fmt.Errorf("decoding the %s result: %w", method, err)
	}
	return nil
}

// scanResult is the reply to scantxoutset.
type scanResult struct {
	Success  bool  `json:"success"`
	Height   int64 `json:"height"`
	Unspents []struct {
		Txid         string      `json:"txid"`
		Vout         int         `json:"vout"`
		ScriptPubKey string      `json:"scriptPubKey"`
		Amount       json.Number `json:"amount"`
		Height       int64       `json:"height"`
	} `json:"unspents"`
}

// ListUnspentForAddress returns the outputs an address can spend.
//
// Uses scantxoutset, which walks the whole UTXO set. That is the honest
// trade-off and it is worth naming: the alternative is importing a watch-only
// descriptor into a wallet at key-provisioning time, after which listunspent
// is an indexed lookup. The wallet approach is what scales -- scanning is
// seconds of CPU per call on mainnet -- but it makes the node stateful,
// requires the import to have happened before the first deposit, and silently
// returns nothing if it did not.
//
// Scanning is stateless and cannot be wrong about an address it was never
// told about, which is the property that matters more while the platform is
// small. Swapping in a descriptor wallet later changes only this method.
func (c *BitcoinRPC) ListUnspentForAddress(ctx context.Context, address string) ([]UTXO, error) {
	return c.ListUnspentForAddresses(ctx, []string{address})
}

// ListUnspentForAddresses scans for several addresses in one pass.
//
// One call rather than one per address, because the expensive part is
// walking the UTXO set and it is walked once however many descriptors are
// supplied. A threshold key can be paid at both its segwit and its legacy
// address, so reading a key's balance means asking about both.
func (c *BitcoinRPC) ListUnspentForAddresses(ctx context.Context, addresses []string) ([]UTXO, error) {
	descriptors := make([]interface{}, 0, len(addresses))
	seen := map[string]bool{}
	for _, a := range addresses {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		descriptors = append(descriptors, fmt.Sprintf("addr(%s)", a))
	}
	if len(descriptors) == 0 {
		return nil, fmt.Errorf("no address to scan for")
	}

	var res scanResult
	err := c.call(ctx, "scantxoutset", []interface{}{"start", descriptors}, &res)
	if err != nil {
		return nil, err
	}
	if !res.Success {
		return nil, fmt.Errorf("the node's UTXO scan did not complete")
	}

	utxos := make([]UTXO, 0, len(res.Unspents))
	for _, u := range res.Unspents {
		sats, err := btcToSatoshis(u.Amount.String())
		if err != nil {
			return nil, fmt.Errorf("output %s:%d: %w", u.Txid, u.Vout, err)
		}
		// The scan reports the height an output was mined at, not its depth.
		// An output in the tip block has one confirmation.
		confirmations := int64(0)
		if u.Height > 0 && res.Height >= u.Height {
			confirmations = res.Height - u.Height + 1
		}
		utxos = append(utxos, UTXO{
			Txid:          u.Txid,
			Vout:          u.Vout,
			Amount:        sats,
			Script:        u.ScriptPubKey,
			Confirmations: confirmations,
		})
	}
	return utxos, nil
}

// SendRawTransaction relays a signed transaction and returns its txid.
func (c *BitcoinRPC) SendRawTransaction(ctx context.Context, rawHex string) (string, error) {
	var txid string
	if err := c.call(ctx, "sendrawtransaction", []interface{}{rawHex}, &txid); err != nil {
		return "", err
	}
	return txid, nil
}

// EstimateFeeRate returns a fee rate in satoshis per virtual byte.
//
// fallback is used when the node cannot estimate, which is not an edge case:
// estimatesmartfee needs a history of recent blocks with real fee pressure,
// so a fresh regtest chain, a newly synced node, or a quiet period all
// return no estimate at all. Failing the whole transaction there would make
// the platform unusable on exactly the networks it gets tested on.
func (c *BitcoinRPC) EstimateFeeRate(ctx context.Context, confirmationTarget int64, fallback int64) (int64, error) {
	if confirmationTarget <= 0 {
		confirmationTarget = 6
	}
	var res struct {
		FeeRate json.Number `json:"feerate"`
		Errors  []string    `json:"errors"`
	}
	if err := c.call(ctx, "estimatesmartfee", []interface{}{confirmationTarget}, &res); err != nil {
		return 0, err
	}
	if res.FeeRate == "" {
		return fallback, nil
	}
	// estimatesmartfee reports BTC per kilo-vbyte. One BTC/kvB is 100,000
	// sat/vB, so the conversion is a factor of 100,000 rather than the
	// 100,000,000 that "BTC to satoshis" suggests.
	perKvB, err := btcToSatoshis(res.FeeRate.String())
	if err != nil {
		return 0, fmt.Errorf("decoding the fee estimate: %w", err)
	}
	rate := perKvB / 1000
	if rate < 1 {
		// Below one sat/vB nothing relays, whatever the estimator says.
		rate = 1
	}
	return rate, nil
}

// BlockCount returns the height of the node's best chain.
func (c *BitcoinRPC) BlockCount(ctx context.Context) (int64, error) {
	var height int64
	if err := c.call(ctx, "getblockcount", nil, &height); err != nil {
		return 0, err
	}
	return height, nil
}

// btcToSatoshis converts a decimal BTC amount to satoshis exactly.
//
// Deliberately not via float64. Bitcoin amounts are exact integers of
// satoshis, and JSON gives them as decimal BTC; parsing "0.1" into a float
// and multiplying by 1e8 is the classic way to be one satoshi out, which is
// enough to make a transaction unbalanced and a signature invalid.
func btcToSatoshis(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")

	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 8 {
		// More precision than a satoshi. Refusing rather than rounding: a
		// node should never send this, and silently rounding an amount is
		// how a transaction ends up unbalanced.
		return 0, fmt.Errorf("amount %q has more precision than one satoshi", s)
	}
	frac += strings.Repeat("0", 8-len(frac))

	value, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok {
		return 0, fmt.Errorf("amount %q is not a decimal number", s)
	}
	if !value.IsInt64() {
		return 0, fmt.Errorf("amount %q does not fit in an int64 of satoshis", s)
	}
	sats := value.Int64()
	if neg {
		sats = -sats
	}
	return sats, nil
}
