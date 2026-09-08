package chains

import (
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcutil"
)

// Choosing which coins to spend, what fee to pay, and what change to return.
//
// PlanBitcoinTransaction takes inputs and outputs already decided. Deciding
// them is the part that makes Bitcoin usable as a product rather than a
// primitive, and it has no equivalent on Ethereum: an account has a balance,
// so a transfer is "send N from the balance". Bitcoin has no balance. It has
// a set of discrete previous outputs, and spending 0.1 BTC means selecting
// some of them whose total is at least 0.1 BTC plus a fee that depends on
// how many of them you selected -- a circular dependency, since each extra
// input makes the transaction bigger and therefore the fee larger.
//
// Getting this wrong is expensive in a way that is invisible until it is
// not. Under-estimate the fee and the transaction sits unconfirmed. Forget
// the change output and the entire remainder of every selected coin goes to
// the miner: the well-known way to lose a large amount of Bitcoin while
// broadcasting a transaction that is completely valid.

// UTXO is one spendable previous output.
type UTXO struct {
	Txid          string `json:"txid"`
	Vout          int    `json:"vout"`
	Amount        int64  `json:"amount"` // satoshis
	Script        string `json:"script"` // scriptPubKey, hex
	Confirmations int64  `json:"confirmations"`
}

// CoinSelectionRequest asks for a transaction paying one destination.
type CoinSelectionRequest struct {
	Network       string `json:"network"`
	UTXOs         []UTXO `json:"utxos"`
	Destination   string `json:"destination"`
	Amount        int64  `json:"amount"`   // satoshis to deliver
	FeeRate       int64  `json:"fee_rate"` // satoshis per virtual byte
	ChangeAddress string `json:"change_address"`
	// MinConfirmations defaults to 1. Zero would allow spending unconfirmed
	// outputs, which is legal but makes this transaction invalid if the
	// parent is replaced -- not a default a custody platform should pick.
	MinConfirmations *int64 `json:"min_confirmations,omitempty"`
}

// CoinSelection is the decision, with the numbers that produced it.
//
// Every field is reported rather than just the resulting transaction,
// because a customer disputing a fee needs to see what was selected and why,
// and "the wallet decided" is not an answer.
type CoinSelection struct {
	Request     *BitcoinSigningRequest `json:"request"`
	Selected    []UTXO                 `json:"selected"`
	TotalIn     int64                  `json:"total_in"`
	Amount      int64                  `json:"amount"`
	Fee         int64                  `json:"fee"`
	Change      int64                  `json:"change"`
	VirtualSize int64                  `json:"virtual_size"`
	FeeRate     int64                  `json:"fee_rate"`
	// ChangeDroppedToFee is set when the change would have been dust: the
	// remainder went to the miner instead of creating an output that costs
	// more to spend than it is worth. Surfaced because it is the one case
	// where the fee paid exceeds the fee quoted.
	ChangeDroppedToFee bool `json:"change_dropped_to_fee"`
}

// Sizes in bytes. Bitcoin fees are charged per virtual byte, so these are
// the numbers that turn a fee rate into a fee. They are upper bounds where
// a value can vary: a DER signature is 71 or 72 bytes depending on whether
// the r and s values happen to have a high bit set, and estimating low
// produces a transaction that pays less than the requested rate.
const (
	// version(4) + input count(1) + output count(1) + locktime(4).
	txOverheadBase = 10
	// Segwit marker and flag. Witness data, so a quarter of a byte each
	// after the weight division -- but only present if some input is segwit.
	segwitMarkerFlagWeight = 2

	// outpoint(36) + script length(1) + scriptSig(107) + sequence(4).
	p2pkhInputBase = 148
	// outpoint(36) + script length(1, empty) + sequence(4).
	p2wpkhInputBase = 41
	// stack items(1) + sig length(1) + sig(72) + key length(1) + key(33).
	p2wpkhInputWitness = 108

	// Dust: an output not worth creating, because spending it later costs
	// more in fees than it holds. Bitcoin Core computes this from the size
	// of the output plus the size of the input that will one day spend it,
	// at 3 sat/vB; these are the resulting well-known thresholds.
	p2pkhDust  = 546
	p2wpkhDust = 294
)

