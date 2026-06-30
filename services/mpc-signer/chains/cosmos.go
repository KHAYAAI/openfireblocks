package chains

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// CosmosSigner implements ChainSigner for Cosmos (secp256k1 ECDSA + Protobuf encoding).
type CosmosSigner struct{}

// NewCosmosSigner creates a new Cosmos signer.
func NewCosmosSigner() ChainSigner {
	return &CosmosSigner{}
}

// SignMessage signs a message hash for Cosmos (same ECDSA as Bitcoin/Ethereum).
func (c *CosmosSigner) SignMessage(ctx context.Context, messageHash []byte, privKeyHex string) (*Signature, error) {
	if len(privKeyHex) >= 2 && privKeyHex[:2] == "0x" {
		privKeyHex = privKeyHex[2:]
	}

	privKey, err := crypto.HexToECDSA(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	signature, err := crypto.Sign(messageHash, privKey)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	// Cosmos uses the same signature format as Ethereum
	return &Signature{
		R:              hex.EncodeToString(signature[:32]),
		S:              hex.EncodeToString(signature[32:64]),
		V:              signature[64],
		SignatureBytes: "0x" + hex.EncodeToString(signature),
	}, nil
}

// VerifySignature verifies a Cosmos signature.
func (c *CosmosSigner) VerifySignature(ctx context.Context, messageHash []byte, signature *Signature, pubKeyHex string) (bool, error) {
	if len(pubKeyHex) >= 2 && pubKeyHex[:2] == "0x" {
		pubKeyHex = pubKeyHex[2:]
	}

	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
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

// RecoverAddress recovers the Cosmos address from a message and signature.
// Cosmos addresses are Bech32-encoded with prefix "cosmos".
func (c *CosmosSigner) RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error) {
	sigBytes, err := hex.DecodeString(signature.SignatureBytes[2:])
	if err != nil {
		return "", fmt.Errorf("invalid signature: %w", err)
	}

	pubKey, err := crypto.SigToPub(messageHash, sigBytes)
	if err != nil {
		return "", fmt.Errorf("recovery failed: %w", err)
	}

	// Derive Cosmos address from public key
	pubKeyBytes := crypto.CompressPubkey(pubKey)
	addr := types.AccAddress(crypto.Keccak256(pubKeyBytes)[:20])
	return addr.String(), nil
}

// BuildTransaction builds a Cosmos transaction ready for signing (SignDoc format).
// Cosmos transactions must be signed over the SignDoc, not the full transaction.
func (c *CosmosSigner) BuildTransaction(ctx context.Context, txData interface{}) ([]byte, error) {
	req, ok := txData.(*CosmosSignRequest)
	if !ok {
		return nil, fmt.Errorf("invalid transaction type for Cosmos")
	}

	// Build SignDoc (the bytes to sign)
	// Format: {msgs: [...], fee: {...}, timeout: N, account_number: N, sequence: N, chain_id: "..."}
	// This is Protobuf-encoded, but for simplicity we'll hash the JSON representation
	// (Real implementation would use cosmos-sdk's auth.SignDoc)

	// Simplified: create a canonical JSON representation
	signDocStr := fmt.Sprintf(`{
		"msgs":%v,
		"fee":%v,
		"timeout_height":%d,
		"account_number":%d,
		"sequence":%d,
		"chain_id":"%s"
	}`, req.Messages, req.Fee, req.Timeout, req.AccountNum, req.Sequence, req.ChainID)

	// Hash the SignDoc (Cosmos uses SHA256)
	hash := sha256.Sum256([]byte(signDocStr))
	return hash[:], nil
}

// BroadcastTransaction broadcasts a signed Cosmos transaction.
func (c *CosmosSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	// TODO: Implement broadcast via Cosmos RPC
	// Example:
	//   client, _ := cosmosclient.NewClient(
	//     cosmosclient.WithChainID("cosmoshub-4"),
	//     cosmosclient.WithNodeAddress("https://cosmos-rpc.allthatnode.com:26657"),
	//   )
	//   hash, err := client.BroadcastTx(ctx, signedTx)
	//   return hash, err
	return "", fmt.Errorf("broadcasting not yet implemented for Cosmos")
}

// CosmosAddress converts a public key to a Cosmos Bech32 address.
func CosmosAddress(pubKeyHex string) (string, error) {
	if len(pubKeyHex) >= 2 && pubKeyHex[:2] == "0x" {
		pubKeyHex = pubKeyHex[2:]
	}

	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return "", fmt.Errorf("invalid public key: %w", err)
	}

	// Keccak256 hash and take first 20 bytes
	hash := crypto.Keccak256(pubKeyBytes)[:20]
	addr := types.AccAddress(hash)
	return addr.String(), nil
}
