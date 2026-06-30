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

// SignTransaction builds an Ethereum transaction from the request (legacy or
// EIP-1559 depending on the fee fields) and signs it with the shared key.
func (m *MPCSigner) SignTransaction(ctx context.Context, req *SignRequest) (*SignedTransaction, error) {
	if !common.IsHexAddress(req.To) {
		return nil, fmt.Errorf("invalid 'to' address: %q", req.To)
	}
	toAddr := common.HexToAddress(req.To)

	value, err := parseBig(req.Value, true)
	if err != nil {
		return nil, fmt.Errorf("invalid value: %w", err)
	}

	data, err := decodeData(req.Data)
	if err != nil {
		return nil, err
	}

	chainID := big.NewInt(int64(req.ChainID))
	useDynamic := req.MaxFeePerGas != "" && req.MaxPriorityFeePerGas != ""

	var (
		tx     *types.Transaction
		signer types.Signer
	)
	if useDynamic {
		maxFee, err := parseBig(req.MaxFeePerGas, false)
		if err != nil {
			return nil, fmt.Errorf("invalid maxFeePerGas: %w", err)
		}
		tip, err := parseBig(req.MaxPriorityFeePerGas, false)
		if err != nil {
			return nil, fmt.Errorf("invalid maxPriorityFeePerGas: %w", err)
		}
		tx = types.NewTx(&types.DynamicFeeTx{
			ChainID:   chainID,
			Nonce:     req.Nonce,
			GasTipCap: tip,
			GasFeeCap: maxFee,
			Gas:       req.GasLimit,
			To:        &toAddr,
			Value:     value,
			Data:      data,
		})
		signer = types.NewLondonSigner(chainID)
	} else {
		gasPrice, err := parseBig(req.GasPrice, false)
		if err != nil {
			return nil, fmt.Errorf("invalid gasPrice: %w", err)
		}
		tx = types.NewTx(&types.LegacyTx{
			Nonce:    req.Nonce,
			GasPrice: gasPrice,
			Gas:      req.GasLimit,
			To:       &toAddr,
			Value:    value,
			Data:     data,
		})
		signer = types.NewEIP155Signer(chainID)
	}

	signedTx, err := types.SignTx(tx, signer, m.privKey)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	rawTx, err := signedTx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to RLP-encode signed tx: %w", err)
	}

	// Canonical 65-byte [R || S || recovery] signature for the audit trail.
	// Derived by trying both recovery ids and keeping the one that recovers the
	// signer address — correct for legacy (EIP-155 v) and 1559 (yParity) alike.
	sig, err := m.compactSignature(signer.Hash(tx), signedTx)
	if err != nil {
		return nil, err
	}

	return &SignedTransaction{
		RawTx:     hexutil.Encode(rawTx),
		Signature: hexutil.Encode(sig),
		Hash:      signedTx.Hash().Hex(),
		From:      m.address.Hex(),
	}, nil
}

// compactSignature returns the 65-byte [R || S || V] signature where V is the
// 0/1 recovery id, independent of the transaction's encoded V convention.
func (m *MPCSigner) compactSignature(hash common.Hash, signedTx *types.Transaction) ([]byte, error) {
	_, r, s := signedTx.RawSignatureValues()
	sig := make([]byte, 65)
	r.FillBytes(sig[0:32])
	s.FillBytes(sig[32:64])
	for v := byte(0); v <= 1; v++ {
		sig[64] = v
		pub, err := crypto.SigToPub(hash.Bytes(), sig)
		if err != nil {
			continue
		}
		if crypto.PubkeyToAddress(*pub) == m.address {
			return sig, nil
		}
	}
	return nil, fmt.Errorf("failed to derive recovery id for signature")
}

// parseBig parses a base-10 integer string. When zeroOK, "" and "0" yield 0.
func parseBig(s string, zeroOK bool) (*big.Int, error) {
	if s == "" || s == "0" {
		if zeroOK || s == "0" {
			return new(big.Int), nil
		}
		return nil, fmt.Errorf("empty value")
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 10); !ok {
		return nil, fmt.Errorf("not a base-10 integer: %q", s)
	}
	return n, nil
}

// decodeData decodes optional 0x-prefixed call data.
func decodeData(s string) ([]byte, error) {
	if s == "" || s == "0x" {
		return nil, nil
	}
	decoded, err := hexutil.Decode(s)
	if err != nil {
		return nil, fmt.Errorf("invalid data: %w", err)
	}
	return decoded, nil
}