// SelectCoins picks inputs and builds the signing request for them.
//
// Largest-first. The alternative worth naming is branch-and-bound, which
// searches for a combination needing no change output at all; it is what
// Bitcoin Core does and it is genuinely better at keeping a wallet's coin
// set healthy. Largest-first is chosen here because it minimises the input
// count, which minimises both the fee and the number of digests that have
// to go through a threshold ceremony -- and a ceremony is far more
// expensive than a signature. The cost is a slow drift towards a wallet
// holding many small outputs, which is a consolidation problem, not a
// correctness one.
func SelectCoins(req *CoinSelectionRequest) (*CoinSelection, error) {
	params, err := bitcoinNetParams(req.Network)
	if err != nil {
		return nil, err
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("amount must be positive, got %d sats", req.Amount)
	}
	if req.FeeRate <= 0 {
		return nil, fmt.Errorf("fee rate must be positive, got %d sat/vB", req.FeeRate)
	}

	destScript, err := scriptForAddress(req.Destination, params)
	if err != nil {
		return nil, fmt.Errorf("destination: %w", err)
	}
	changeScript, err := scriptForAddress(req.ChangeAddress, params)
	if err != nil {
		return nil, fmt.Errorf("change address: %w", err)
	}

	minConf := int64(1)
	if req.MinConfirmations != nil {
		minConf = *req.MinConfirmations
	}

	// Filter before sorting so the error message can distinguish "you have
	// no coins" from "your coins are not confirmed yet" -- two situations
	// that look identical from a balance and need different responses.
	var spendable []UTXO
	var unconfirmed int
	for _, u := range req.UTXOs {
		if u.Amount <= 0 {
			continue
		}
		if u.Confirmations < minConf {
			unconfirmed++
			continue
		}
		if _, _, err := inputWeights(u.Script); err != nil {
			// An output type this platform cannot sign for. Skipping rather
			// than failing: an unspendable coin in the set should not make
			// every other coin unspendable too.
			continue
		}
		spendable = append(spendable, u)
	}
	if len(spendable) == 0 {
		if unconfirmed > 0 {
			return nil, fmt.Errorf(
				"no spendable outputs: %d output(s) exist but have fewer than %d confirmation(s)",
				unconfirmed, minConf)
		}
		return nil, fmt.Errorf("no spendable outputs")
	}

	sort.Slice(spendable, func(i, j int) bool {
		if spendable[i].Amount != spendable[j].Amount {
			return spendable[i].Amount > spendable[j].Amount
		}
		// Deterministic tie-break, so the same wallet state always produces
		// the same transaction. Without it an idempotent retry could select
		// a different set and broadcast a second, different transaction.
		if spendable[i].Txid != spendable[j].Txid {
			return spendable[i].Txid < spendable[j].Txid
		}
		return spendable[i].Vout < spendable[j].Vout
	})

	changeDust := dustThreshold(changeScript)

	var selected []UTXO
	var totalIn int64
	for _, u := range spendable {
		selected = append(selected, u)
		totalIn += u.Amount

		// The fee depends on the selection, so it is recomputed on every
		// step rather than estimated once up front.
		withChange := estimateVirtualSize(selected, [][]byte{destScript, changeScript})
		feeWithChange := withChange * req.FeeRate

		if totalIn < req.Amount+feeWithChange {
			continue
		}

		change := totalIn - req.Amount - feeWithChange
		if change >= changeDust {
			return buildSelection(req, selected, totalIn, change,
				feeWithChange, withChange, false)
		}

		// The change would be dust. Drop the output: it is cheaper to give
		// the remainder to the miner than to create an output that costs
		// more than it holds to spend. Recompute the size without it, since
		// the transaction is now smaller.
		noChange := estimateVirtualSize(selected, [][]byte{destScript})
		feeNoChange := noChange * req.FeeRate
		if totalIn >= req.Amount+feeNoChange {
			// The fee reported is what is actually paid -- the remainder --
			// not the fee the rate implies. Quoting the smaller number would
			// make the arithmetic in the response not add up.
			return buildSelection(req, selected, totalIn, 0,
				totalIn-req.Amount, noChange, true)
		}
		// Not quite enough even without change; keep accumulating.
	}

	// Report the shortfall rather than just "insufficient funds": the fee is
	// part of what is missing, and a caller with exactly the destination
	// amount available needs to be told that the fee is why it failed.
	size := estimateVirtualSize(selected, [][]byte{destScript, changeScript})
	need := req.Amount + size*req.FeeRate
	return nil, fmt.Errorf(
		"insufficient funds: %d sats available across %d output(s), need %d (%d to send + ~%d fee at %d sat/vB)",
		totalIn, len(selected), need, req.Amount, size*req.FeeRate, req.FeeRate)
}

