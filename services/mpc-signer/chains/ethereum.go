package chains

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"

	"strings"

	"forge-crypto/mpc-signer/internal/ethcrypto"
	"forge-crypto/mpc-signer/internal/ethtypes"
)

// EthereumSigner implements ChainSigner for Ethereum.
type EthereumSigner struct{}

// NewEthereumSigner creates a new Ethereum signer.
func NewEthereumSigner() ChainSigner {
	return &EthereumSigner{}
}

// SignMessage signs a message hash for Ethereum.
func (e *EthereumSigner) SignMessage(ctx context.Context, messageHash []byte, privKeyHex string) (*Signature, error) {
	privKey, err := ethcrypto.HexToECDSA(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	signature, err := ethcrypto.Sign(messageHash, privKey)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	// signature is 65 bytes: [R (32) || S (32) || V (1)]
	return &Signature{
		R:              hex.EncodeToString(signature[:32]),
		S:              hex.EncodeToString(signature[32:64]),
		V:              signature[64],
		SignatureBytes: "0x" + hex.EncodeToString(signature),
	}, nil
}

// VerifySignature verifies an Ethereum signature.
func (e *EthereumSigner) VerifySignature(ctx context.Context, messageHash []byte, signature *Signature, pubKey string) (bool, error) {
	pubKeyBytes, err := hex.DecodeString(pubKey)
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}

	sigBytes, err := hex.DecodeString(signature.SignatureBytes[2:])
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}

	recovered := ethcrypto.VerifySignature(pubKeyBytes, messageHash, sigBytes[:64])
	return recovered, nil
}

// RecoverAddress recovers the signer address from a message and signature.
func (e *EthereumSigner) RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error) {
	sigBytes, err := hex.DecodeString(signature.SignatureBytes[2:])
	if err != nil {
		return "", fmt.Errorf("invalid signature: %w", err)
	}

	x, y, err := ethcrypto.SigToPub(messageHash, sigBytes)
	if err != nil {
		return "", fmt.Errorf("recovery failed: %w", err)
	}

	addr := ethcrypto.Address(x, y)
	return addr, nil
}

// BuildTransaction builds an Ethereum transaction ready for signing.
func (e *EthereumSigner) BuildTransaction(ctx context.Context, txData interface{}) ([]byte, error) {
	req, ok := txData.(*EthereumSignRequest)
	if !ok {
		return nil, fmt.Errorf("invalid transaction type for Ethereum")
	}

	to, err := ethcrypto.ParseAddress(req.To)
	if err != nil {
		return nil, fmt.Errorf("invalid 'to' address: %q", req.To)
	}

	value, ok := new(big.Int).SetString(req.Value, 10)
	if !ok {
		return nil, fmt.Errorf("value is not a base-10 integer: %q", req.Value)
	}
	gasPrice, ok := new(big.Int).SetString(req.GasPrice, 10)
	if !ok {
		return nil, fmt.Errorf("gasPrice is not a base-10 integer: %q", req.GasPrice)
	}

	var data []byte
	if trimmed := strings.TrimPrefix(req.Data, "0x"); trimmed != "" {
		data, err = hex.DecodeString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
	}

	tx := &ethtypes.Transaction{
		ChainID:  big.NewInt(int64(req.ChainID)),
		Nonce:    req.Nonce,
		GasPrice: gasPrice,
		Gas:      req.GasLimit,
		To:       to,
		Value:    value,
		Data:     data,
	}

	// The EIP-155 signing hash: what actually gets signed.
	return tx.SigningHash()
}

// BroadcastTransaction broadcasts a signed Ethereum transaction (stub).
func (e *EthereumSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	// TODO: implement RPC broadcast
	return "", fmt.Errorf("broadcasting not yet implemented for Ethereum")
}
