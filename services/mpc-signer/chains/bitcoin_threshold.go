package chains

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"
)

// Bitcoin signing for keys this process does not hold.
//
// Everything else in bitcoin.go signs with a private key passed in as a hex
// string, which is the single-key model: whoever calls it has the key. A
// threshold key has no such thing -- the private key does not exist anywhere
// and never will, which is the entire point of the platform. So the flow has
// to invert:
//
//	1. Build the unsigned transaction and compute the sighash for each input
//	   (BitcoinSigningRequest -> BitcoinSigningPlan).
//	2. Hand those 32-byte digests to the MPC committee, which returns (r, s)
//	   per input without anybody ever holding the key.
//	3. Assemble the digests' signatures back into a spendable transaction
//	   (AssembleBitcoinTransaction).
//
// The middle step is the same threshold-ECDSA ceremony Ethereum already
// uses -- Bitcoin and Ethereum are both secp256k1 ECDSA, and a signature
// over a 32-byte digest does not care which chain produced the digest. What
// differs is entirely in steps 1 and 3: what gets hashed, and how the
// signature is packaged.

// BitcoinSigningRequest describes the transaction to build.
//
// Reuses BitcoinInput/BitcoinOutput from types.go rather than introducing a
// parallel set: the shape of a Bitcoin transaction does not change because
// the key is held by a committee instead of a process, and two nearly
// identical structs would drift.
type BitcoinSigningRequest struct {
	Network string          `json:"network"`
	Inputs  []BitcoinInput  `json:"inputs"`
	Outputs []BitcoinOutput `json:"outputs"`
}

// BitcoinSigningPlan is the unsigned transaction plus the digests to sign.
type BitcoinSigningPlan struct {
	// UnsignedTxHex is the serialised transaction with empty signature
	// scripts. Kept so assembly operates on exactly the bytes that were
	// hashed rather than rebuilding and hoping the result matches.
	UnsignedTxHex string `json:"unsigned_tx_hex"`
	// SigHashes is one 32-byte digest per input, in input order.
	SigHashes []string `json:"sighashes"`
	// PrevScripts mirrors SigHashes: the script each input spends, needed
	// again at assembly time to build the correct signature script.
	PrevScripts []string `json:"prev_scripts"`
	// Witness marks which inputs are segwit. Carried rather than
	// re-derived so assembly cannot disagree with planning about how an
	// input was signed -- a disagreement there produces a transaction that
	// looks fine and spends nothing.
	Witness []bool `json:"witness"`
	// Amounts is required to re-verify a segwit input: BIP143 signs the
	// value, so the script engine needs it too.
	Amounts []int64 `json:"amounts"`
}

// BitcoinInputSignature is one committee-produced signature.
type BitcoinInputSignature struct {
	R string `json:"r"`
	S string `json:"s"`
}

