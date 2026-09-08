package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"forge-crypto/mpc-signer/chains"
)

// The two calls that let the gateway spend Bitcoin from a threshold key.
//
// Split in two because a threshold signature happens between them, and it
// does not happen here. The gateway asks what to sign (/bitcoin/prepare),
// runs the ceremony against the party pods, and comes back with signatures
// to turn into a transaction (/bitcoin/finalize). This service holds no
// share and never sees one.
//
// The split is also what keeps the gateway out of the Bitcoin business.
// Coin selection, BIP143, witness assembly and dust thresholds are hard to
// get right and identical for every caller; putting them behind two calls
// means there is one implementation, in the language whose Bitcoin library
// this platform already validates against.

type bitcoinPrepareRequest struct {
	Network string `json:"network"`
	// PubKeyHex is the threshold key's public key. Both of the addresses it
	// can be paid at are derived from it here rather than by the caller:
	// address derivation and spending have to agree about key
	// serialisation, and they only reliably agree if one place decides.
	PubKeyHex string `json:"pubkey_hex"`
	// Address narrows the spend to a single address. Optional; without it
	// both of the key's addresses are scanned, which is what a balance
	// actually means.
	Address     string `json:"address,omitempty"`
	Destination string `json:"destination"`
	Amount      int64  `json:"amount"`
	// FeeRate in sat/vB. Absent or zero asks the node to estimate.
	FeeRate int64 `json:"fee_rate,omitempty"`
	// ChangeAddress defaults to Address: change returns to the key it came
	// from. Sending change anywhere else is how a wallet accidentally pays
	// its remainder to somebody, so it has to be asked for explicitly.
	ChangeAddress      string `json:"change_address,omitempty"`
	MinConfirmations   *int64 `json:"min_confirmations,omitempty"`
	ConfirmationTarget int64  `json:"confirmation_target,omitempty"`
}

type bitcoinPrepareResponse struct {
	Selection *chains.CoinSelection      `json:"selection"`
	Plan      *chains.BitcoinSigningPlan `json:"plan"`
	Balance   int64                      `json:"balance"`
	UTXOs     int                        `json:"utxo_count"`
	// Addresses that were scanned, so a caller seeing an unexpected balance
	// can tell whether it looked where they thought it did.
	Addresses []string `json:"addresses"`
}

type bitcoinFinalizeRequest struct {
	Plan       *chains.BitcoinSigningPlan     `json:"plan"`
	Signatures []chains.BitcoinInputSignature `json:"signatures"`
	PubKeyHex  string                         `json:"pubkey_hex"`
	// Broadcast is opt-in. Assembling and broadcasting are separate
	// decisions: a caller may want the bytes to inspect, and a broadcast
	// cannot be undone.
	Broadcast bool `json:"broadcast"`
}

type bitcoinFinalizeResponse struct {
	RawTxHex    string `json:"raw_tx_hex"`
	TxID        string `json:"txid"`
	Broadcast   bool   `json:"broadcast"`
	BroadcastID string `json:"broadcast_txid,omitempty"`
}

// handleBitcoinPrepare selects coins and returns the digests to sign.
func (s *server) handleBitcoinPrepare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req bitcoinPrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}
	node := s.bitcoinNode()
	if node == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "no Bitcoin node is configured (set BITCOIN_RPC_URL)",
		})
		return
	}
	// Which addresses hold this key's money.
	var scan []string
	var defaultChange string
	if req.PubKeyHex != "" {
		segwit, legacy, err := chains.BitcoinAddressesForPubKey(req.PubKeyHex, req.Network)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
			return
		}
		scan = []string{segwit, legacy}
		// Change goes to the segwit address: it is the cheaper of the two to
		// spend later, and change is by definition spent later.
		defaultChange = segwit
	}
	if req.Address != "" {
		scan = []string{req.Address}
		if defaultChange == "" {
			defaultChange = req.Address
		}
	}
	if len(scan) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "pubkey_hex or address is required: one of them says whose coins will be spent",
		})
		return
	}

	utxos, err := node.ListUnspentForAddresses(ctx, scan)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": fmt.Sprintf("could not read the address's unspent outputs: %v", err),
		})
		return
	}
	var balance int64
	for _, u := range utxos {
		balance += u.Amount
	}

	feeRate := req.FeeRate
	if feeRate <= 0 {
		// The fallback matters: a node with no fee history returns no
		// estimate at all, which is the normal state on regtest and on a
		// freshly synced node.
		feeRate, err = node.EstimateFeeRate(ctx, req.ConfirmationTarget, defaultFeeRateSatPerVByte)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]interface{}{
				"error": fmt.Sprintf("could not estimate a fee rate: %v", err),
			})
			return
		}
	}

	changeAddress := req.ChangeAddress
	if changeAddress == "" {
		changeAddress = defaultChange
	}

	selection, err := chains.SelectCoins(&chains.CoinSelectionRequest{
		Network:          req.Network,
		UTXOs:            utxos,
		Destination:      req.Destination,
		Amount:           req.Amount,
		FeeRate:          feeRate,
		ChangeAddress:    changeAddress,
		MinConfirmations: req.MinConfirmations,
	})
	if err != nil {
		// A client error: insufficient funds, a bad address, an amount of
		// zero. Reporting these as 500s would hide the caller's mistake
		// behind an apparent outage.
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   err.Error(),
			"balance": balance,
		})
		return
	}

	plan, err := chains.PlanBitcoinTransaction(selection.Request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, bitcoinPrepareResponse{
		Selection: selection,
		Plan:      plan,
		Balance:   balance,
		UTXOs:     len(utxos),
		Addresses: scan,
	})
}

// handleBitcoinFinalize assembles the signed transaction and optionally
// broadcasts it.
func (s *server) handleBitcoinFinalize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req bitcoinFinalizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}
	if req.Plan == nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "no signing plan supplied"})
		return
	}

	// Assembly runs the script engine over every input, so a bad signature
	// or a mismatched key fails here rather than at the node.
	raw, err := chains.AssembleBitcoinTransaction(req.Plan, req.Signatures, req.PubKeyHex)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	txid, err := chains.BitcoinTxID(raw)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
		return
	}

	resp := bitcoinFinalizeResponse{RawTxHex: hex.EncodeToString(raw), TxID: txid}
	if !req.Broadcast {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	broadcastID, err := s.signerRouter.BroadcastTransaction(ctx, "bitcoin", []byte(resp.RawTxHex))
	if err != nil {
		// 502, not 500: the transaction assembled and validated here, and
		// the node refused it. Those are different problems with different
		// owners, and the caller still gets the bytes back so a broadcast
		// can be retried without re-running the ceremony.
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error":      err.Error(),
			"raw_tx_hex": resp.RawTxHex,
			"txid":       resp.TxID,
		})
		return
	}
	resp.Broadcast = true
	resp.BroadcastID = broadcastID
	writeJSON(w, http.StatusOK, resp)
}

// defaultFeeRateSatPerVByte is used when the node cannot estimate. Low
// enough not to overpay on a quiet chain, above the 1 sat/vB relay floor.
const defaultFeeRateSatPerVByte = 2

// bitcoinNode returns the configured node client, or nil.
func (s *server) bitcoinNode() *chains.BitcoinRPC {
	signer, ok := s.signerRouter.Signer("bitcoin").(*chains.BitcoinSigner)
	if !ok {
		return nil
	}
	return signer.Node()
}
