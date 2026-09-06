package chains

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// EthereumSigner implements ChainSigner for Ethereum.
type EthereumSigner struct{}

// NewEthereumSigner creates a new Ethereum signer.
func NewEthereumSigner() ChainSigner {
	return &EthereumSigner{}
}

// SignMessage signs a message hash for Ethereum.
func (e *EthereumSigner) SignMessage(ctx context.Context, messageHash []byte, privKeyHex string) (*Signature, error) {
	privKey, err := crypto.HexToECDSA(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	signature, err := crypto.Sign(messageHash, privKey)
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

	recovered := crypto.VerifySignature(pubKeyBytes, messageHash, sigBytes[:64])
	return recovered, nil
}

// RecoverAddress recovers the signer address from a message and signature.
func (e *EthereumSigner) RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error) {
	sigBytes, err := hex.DecodeString(signature.SignatureBytes[2:])
	if err != nil {
		return "", fmt.Errorf("invalid signature: %w", err)
	}

	pubKey, err := crypto.SigToPub(messageHash, sigBytes)
	if err != nil {
		return "", fmt.Errorf("recovery failed: %w", err)
	}

	addr := crypto.PubkeyToAddress(*pubKey)
	return addr.Hex(), nil
}

// BuildTransaction builds an Ethereum transaction ready for signing.
func (e *EthereumSigner) BuildTransaction(ctx context.Context, txData interface{}) ([]byte, error) {
	req, ok := txData.(*EthereumSignRequest)
	if !ok {
		return nil, fmt.Errorf("invalid transaction type for Ethereum")
	}

	if !common.IsHexAddress(req.To) {
		return nil, fmt.Errorf("invalid 'to' address: %q", req.To)
	}

	to := common.HexToAddress(req.To)
	value := new(big.Int)
	value.SetString(req.Value, 10)

	gasPrice := new(big.Int)
	gasPrice.SetString(req.GasPrice, 10)

	data, err := hexutil.Decode(req.Data)
	if err != nil {
		return nil, fmt.Errorf("invalid data: %w", err)
	}

	// Build legacy (EIP-155) transaction
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    req.Nonce,
		GasPrice: gasPrice,
		Gas:      req.GasLimit,
		To:       &to,
		Value:    value,
		Data:     data,
	})

	// Return RLP-encoded transaction hash for signing
	signer := types.NewEIP155Signer(big.NewInt(int64(req.ChainID)))
	hash := signer.Hash(tx)
	return hash[:], nil
}

// BroadcastTransaction broadcasts a signed Ethereum transaction (stub).
func (e *EthereumSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	// TODO: implement RPC broadcast
	return "", fmt.Errorf("broadcasting not yet implemented for Ethereum")
}