// PlanBitcoinTransaction builds the unsigned transaction and its sighashes.
//
// Every input is hashed with SigHashAll, which commits to all inputs and all
// outputs. That matters here more than in a single-key wallet: the digests
// leave this process and travel to a signing committee, and SigHashAll is
// what makes a returned signature useless for any transaction other than
// exactly the one that was planned. A permissive sighash flag would let a
// captured signature be replayed into a transaction paying somebody else.
func PlanBitcoinTransaction(req *BitcoinSigningRequest) (*BitcoinSigningPlan, error) {
	params, err := bitcoinNetParams(req.Network)
	if err != nil {
		return nil, err
	}
	if len(req.Inputs) == 0 {
		return nil, fmt.Errorf("no inputs to spend")
	}
	if len(req.Outputs) == 0 {
		return nil, fmt.Errorf("no outputs; this would burn the entire input value to fees")
	}

	tx := wire.NewMsgTx(wire.TxVersion)

	for i, in := range req.Inputs {
		hash, err := chainhash.NewHashFromStr(in.Txid)
		if err != nil {
			return nil, fmt.Errorf("input %d: invalid txid %q: %w", i, in.Txid, err)
		}
		if in.Vout < 0 {
			return nil, fmt.Errorf("input %d: negative vout %d", i, in.Vout)
		}
		tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(hash, uint32(in.Vout)), nil, nil))
	}

	var totalOut int64
	for i, out := range req.Outputs {
		if out.Amount <= 0 {
			return nil, fmt.Errorf("output %d has a non-positive amount (%d sats)", i, out.Amount)
		}
		addr, err := btcutil.DecodeAddress(out.Address, params)
		if err != nil {
			return nil, fmt.Errorf("output %d: invalid address %q: %w", i, out.Address, err)
		}
		// An address valid on another network decodes fine and would send
		// real funds to an unspendable script. Bitcoin's version bytes are
		// the only thing distinguishing them.
		if !addr.IsForNet(params) {
			return nil, fmt.Errorf("output %d: address %q is not valid on %s", i, out.Address, params.Name)
		}
		script, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return nil, fmt.Errorf("output %d: %w", i, err)
		}
		// Dust: an output too small to be worth the fee of spending it
		// later. The network refuses to relay these outright, so building
		// one produces a transaction that is well-formed, correctly
		// signed, and rejected -- which is the worst place to find out,
		// because by then a threshold ceremony has already run for every
		// input. Refusing here costs nothing and fails with the number the
		// caller needs.
		if threshold := dustThreshold(script); out.Amount < threshold {
			return nil, fmt.Errorf(
				"output %d sends %d sats to %s, below the %d sat dust threshold for that address type; "+
					"the network will not relay it",
				i, out.Amount, out.Address, threshold)
		}
		tx.AddTxOut(wire.NewTxOut(out.Amount, script))
		totalOut += out.Amount
	}

	// Fee sanity. The difference between inputs and outputs is the fee, and
	// on Bitcoin it is implicit -- a mistyped output amount does not fail,
	// it just pays the miner the remainder. Refusing a negative fee catches
	// a transaction that cannot be mined; refusing an absurd one catches the
	// far more expensive mistake.
	var totalIn int64
	for _, in := range req.Inputs {
		totalIn += in.Amount
	}
	if totalIn > 0 {
		fee := totalIn - totalOut
		if fee < 0 {
			return nil, fmt.Errorf("outputs (%d sats) exceed inputs (%d sats)", totalOut, totalIn)
		}
		if totalOut > 0 && fee > totalOut {
			return nil, fmt.Errorf(
				"implied fee of %d sats exceeds the %d sats being sent; refusing (check the output amounts)",
				fee, totalOut)
		}
	}

	// Computed once and shared across inputs: BIP143 hashes the prevouts,
	// sequences and outputs of the whole transaction, and recomputing them
	// per input is what makes naive segwit signing quadratic -- the very
	// problem BIP143 was written to remove.
	sigHashes := txscript.NewTxSigHashes(tx)

	plan := &BitcoinSigningPlan{}
	for i, in := range req.Inputs {
		prevScript, err := hex.DecodeString(in.Script)
		if err != nil {
			return nil, fmt.Errorf("input %d: invalid scriptPubKey hex: %w", i, err)
		}
		if len(prevScript) == 0 {
			return nil, fmt.Errorf("input %d: no scriptPubKey; the sighash cannot be computed without it", i)
		}

		witness := txscript.IsPayToWitnessPubKeyHash(prevScript)
		var digest []byte
		if witness {
			// BIP143 commits to the value being spent, which the legacy
			// algorithm does not. That is the fix for the hardware-wallet
			// fee attack, and it means an input's amount is now part of what
			// gets signed -- so a wrong amount produces a signature that
			// silently fails rather than a transaction that overpays.
			if in.Amount <= 0 {
				return nil, fmt.Errorf(
					"input %d spends a segwit output but has no amount; BIP143 signs the value, so it cannot be omitted", i)
			}
			// The script actually hashed for P2WPKH is not the witness
			// program -- it is the equivalent P2PKH script built from the
			// same key hash. Signing the witness program instead produces a
			// signature that verifies against nothing.
			script, serr := p2pkhScriptForWitnessProgram(prevScript)
			if serr != nil {
				return nil, fmt.Errorf("input %d: %w", i, serr)
			}
			digest, err = txscript.CalcWitnessSigHash(script, sigHashes, txscript.SigHashAll, tx, i, in.Amount)
		} else {
			digest, err = txscript.CalcSignatureHash(prevScript, txscript.SigHashAll, tx, i)
		}
		if err != nil {
			return nil, fmt.Errorf("input %d: failed to compute sighash: %w", i, err)
		}

		plan.SigHashes = append(plan.SigHashes, hex.EncodeToString(digest))
		plan.PrevScripts = append(plan.PrevScripts, in.Script)
		plan.Witness = append(plan.Witness, witness)
		plan.Amounts = append(plan.Amounts, in.Amount)
	}

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("failed to serialize the unsigned transaction: %w", err)
	}
	plan.UnsignedTxHex = hex.EncodeToString(buf.Bytes())
	return plan, nil
}

