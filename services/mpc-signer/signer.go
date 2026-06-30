package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// signer.go holds the actual Ethereum transaction signing logic.
//
// Phase 0: a single shared ECDSA (secp256k1) key signs every transaction.
// This is intentionally simple so we can prove the end-to-end flow
// (request -> sign -> broadcast -> audit). It is NOT production-safe.
//
// Phase 1+: replace the single key with Binance TSS-Lib threshold signing,
// where the private key is never reconstructed and key shares are stored in
// HashiCorp Vault.

// SignedTransaction is the result of signing a SignRequest.
type SignedTransaction struct {
	RawTx     string // 0x-prefixed RLP-encoded signed transaction (ready to broadcast)
	Signature string // 0x-prefixed 65-byte [R || S || V] signature
	Hash      string // 0x-prefixed transaction hash
	From      string // signer address
}

// MPCSigner owns the signing key material.
type MPCSigner struct {
	privKey *ecdsa.PrivateKey
	address common.Address
}

// NewMPCSigner loads the shared signing key.
//
// If MPC_SIGNER_PRIVATE_KEY is set (0x-prefixed or bare hex), it is used so the
// signer address is stable across restarts (needed for nonce management in
// local testing). Otherwise a fresh ephemeral key is generated.
func NewMPCSigner(privKeyHex string) (*MPCSigner, error) {
	var (
		privKey *ecdsa.PrivateKey
		err     error
	)

	if privKeyHex != "" {
		// Accept an optional 0x prefix.
		if len(privKeyHex) >= 2 && privKeyHex[:2] == "0x" {
			privKeyHex = privKeyHex[2:]
		}
		privKey, err = crypto.HexToECDSA(privKeyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid MPC_SIGNER_PRIVATE_KEY: %w", err)
		}
	} else {
		privKey, err = crypto.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate signing key: %w", err)
		}
	}

	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	return &MPCSigner{privKey: privKey, address: addr}, nil
}

// Address returns the signer's Ethereum address.
func (m *MPCSigner) Address() string {
	return m.address.Hex()
}

// SignTransaction builds an EIP-155 legacy Ethereum transaction from the request
// and signs it with the shared key.
func (m *MPCSigner) SignTransaction(ctx context.Context, req *SignRequest) (*SignedTransaction, error) {
	if !common.IsHexAddress(req.To) {
		return nil, fmt.Errorf("invalid 'to' address: %q", req.To)
	}
	toAddr := common.HexToAddress(req.To)

	// Parse value (wei). Empty or "0" both mean zero.
	value := new(big.Int)
	if req.Value != "" && req.Value != "0" {
		if _, ok := value.SetString(req.Value, 10); !ok {
			return nil, fmt.Errorf("invalid value: %q", req.Value)
		}
	}

	// Parse gas price (wei per gas).
	gasPrice := new(big.Int)
	if _, ok := gasPrice.SetString(req.GasPrice, 10); !ok {
		return nil, fmt.Errorf("invalid gasPrice: %q", req.GasPrice)
	}

	// Decode optional call data.
	var data []byte
	if req.Data != "" && req.Data != "0x" {
		decoded, err := hexutil.Decode(req.Data)
		if err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
		data = decoded
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    req.Nonce,
		GasPrice: gasPrice,
		Gas:      req.GasLimit,
		To:       &toAddr,
		Value:    value,
		Data:     data,
	})

	// EIP-155 replay-protected signing for the requested chain.
	chainID := big.NewInt(int64(req.ChainID))
	signer := types.NewEIP155Signer(chainID)

	signedTx, err := types.SignTx(tx, signer, m.privKey)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	rawTx, err := signedTx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to RLP-encode signed tx: %w", err)
	}

	// Reconstruct the 65-byte [R || S || V] signature for the audit trail.
	v, r, s := signedTx.RawSignatureValues()
	sig := make([]byte, 65)
	r.FillBytes(sig[0:32])
	s.FillBytes(sig[32:64])
	sig[64] = byte(v.Uint64())

	return &SignedTransaction{
		RawTx:     hexutil.Encode(rawTx),
		Signature: hexutil.Encode(sig),
		Hash:      signedTx.Hash().Hex(),
		From:      m.address.Hex(),
	}, nil
}
