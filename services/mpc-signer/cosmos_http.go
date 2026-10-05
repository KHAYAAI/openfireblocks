package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcutil/bech32"

	"forge-crypto/mpc-signer/chains"
)

// The Cosmos spend path, in the same prepare / threshold-sign / finalize
// shape as Bitcoin and Solana. See solana_http.go for why the split exists.
//
// Bank sends of a single denomination only. Staking, IBC transfers and
// CosmWasm calls are different messages with different failure modes and are
// not offered rather than offered half-working.

const maxCosmosMemo = 256 // the SDK's default max_memo_characters

type cosmosPrepareRequest struct {
	// PubKeyHex is the threshold key's secp256k1 public key, 33 or 65 bytes.
	PubKeyHex   string `json:"pubkey_hex"`
	Destination string `json:"destination"`
	// Amount in base units (uatom, not ATOM), a base-10 string.
	Amount string `json:"amount"`
	// Denom defaults to the node's fee denom.
	Denom string `json:"denom,omitempty"`
	Memo  string `json:"memo,omitempty"`
}

type cosmosPrepareResponse struct {
	Plan     *chains.CosmosSigningPlan `json:"plan"`
	From     string                    `json:"from"`
	To       string                    `json:"to"`
	Amount   string                    `json:"amount"`
	Denom    string                    `json:"denom"`
	Fee      string                    `json:"fee"`
	FeeDenom string                    `json:"fee_denom"`
	GasLimit uint64                    `json:"gas_limit"`
	Balance  string                    `json:"balance"`
}

type cosmosFinalizeRequest struct {
	Plan      *chains.CosmosSigningPlan `json:"plan"`
	R         string                    `json:"r"`
	S         string                    `json:"s"`
	PubKeyHex string                    `json:"pubkey_hex"`
	Broadcast bool                      `json:"broadcast"`
}

type cosmosFinalizeResponse struct {
	TxHash      string `json:"txhash"`
	RawTxBase64 string `json:"raw_tx_base64"`
	Broadcast   bool   `json:"broadcast"`
}

func (s *server) cosmosNode() *chains.CosmosNode {
	signer, ok := s.signerRouter.Signer("cosmos-hub").(*chains.CosmosSigner)
	if !ok {
		return nil
	}
	return signer.Node()
}

func errNoCosmosNode(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "no Cosmos node is configured (set COSMOS_LCD_URL)"})
}

func cosmosAddressFromHex(pubKeyHex, prefix string) (string, []byte, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(pubKeyHex, "0x"))
	if err != nil {
		return "", nil, fmt.Errorf("pubkey_hex is not valid hex")
	}
	pub, err := btcec.ParsePubKey(raw)
	if err != nil {
		return "", nil, fmt.Errorf("pubkey_hex is not a secp256k1 public key")
	}
	compressed := pub.SerializeCompressed()
	addr, err := chains.CosmosAddressFromPubKey(compressed, prefix)
	return addr, compressed, err
}

// validCosmosAddress accepts a bech32 address with the chain's prefix and a
// 20-byte payload. A valid address for ANOTHER chain is refused: the
// checksum passes, the funds would be sent to an address the destination
// chain does not recognise as the intended recipient's.
func validCosmosAddress(addr, prefix string) error {
	hrp, data, err := bech32.Decode(addr)
	if err != nil {
		return fmt.Errorf("destination is not a valid bech32 address")
	}
	if hrp != prefix {
		return fmt.Errorf("destination is a %q address; this chain uses %q", hrp, prefix)
	}
	payload, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil || len(payload) != 20 {
		return fmt.Errorf("destination is not a 20-byte account address")
	}
	return nil
}

func (s *server) handleCosmosAddresses(w http.ResponseWriter, r *http.Request) {
	prefix := chains.DefaultCosmosPrefix
	if n := s.cosmosNode(); n != nil {
		prefix = n.Bech32Prefix
	}
	if p := r.URL.Query().Get("prefix"); p != "" {
		prefix = p
	}
	addr, _, err := cosmosAddressFromHex(r.URL.Query().Get("pubkey"), prefix)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"address": addr, "preferred": addr, "prefix": prefix})
}

func (s *server) handleCosmosBalance(w http.ResponseWriter, r *http.Request) {
	node := s.cosmosNode()
	if node == nil {
		errNoCosmosNode(w)
		return
	}
	addr, denom := r.URL.Query().Get("address"), r.URL.Query().Get("denom")
	if denom == "" {
		denom = node.FeeDenom
	}
	if err := validCosmosAddress(addr, node.Bech32Prefix); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	bal, err := node.Balance(r.Context(), addr, denom)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"address": addr, "denom": denom, "amount": bal})
}