// AssembleBitcoinTransaction puts committee signatures into the transaction.
//
// pubKeyHex is the threshold key's public key, compressed or uncompressed,
// which goes into each signature script alongside the signature -- P2PKH
// outputs commit to the hash of the public key, so spending one has to
// reveal it.
//
// The result is verified against btcd's script engine before being returned.
// That is not belt-and-braces: a malformed signature script produces a
// transaction that serialises perfectly, broadcasts, and is rejected by the
// network with an error that says nothing useful. Running the interpreter
// here turns that into a specific failure at the point the mistake was made.
func AssembleBitcoinTransaction(
	plan *BitcoinSigningPlan,
	sigs []BitcoinInputSignature,
	pubKeyHex string,
) ([]byte, error) {
	if plan == nil {
		return nil, fmt.Errorf("no signing plan")
	}
	if len(sigs) != len(plan.SigHashes) {
		return nil, fmt.Errorf("got %d signatures for %d inputs", len(sigs), len(plan.SigHashes))
	}

	rawPubKey, err := hex.DecodeString(stripHexPrefix(pubKeyHex))
	if err != nil {
		return nil, fmt.Errorf("invalid public key hex: %w", err)
	}
	parsedPubKey, err := btcec.ParsePubKey(rawPubKey, btcec.S256())
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}
	// Always the compressed form, whatever was passed in.
	//
	// A P2PKH output commits to hash160 of a *specific* serialisation, and
	// the compressed and uncompressed encodings of the same key hash to
	// different addresses. The platform's DKG reports public keys
	// uncompressed (65 bytes, 0x04...), while Bitcoin addresses are derived
	// from the compressed form -- so passing the bytes through verbatim put
	// the wrong key in the signature script and the script failed with
	// OP_EQUALVERIFY, which says nothing about serialisation and everything
	// about a mismatch nobody can see.
	//
	// Normalising here rather than at the call sites means address
	// derivation and spending cannot disagree.
	pubKeyBytes := parsedPubKey.SerializeCompressed()

	rawTx, err := hex.DecodeString(plan.UnsignedTxHex)
	if err != nil {
		return nil, fmt.Errorf("invalid unsigned transaction hex: %w", err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(rawTx)); err != nil {
		return nil, fmt.Errorf("failed to deserialize the unsigned transaction: %w", err)
	}
	if len(tx.TxIn) != len(sigs) {
		return nil, fmt.Errorf("the transaction has %d inputs but %d signatures were supplied", len(tx.TxIn), len(sigs))
	}

	for i, sig := range sigs {
		r, ok := new(big.Int).SetString(stripHexPrefix(sig.R), 16)
		if !ok {
			return nil, fmt.Errorf("input %d: r is not valid hex", i)
		}
		s, ok := new(big.Int).SetString(stripHexPrefix(sig.S), 16)
		if !ok {
			return nil, fmt.Errorf("input %d: s is not valid hex", i)
		}

		// Low-S normalisation. Bitcoin enforces BIP-62: a signature whose s
		// is above half the curve order is non-standard and will not relay,
		// even though it verifies perfectly. (r, s) and (r, n-s) are both
		// valid signatures over the same digest, so this is a re-encoding
		// rather than a change of meaning -- but a threshold ceremony has no
		// reason to prefer one, so it has to be done here or roughly half of
		// all transactions would silently fail to propagate.
		halfOrder := new(big.Int).Rsh(btcec.S256().N, 1)
		if s.Cmp(halfOrder) > 0 {
			s = new(big.Int).Sub(btcec.S256().N, s)
		}

		btcSig := &btcec.Signature{R: r, S: s}
		// The trailing hash-type byte is part of the signature push, not the
		// DER encoding: it tells a verifier which sighash was signed.
		sigWithHashType := append(btcSig.Serialize(), byte(txscript.SigHashAll))

		if i < len(plan.Witness) && plan.Witness[i] {
			// A segwit input carries its signature in the witness, and its
			// signature script must stay *empty*. Putting the same data in
			// both is not belt-and-braces: it changes the txid, because the
			// signature script is committed to and the witness is not.
			tx.TxIn[i].Witness = wire.TxWitness{sigWithHashType, pubKeyBytes}
			tx.TxIn[i].SignatureScript = nil
			continue
		}

		sigScript, err := txscript.NewScriptBuilder().
			AddData(sigWithHashType).
			AddData(pubKeyBytes).
			Script()
		if err != nil {
			return nil, fmt.Errorf("input %d: failed to build the signature script: %w", i, err)
		}
		tx.TxIn[i].SignatureScript = sigScript
	}

	// Execute each input's script for real, against the output it spends.
	//
	// The hash cache and the input amount are not optional for segwit: the
	// engine recomputes the BIP143 digest to check the signature, and it
	// signs over the value. Passing 0 here would fail every segwit input
	// with a signature error that has nothing to do with the signature.
	hashCache := txscript.NewTxSigHashes(tx)
	for i, prevScriptHex := range plan.PrevScripts {
		prevScript, err := hex.DecodeString(prevScriptHex)
		if err != nil {
			return nil, fmt.Errorf("input %d: invalid previous script hex: %w", i, err)
		}
		var amount int64
		if i < len(plan.Amounts) {
			amount = plan.Amounts[i]
		}
		engine, err := txscript.NewEngine(prevScript, tx, i,
			txscript.StandardVerifyFlags, nil, hashCache, amount)
		if err != nil {
			return nil, fmt.Errorf("input %d: could not build a script engine: %w", i, err)
		}
		if err := engine.Execute(); err != nil {
			return nil, fmt.Errorf(
				"input %d: the assembled transaction does not satisfy the output it spends: %w", i, err)
		}
	}

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("failed to serialize the signed transaction: %w", err)
	}
	return buf.Bytes(), nil
}

