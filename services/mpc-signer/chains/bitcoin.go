package chains

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"
)

// BitcoinSigner implements ChainSigner for Bitcoin (secp256k1 ECDSA).
//
// Uses btcec v1 (github.com/btcsuite/btcd/btcec) rather than btcec/v2
// deliberately: txscript and btcutil at the btcd version this module pins
// take v1 types, and that btcd version is itself pinned by
// bnb-chain/tss-lib v2, which needs the v1 btcec package path. Mixing v2
// here would either not compile against txscript or force a btcd upgrade
// that breaks tss-lib -- and tss-lib is the threshold-signing core this
// whole platform is built on, so it wins the version conflict.
type BitcoinSigner struct{}

// NewBitcoinSigner creates a new Bitcoin signer.
func NewBitcoinSigner() ChainSigner {
	return &BitcoinSigner{}
}

func decodeBitcoinPrivKey(privKeyHex string) (*btcec.PrivateKey, error) {
	if len(privKeyHex) >= 2 && (privKeyHex[:2] == "0x" || privKeyHex[:2] == "0X") {
		privKeyHex = privKeyHex[2:]
	}
	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	if len(privKeyBytes) != 32 {
		return nil, fmt.Errorf("private key must be 32 bytes, got %d", len(privKeyBytes))
	}
	privKey, _ := btcec.PrivKeyFromBytes(btcec.S256(), privKeyBytes)
	if privKey == nil {
		return nil, fmt.Errorf("invalid private key")
	}
	return privKey, nil
}

// SignMessage signs a 32-byte message hash with secp256k1.
//
// Produces a COMPACT (65-byte [V || R || S]) signature rather than DER:
// compact carries the recovery id, which is what makes RecoverAddress
// possible at all. The Signature struct's R/S/V fields are populated from
// it, and SignatureBytes holds the full compact form.
func (b *BitcoinSigner) SignMessage(ctx context.Context, messageHash []byte, privKeyHex string) (*Signature, error) {
	if len(messageHash) != 32 {
		return nil, fmt.Errorf("message hash must be 32 bytes, got %d", len(messageHash))
	}

	privKey, err := decodeBitcoinPrivKey(privKeyHex)
	if err != nil {
		return nil, err
	}

	// compressed=true: the address derivation below uses the compressed
	// public key, so recovery must reproduce that same form.
	compact, err := btcec.SignCompact(btcec.S256(), privKey, messageHash, true)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}
	if len(compact) != 65 {
		return nil, fmt.Errorf("unexpected compact signature length %d", len(compact))
	}

	// btcec compact layout is [V || R || S]; V is the recovery byte with
	// the 27 (+4 if compressed) offset baked in.
	return &Signature{
		V:              compact[0],
		R:              hex.EncodeToString(compact[1:33]),
		S:              hex.EncodeToString(compact[33:65]),
		SignatureBytes: hex.EncodeToString(compact),
	}, nil
}

// VerifySignature verifies a compact signature against a hex-encoded
// (compressed or uncompressed) public key.
func (b *BitcoinSigner) VerifySignature(ctx context.Context, messageHash []byte, signature *Signature, pubKeyHex string) (bool, error) {
	if signature == nil {
		return false, fmt.Errorf("no signature provided")
	}
	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}
	expected, err := btcec.ParsePubKey(pubKeyBytes, btcec.S256())
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}

	compact, err := hex.DecodeString(signature.SignatureBytes)
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}
	if len(compact) != 65 {
		return false, fmt.Errorf("signature must be 65 bytes, got %d", len(compact))
	}

	recovered, _, err := btcec.RecoverCompact(btcec.S256(), compact, messageHash)
	if err != nil {
		return false, fmt.Errorf("recovery failed: %w", err)
	}
	return recovered.IsEqual(expected), nil
}

// RecoverAddress recovers the P2PKH address of the signer. Network
// defaults to mainnet; BuildTransaction is where a caller chooses testnet,
// and a bare signature carries no network information of its own.
func (b *BitcoinSigner) RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error) {
	if signature == nil {
		return "", fmt.Errorf("no signature provided")
	}
	compact, err := hex.DecodeString(signature.SignatureBytes)
	if err != nil {
		return "", fmt.Errorf("invalid signature: %w", err)
	}
	if len(compact) != 65 {
		return "", fmt.Errorf("signature must be 65 bytes, got %d", len(compact))
	}

	pubKey, compressed, err := btcec.RecoverCompact(btcec.S256(), compact, messageHash)
	if err != nil {
		return "", fmt.Errorf("recovery failed: %w", err)
	}

	serialized := pubKey.SerializeUncompressed()
	if compressed {
		serialized = pubKey.SerializeCompressed()
	}

	addr, err := btcutil.NewAddressPubKeyHash(btcutil.Hash160(serialized), &chaincfg.MainNetParams)
	if err != nil {
		return "", fmt.Errorf("failed to derive address: %w", err)
	}
	return addr.EncodeAddress(), nil
}

