package chains

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// BitcoinSigner implements ChainSigner for Bitcoin (secp256k1 ECDSA).
type BitcoinSigner struct{}

// NewBitcoinSigner creates a new Bitcoin signer.
func NewBitcoinSigner() ChainSigner {
	return &BitcoinSigner{}
}

// SignMessage signs a message hash for Bitcoin using secp256k1.
func (b *BitcoinSigner) SignMessage(ctx context.Context, messageHash []byte, privKeyHex string) (*Signature, error) {
	if len(privKeyHex) >= 2 && privKeyHex[:2] == "0x" {
		privKeyHex = privKeyHex[2:]
	}

	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	privKey, _ := btcec.PrivKeyFromBytes(privKeyBytes)
	if privKey == nil {
		return nil, fmt.Errorf("invalid private key")
	}

	// Sign using ECDSA (deterministic per RFC 6979)
	sig := ecdsa.Sign(privKey, messageHash)
	if sig == nil {
		return nil, fmt.Errorf("signing failed")
	}

	return &Signature{
		R:              hex.EncodeToString(sig.R.Bytes()),
		S:              hex.EncodeToString(sig.S.Bytes()),
		SignatureBytes: "0x" + hex.EncodeToString(sig.Serialize()),
	}, nil
}

// VerifySignature verifies a Bitcoin signature.
func (b *BitcoinSigner) VerifySignature(ctx context.Context, messageHash []byte, signature *Signature, pubKeyHex string) (bool, error) {
	if len(pubKeyHex) >= 2 && pubKeyHex[:2] == "0x" {
		pubKeyHex = pubKeyHex[2:]
	}

	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}

	pubKey, err := btcec.ParsePubKey(pubKeyBytes)
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}

	sigBytes, err := hex.DecodeString(signature.SignatureBytes[2:])
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}

	sig, err := ecdsa.ParseDERSignature(sigBytes)
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}

	valid := sig.Verify(messageHash, pubKey)
	return valid, nil
}

// RecoverAddress recovers the signer address from a message and signature (not applicable for Bitcoin).
func (b *BitcoinSigner) RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error) {
	return "", fmt.Errorf("address recovery not applicable for Bitcoin (use pubkey recovery instead)")
}

// BuildTransaction builds a Bitcoin transaction ready for signing (SegWit-compatible).
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
		hash, err := wire.NewHashFromStr(input.Txid)
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

	// For signing, return the double-SHA256 hash of the serialized transaction
	// (simplified; real BIP 143 signing requires more complex sighash computation for SegWit)
	var buf wire.Buffer
	_ = tx.Serialize(&buf)
	txBytes := buf.Bytes()

	// Compute double-SHA256 (legacy sighash; BIP 143 for SegWit requires scriptCode)
	hash := sha256.Sum256(txBytes)
	hash = sha256.Sum256(hash[:])
	return hash[:], nil
}

// BroadcastTransaction broadcasts a signed Bitcoin transaction via RPC.
func (b *BitcoinSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	// TODO: implement RPC broadcast via bitcoind
	// Example with btcrpcclient:
	//   client, _ := rpcclient.New(connCfg, nil)
	//   msgTx := wire.MsgTx{}
	//   msgTx.Deserialize(bytes.NewReader(signedTx))
	//   hash, err := client.SendRawTransaction(&msgTx, false)
	//   return hash.String(), err
	return "", fmt.Errorf("broadcasting not yet implemented for Bitcoin")
}