func (s *server) handleCosmosPrepare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req cosmosPrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}
	node := s.cosmosNode()
	if node == nil {
		errNoCosmosNode(w)
		return
	}
	bad := func(msg string) { writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": msg}) }
	gateway := func(format string, a ...interface{}) {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": fmt.Sprintf(format, a...)})
	}

	from, pub, err := cosmosAddressFromHex(req.PubKeyHex, node.Bech32Prefix)
	if err != nil {
		bad(err.Error())
		return
	}
	if err := validCosmosAddress(req.Destination, node.Bech32Prefix); err != nil {
		bad(err.Error())
		return
	}
	if len(req.Memo) > maxCosmosMemo {
		bad(fmt.Sprintf("memo is longer than %d characters", maxCosmosMemo))
		return
	}
	denom := req.Denom
	if denom == "" {
		denom = node.FeeDenom
	}
	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		bad("amount must be a positive integer number of base units, as a string")
		return
	}

	acct, err := node.Account(ctx, from)
	if errors.Is(err, chains.ErrCosmosNotFound) {
		bad(fmt.Sprintf("%s has never been funded, so it has no account on this chain yet", from))
		return
	}
	if err != nil {
		gateway("could not read the account: %v", err)
		return
	}
	chainID, err := node.ResolveChainID(ctx)
	if err != nil {
		gateway("%v", err)
		return
	}
	fee, err := node.Fee()
	if err != nil {
		gateway("%v", err)
		return
	}

	// Refused before the ceremony: an overdraft is only discovered at
	// CheckTx, after the parties have signed.
	feeInt, _ := new(big.Int).SetString(fee, 10)
	balStr, err := node.Balance(ctx, from, denom)
	if err != nil {
		gateway("could not read the balance: %v", err)
		return
	}
	bal, _ := new(big.Int).SetString(balStr, 10)
	need := new(big.Int).Set(amount)
	if denom == node.FeeDenom {
		need.Add(need, feeInt)
	} else {
		feeBal, err := node.Balance(ctx, from, node.FeeDenom)
		if err != nil {
			gateway("could not read the fee balance: %v", err)
			return
		}
		if fb, _ := new(big.Int).SetString(feeBal, 10); fb.Cmp(feeInt) < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"error": fmt.Sprintf("insufficient %s for the fee: need %s, have %s", node.FeeDenom, fee, feeBal)})
			return
		}
	}
	if bal.Cmp(need) < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   fmt.Sprintf("insufficient %s: need %s (amount %s + fee %s where the fee is in this denom), have %s", denom, need, amount, fee, balStr),
			"balance": balStr,
		})
		return
	}

	plan, err := chains.PlanCosmosSend(chains.CosmosSend{
		ChainID: chainID, AccountNumber: acct.AccountNumber, Sequence: acct.Sequence,
		From: from, To: req.Destination, Denom: denom, Amount: amount.String(),
		FeeDenom: node.FeeDenom, FeeAmount: fee, GasLimit: node.GasLimit, Memo: req.Memo, PubKey: pub,
	})
	if err != nil {
		bad(err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cosmosPrepareResponse{
		Plan: plan, From: from, To: req.Destination, Amount: amount.String(), Denom: denom,
		Fee: fee, FeeDenom: node.FeeDenom, GasLimit: node.GasLimit, Balance: balStr,
	})
}

func (s *server) handleCosmosFinalize(w http.ResponseWriter, r *http.Request) {
	var req cosmosFinalizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}
	rb, err1 := hex.DecodeString(strings.TrimPrefix(req.R, "0x"))
	sb, err2 := hex.DecodeString(strings.TrimPrefix(req.S, "0x"))
	pubRaw, err3 := hex.DecodeString(strings.TrimPrefix(req.PubKeyHex, "0x"))
	if err1 != nil || err2 != nil || err3 != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "r, s and pubkey_hex must be hex"})
		return
	}
	tx, err := chains.AssembleCosmosTx(req.Plan, rb, sb, pubRaw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	resp := cosmosFinalizeResponse{TxHash: tx.Hash, RawTxBase64: base64.StdEncoding.EncodeToString(tx.Raw)}
	if !req.Broadcast {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	node := s.cosmosNode()
	if node == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "no Cosmos node is configured (set COSMOS_LCD_URL)",
			"txhash": resp.TxHash, "raw_tx_base64": resp.RawTxBase64})
		return
	}
	res, err := node.Broadcast(r.Context(), tx.Raw)
	if err != nil {
		// The bytes come back so a relay can be retried without a new
		// ceremony, until the account's sequence moves on.
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": err.Error(), "txhash": resp.TxHash, "raw_tx_base64": resp.RawTxBase64})
		return
	}
	resp.Broadcast = true
	resp.TxHash = res.TxHash
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleCosmosStatus(w http.ResponseWriter, r *http.Request) {
	node := s.cosmosNode()
	if node == nil {
		errNoCosmosNode(w)
		return
	}
	hash := r.URL.Query().Get("txhash")
	if len(hash) != 64 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "txhash must be a 64-character hex transaction hash"})
		return
	}
	st, err := node.TxStatus(r.Context(), hash)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleCosmosAccount reports an address's account number and sequence. The
// sequence counts the transactions the chain has accepted from the address,
// which reconciliation compares with what the platform signed.
func (s *server) handleCosmosAccount(w http.ResponseWriter, r *http.Request) {
	node := s.cosmosNode()
	if node == nil {
		errNoCosmosNode(w)
		return
	}
	addr := r.URL.Query().Get("address")
	if err := validCosmosAddress(addr, node.Bech32Prefix); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	a, err := node.Account(r.Context(), addr)
	if errors.Is(err, chains.ErrCosmosNotFound) {
		// Never funded: zero transactions, which is an answer, not an error.
		writeJSON(w, http.StatusOK, map[string]interface{}{"address": addr, "exists": false, "sequence": 0})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"address": addr, "exists": true, "account_number": a.AccountNumber, "sequence": a.Sequence})
}