// BuildTransaction assembles an unsigned Bitcoin transaction and returns
// the raw serialized bytes.
//
// Note on what this does and does not do: it produces the unsigned
// transaction. Bitcoin signs per-input over a sighash that depends on the
// input's previous output script, so a single "message hash" for the whole
// transaction (which the ChainSigner interface assumes) does not exist for
// Bitcoin. SignTransactionInput below is the operation a caller actually
// needs, and it is where per-input sighashes are computed.
func (b *BitcoinSigner) BuildTransaction(ctx context.Context, txData interface{}) ([]byte, error) {
	req, ok := txData.(*BitcoinSignRequest)
	if !ok {
		return nil, fmt.Errorf("invalid transaction type for Bitcoin")
	}
	if len(req.Inputs) == 0 {
		return nil, fmt.Errorf("transaction must have at least one input")
	}
	if len(req.Outputs) == 0 {
		return nil, fmt.Errorf("transaction must have at least one output")
	}

	params, err := bitcoinNetParams(req.Network)
	if err != nil {
		return nil, err
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	for _, input := range req.Inputs {
		hash, err := chainhash.NewHashFromStr(input.Txid)
		if err != nil {
			return nil, fmt.Errorf("invalid txid %q: %w", input.Txid, err)
		}
		if input.Vout < 0 {
			return nil, fmt.Errorf("invalid vout %d", input.Vout)
		}
		tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(hash, uint32(input.Vout)), nil, nil))
	}

	for _, output := range req.Outputs {
		addr, err := btcutil.DecodeAddress(output.Address, params)
		if err != nil {
			return nil, fmt.Errorf("invalid output address %q: %w", output.Address, err)
		}
		if !addr.IsForNet(params) {
			return nil, fmt.Errorf("address %q is not valid for network %q", output.Address, req.Network)
		}
		pkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return nil, fmt.Errorf("failed to build output script: %w", err)
		}
		if output.Amount <= 0 {
			return nil, fmt.Errorf("output amount must be positive, got %d", output.Amount)
		}
		tx.AddTxOut(wire.NewTxOut(output.Amount, pkScript))
	}

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("failed to serialize transaction: %w", err)
	}
	return buf.Bytes(), nil
}

// SignTransactionInput signs one input of a previously built transaction
// and returns the fully serialized transaction with that input's
// signature script attached.
//
// This is the Bitcoin-shaped signing operation the generic ChainSigner
// interface cannot express: the sighash covers the previous output's
// script, so each input is signed separately against its own subscript.
func (b *BitcoinSigner) SignTransactionInput(
	rawTx []byte,
	inputIndex int,
	prevOutScript []byte,
	privKeyHex string,
) ([]byte, error) {
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(rawTx)); err != nil {
		return nil, fmt.Errorf("failed to deserialize transaction: %w", err)
	}
	if inputIndex < 0 || inputIndex >= len(tx.TxIn) {
		return nil, fmt.Errorf("input index %d out of range (%d inputs)", inputIndex, len(tx.TxIn))
	}

	privKey, err := decodeBitcoinPrivKey(privKeyHex)
	if err != nil {
		return nil, err
	}

	sigScript, err := txscript.SignatureScript(
		tx, inputIndex, prevOutScript, txscript.SigHashAll, privKey, true,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to sign input %d: %w", inputIndex, err)
	}
	tx.TxIn[inputIndex].SignatureScript = sigScript

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return nil, fmt.Errorf("failed to serialize signed transaction: %w", err)
	}
	return buf.Bytes(), nil
}

// BroadcastTransaction is not implemented: it needs a Bitcoin node or a
// third-party broadcast API, neither of which this service is configured
// with. Returning an error is deliberate -- a stub that reported success
// would be far worse than one that admits it cannot broadcast.
func (b *BitcoinSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	return "", fmt.Errorf("broadcasting not implemented for Bitcoin: no node or broadcast API is configured")
}

func bitcoinNetParams(network string) (*chaincfg.Params, error) {
	switch network {
	case "", "mainnet":
		return &chaincfg.MainNetParams, nil
	case "testnet", "testnet3":
		return &chaincfg.TestNet3Params, nil
	case "regtest":
		return &chaincfg.RegressionNetParams, nil
	default:
		return nil, fmt.Errorf("unsupported Bitcoin network: %q", network)
	}
}

// payToAddrScriptForTest exposes txscript.PayToAddrScript to this package's
// tests without importing txscript there.
func payToAddrScriptForTest(addr btcutil.Address) ([]byte, error) {
	return txscript.PayToAddrScript(addr)
}