// buildSelection turns a decided selection into the signing request that
// PlanBitcoinTransaction takes, so the two cannot disagree about what is
// being spent.
func buildSelection(
	req *CoinSelectionRequest,
	selected []UTXO,
	totalIn, change, fee, vsize int64,
	changeDropped bool,
) (*CoinSelection, error) {
	inputs := make([]BitcoinInput, 0, len(selected))
	for _, u := range selected {
		inputs = append(inputs, BitcoinInput{
			Txid:   u.Txid,
			Vout:   u.Vout,
			Amount: u.Amount,
			Script: u.Script,
		})
	}

	outputs := []BitcoinOutput{{Address: req.Destination, Amount: req.Amount}}
	if change > 0 {
		outputs = append(outputs, BitcoinOutput{Address: req.ChangeAddress, Amount: change})
	}

	return &CoinSelection{
		Request: &BitcoinSigningRequest{
			Network: req.Network,
			Inputs:  inputs,
			Outputs: outputs,
		},
		Selected:           append([]UTXO(nil), selected...),
		TotalIn:            totalIn,
		Amount:             req.Amount,
		Fee:                fee,
		Change:             change,
		VirtualSize:        vsize,
		FeeRate:            req.FeeRate,
		ChangeDroppedToFee: changeDropped,
	}, nil
}

// scriptForAddress decodes an address and returns the script paying it,
// refusing an address belonging to a different network.
//
// The network check is the one that matters: a mainnet address decodes
// perfectly well against regtest parameters and would produce a valid
// transaction paying a script nobody can ever spend.
func scriptForAddress(addr string, params *chaincfg.Params) ([]byte, error) {
	if addr == "" {
		return nil, fmt.Errorf("no address supplied")
	}
	decoded, err := btcutil.DecodeAddress(addr, params)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if !decoded.IsForNet(params) {
		return nil, fmt.Errorf("address %q is not valid on %s", addr, params.Name)
	}
	return txscript.PayToAddrScript(decoded)
}

// inputWeights returns the base and witness bytes an input spending this
// script will occupy.
//
// Only the two output types this platform can actually threshold-sign are
// accepted. Returning an error for anything else keeps the fee estimate
// honest: silently guessing a size for an unknown script type produces a
// fee that is wrong in an unknown direction.
func inputWeights(scriptHex string) (base, witness int64, err error) {
	script, err := hex.DecodeString(scriptHex)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid scriptPubKey hex: %w", err)
	}
	switch {
	case txscript.IsPayToWitnessPubKeyHash(script):
		return p2wpkhInputBase, p2wpkhInputWitness, nil
	case isPayToPubKeyHash(script):
		return p2pkhInputBase, 0, nil
	default:
		return 0, 0, fmt.Errorf("unsupported output type; only P2PKH and P2WPKH can be signed")
	}
}

func isPayToPubKeyHash(script []byte) bool {
	return len(script) == 25 &&
		script[0] == txscript.OP_DUP &&
		script[1] == txscript.OP_HASH160 &&
		script[2] == 20 &&
		script[23] == txscript.OP_EQUALVERIFY &&
		script[24] == txscript.OP_CHECKSIG
}

// estimateVirtualSize returns the transaction's size in virtual bytes.
//
// Virtual bytes, not bytes: segwit charges witness data at a quarter rate,
// which is the discount that makes a P2WPKH spend cheaper than the P2PKH
// spend of the same value. weight = base*4 + witness, and vsize is that
// divided by four, rounded up.
func estimateVirtualSize(inputs []UTXO, outputScripts [][]byte) int64 {
	base := int64(txOverheadBase)
	witness := int64(0)
	anySegwit := false

	for _, in := range inputs {
		b, w, err := inputWeights(in.Script)
		if err != nil {
			// Unspendable inputs are filtered out before this is reached;
			// charging the larger of the two known types is the safe
			// direction if one ever gets through.
			b, w = p2pkhInputBase, 0
		}
		base += b
		witness += w
		if w > 0 {
			anySegwit = true
		}
	}
	for _, s := range outputScripts {
		// amount(8) + script length(1) + script.
		base += 9 + int64(len(s))
	}
	if anySegwit {
		witness += segwitMarkerFlagWeight
	}

	weight := base*4 + witness
	return (weight + 3) / 4
}

// dustThreshold is the smallest output worth creating for a given script.
func dustThreshold(script []byte) int64 {
	if txscript.IsPayToWitnessPubKeyHash(script) {
		return p2wpkhDust
	}
	return p2pkhDust
}
