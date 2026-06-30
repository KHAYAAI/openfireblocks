package chains

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
)

// CosmosSigner implements ChainSigner for Cosmos.
type CosmosSigner struct{}

// NewCosmosSigner creates a new Cosmos signer.
func NewCosmosSigner() ChainSigner {
	return &CosmosSigner{}
}

// SignMessage signs a message hash for Cosmos.
func (c *CosmosSigner) SignMessage(ctx context.Context, messageHash []byte, privKeyHex string) (*Signature, error) {
	privKey, err := crypto.HexToECDSA(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	signature, err := crypto.Sign(messageHash, privKey)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	// Cosmos uses the same ECDSA (secp256k1) as Ethereum
	return &Signature{
		R:              hex.EncodeToString(signature[:32]),
		S:              hex.EncodeToString(signature[32:64]),
		V:              signature[64],
		SignatureBytes: "0x" + hex.EncodeToString(signature),
	}, nil
}

// VerifySignature verifies a Cosmos signature.
func (c *CosmosSigner) VerifySignature(ctx context.Context, messageHash []byte, signature *Signature, pubKey string) (bool, error) {
	pubKeyBytes, err := hex.DecodeString(pubKey)
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}

	sigBytes, err := hex.DecodeString(signature.SignatureBytes[2:])
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}

	recovered := crypto.VerifySignature(pubKeyBytes, messageHash, sigBytes[:64])
	return recovered, nil
}

// RecoverAddress recovers the signer address from a message and signature.
func (c *CosmosSigner) RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error) {
	sigBytes, err := hex.DecodeString(signature.SignatureBytes[2:])
	if err != nil {
		return "", fmt.Errorf("invalid signature: %w", err)
	}

	pubKey, err := crypto.SigToPub(messageHash, sigBytes)
	if err != nil {
		return "", fmt.Errorf("recovery failed: %w", err)
	}

	// Cosmos uses Bech32 encoding with prefix "cosmos"
	// For now, return raw address
	addr := crypto.PubkeyToAddress(*pubKey)
	return addr.Hex(), nil
}

// BuildTransaction builds a Cosmos transaction ready for signing.
func (c *CosmosSigner) BuildTransaction(ctx context.Context, txData interface{}) ([]byte, error) {
	req, ok := txData.(*CosmosSignRequest)
	if !ok {
		return nil, fmt.Errorf("invalid transaction type for Cosmos")
	}

	// Build Cosmos SignDoc (the bytes to sign)
	// Format: {msgs: [...], fee: {...}, timeout: N, account_number: N, sequence: N, chain_id: "..."}
	// This is Protobuf-encoded, but for now we return a JSON representation

	// TODO: implement proper Protobuf encoding
	// For now, return a placeholder
	signDoc := map[string]interface{}{
		"msgs":            req.Messages,
		"fee":             req.Fee,
		"timeout_height":  req.Timeout,
		"account_number":  req.AccountNum,
		"sequence":        req.Sequence,
		"chain_id":        req.ChainID,
	}

	// Return JSON-encoded bytes (should be Protobuf in production)
	data, err := marshalJSON(signDoc)
	if err != nil {
		return nil, fmt.Errorf("marshaling failed: %w", err)
	}

	return data, nil
}

// BroadcastTransaction broadcasts a signed Cosmos transaction (stub).
func (c *CosmosSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	// TODO: implement RPC broadcast via Cosmos RPC
	return "", fmt.Errorf("broadcasting not yet implemented for Cosmos")
}

// marshalJSON is a placeholder for JSON marshaling.
func marshalJSON(v interface{}) ([]byte, error) {
	// TODO: use proper JSON marshaling
	return nil, fmt.Errorf("marshaling not implemented")
}