// BitcoinAddressesForPubKey returns the addresses a threshold key can be
// paid at, in the order they should be preferred for change.
//
// Both come from the same 20-byte hash of the same compressed key; only the
// encoding differs. That is why a key needs no second ceremony to receive at
// a segwit address -- and why a wallet's balance is the sum of what it holds
// under either encoding, not one or the other. A platform that looked at
// only one form would report a balance of zero for a customer whose
// counterparty happened to pay the other.
func BitcoinAddressesForPubKey(pubKeyHex, network string) (p2wpkh, p2pkh string, err error) {
	params, err := bitcoinNetParams(network)
	if err != nil {
		return "", "", err
	}
	raw, err := hex.DecodeString(stripHexPrefix(pubKeyHex))
	if err != nil {
		return "", "", fmt.Errorf("invalid public key hex: %w", err)
	}
	parsed, err := btcec.ParsePubKey(raw, btcec.S256())
	if err != nil {
		return "", "", fmt.Errorf("invalid public key: %w", err)
	}
	// Compressed, always -- see AssembleBitcoinTransaction. The DKG reports
	// uncompressed keys and Bitcoin addresses derive from the compressed
	// form, so normalising in one place is what keeps derivation and
	// spending from disagreeing.
	hash := btcutil.Hash160(parsed.SerializeCompressed())

	segwit, err := btcutil.NewAddressWitnessPubKeyHash(hash, params)
	if err != nil {
		return "", "", fmt.Errorf("deriving the segwit address: %w", err)
	}
	legacy, err := btcutil.NewAddressPubKeyHash(hash, params)
	if err != nil {
		return "", "", fmt.Errorf("deriving the legacy address: %w", err)
	}
	return segwit.EncodeAddress(), legacy.EncodeAddress(), nil
}

// BitcoinTxID returns the transaction id in the display order bitcoind uses.
func BitcoinTxID(signedTx []byte) (string, error) {
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(signedTx)); err != nil {
		return "", fmt.Errorf("failed to deserialize: %w", err)
	}
	return tx.TxHash().String(), nil
}

func stripHexPrefix(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}

// p2pkhScriptForWitnessProgram returns the script BIP143 actually hashes for
// a P2WPKH input.
//
// A witness program is `OP_0 <20-byte key hash>`, but that is not what gets
// signed. BIP143 specifies the "scriptCode" for P2WPKH as the equivalent
// legacy P2PKH script over the same key hash, which is the one non-obvious
// step in segwit signing and the one that produces a signature verifying
// against nothing if you skip it.
func p2pkhScriptForWitnessProgram(witnessProgram []byte) ([]byte, error) {
	// OP_0 (0x00), PUSH20 (0x14), then 20 bytes.
	if len(witnessProgram) != 22 || witnessProgram[0] != 0x00 || witnessProgram[1] != 0x14 {
		return nil, fmt.Errorf("not a P2WPKH witness program")
	}
	return txscript.NewScriptBuilder().
		AddOp(txscript.OP_DUP).
		AddOp(txscript.OP_HASH160).
		AddData(witnessProgram[2:]).
		AddOp(txscript.OP_EQUALVERIFY).
		AddOp(txscript.OP_CHECKSIG).
		Script()
}
