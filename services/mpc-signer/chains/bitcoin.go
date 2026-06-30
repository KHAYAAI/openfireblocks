package chains

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// BitcoinSigner implements ChainSigner for Bitcoin.
type BitcoinSigner struct{}

// NewBitcoinSigner creates a new Bitcoin signer.
func NewBitcoinSigner() ChainSigner {
	return &BitcoinSigner{}
}

// SignMessage signs a message hash for Bitcoin.
func (b *BitcoinSigner) SignMessage(ctx context.Context, messageHash []byte, privKeyHex string) (*Signature, error) {
	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	privKey := btcec.PrivKeyFromBytes(privKeyBytes)
	sig := ecdsa.Sign(privKey, messageHash)

	return &Signature{
		R:              hex.EncodeToString(sig.R.Bytes()),
		S:              hex.EncodeToString(sig.S.Bytes()),
		SignatureBytes: "0x" + hex.EncodeToString(sig.Serialize()),
	}, nil
}

// VerifySignature verifies a Bitcoin signature.
func (b *BitcoinSigner) VerifySignature(ctx context.Context, messageHash []byte, signature *Signature, pubKey string) (bool, error) {
	pubKeyBytes, err := hex.DecodeString(pubKey)
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}

	pubKeyObj, err := btcec.ParsePubKey(pubKeyBytes)
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}

	sigBytes, err := hex.DecodeString(signature.SignatureBytes[2:])
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}

	sig, err := ecdsa.ParseSignature(sigBytes)
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}

	valid := sig.Verify(messageHash, pubKeyObj)
	return valid, nil
}

// RecoverAddress recovers the signer address from a message and signature (not applicable for Bitcoin).
func (b *BitcoinSigner) RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error) {
	return "", fmt.Errorf("address recovery not applicable for Bitcoin")
}

// BuildTransaction builds a Bitcoin transaction ready for signing.
func (b *BitcoinSigner) BuildTransaction(ctx context.Context, txData interface{}) ([]byte, error) {
	req, ok := txData.(*BitcoinSignRequest)
	if !ok {
		return nil, fmt.Errorf("invalid transaction type for Bitcoin")
	}

	// Determine network
	var params *chaincfg.Params
	switch req.Network {
	case "mainnet":
		params = &chaincfg.MainNetParams
	case "testnet":
		params = &chaincfg.TestNet3Params
	default:
		return nil, fmt.Errorf("unsupported network: %s", req.Network)
	}

	// Build transaction
	tx := wire.NewMsgTx(wire.TxVersion)

	// Add inputs
	for _, input := range req.Inputs {
		hash, err := wire.NewShaHashFromStr(input.Txid)
		if err != nil {
			return nil, fmt.Errorf("invalid input txid: %w", err)
		}
		outpoint := wire.NewOutPoint(hash, uint32(input.Vout))
		txIn := wire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
	}

	// Add outputs
	for _, output := range req.Outputs {
		addr, err := btcutil.DecodeAddress(output.Address, params)
		if err != nil {
			return nil, fmt.Errorf("invalid output address: %w", err)
		}

		pkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return nil, fmt.Errorf("failed to build script: %w", err)
		}

		txOut := wire.NewTxOut(output.Amount, pkScript)
		tx.AddTxOut(txOut)
	}

	// Return serialized transaction for signing
	var buf wire.Buffer
	err := tx.Serialize(&buf)
	if err != nil {
		return nil, fmt.Errorf("serialization failed: %w", err)
	}

	return buf.Bytes(), nil
}

// BroadcastTransaction broadcasts a signed Bitcoin transaction (stub).
func (b *BitcoinSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	// TODO: implement RPC broadcast via bitcoind
	return "", fmt.Errorf("broadcasting not yet implemented for Bitcoin")
}
