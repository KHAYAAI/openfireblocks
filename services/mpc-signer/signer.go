package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"forge-crypto/mpc-signer/internal/ethcrypto"
	"forge-crypto/mpc-signer/internal/ethtypes"
	"forge-crypto/mpc-signer/keys"
)

// signer.go holds the single-key Ethereum transaction signing logic.
//
// This is the non-threshold path: one secp256k1 key signs. Threshold
// signing, where no party ever holds the whole key, is
// services/mpc-party, and it is a separate path rather than a mode of
// this one.
//
// Where the key lives is a KeySigner (see ./keys): a software key in
// process memory by default, or -- with HSM_PKCS11_* configured and a
// binary built with -tags pkcs11 -- a key inside a hardware security
// module that signs without ever releasing it.

// SignedTransaction is the result of signing a SignRequest.
type SignedTransaction struct {
	RawTx     string // 0x-prefixed RLP-encoded signed transaction (ready to broadcast)
	Signature string // 0x-prefixed 65-byte [R || S || V] signature
	Hash      string // 0x-prefixed transaction hash
	From      string // signer address
}

// MPCSigner signs Ethereum transactions with a KeySigner.
//
// It no longer holds key material itself. It holds something that can
// sign, which may be a key in this process's memory or a handle to one
// that never leaves an HSM, and it does not know which.
type MPCSigner struct {
	key     keys.KeySigner
	address string
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
		privKey, err = ethcrypto.HexToECDSA(privKeyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid MPC_SIGNER_PRIVATE_KEY: %w", err)
		}
	} else {
		privKey, err = ethcrypto.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate signing key: %w", err)
		}
	}

	return NewMPCSignerFromKey(keys.NewRawKeySigner(privKey))
}

// NewMPCSignerFromKey builds a signer around any KeySigner, including one
// backed by hardware.
func NewMPCSignerFromKey(key keys.KeySigner) (*MPCSigner, error) {
	addr, err := keys.Address(key)
	if err != nil {
		return nil, fmt.Errorf("deriving the signer address: %w", err)
	}
	return &MPCSigner{key: key, address: addr}, nil
}

// Key returns the KeySigner this signer uses, so the multi-chain router
// can sign with the same key rather than a second copy of it.
func (m *MPCSigner) Key() keys.KeySigner { return m.key }

// Address returns the signer's Ethereum address.
func (m *MPCSigner) Address() string {
	return m.address
}

// SignTransaction builds an Ethereum transaction from the request (legacy or
// EIP-1559 depending on the fee fields) and signs it with the shared key.
func (m *MPCSigner) SignTransaction(ctx context.Context, req *SignRequest) (*SignedTransaction, error) {
	to, err := ethcrypto.ParseAddress(req.To)
	if err != nil {
		return nil, fmt.Errorf("invalid 'to' address: %q", req.To)
	}

	value, err := parseBig(req.Value, true)
	if err != nil {
		return nil, fmt.Errorf("invalid value: %w", err)
	}

	data, err := decodeData(req.Data)
	if err != nil {
		return nil, err
	}

	tx := &ethtypes.Transaction{
		ChainID: big.NewInt(int64(req.ChainID)),
		Nonce:   req.Nonce,
		Gas:     req.GasLimit,
		To:      to,
		Value:   value,
		Data:    data,
	}

	if req.MaxFeePerGas != "" && req.MaxPriorityFeePerGas != "" {
		if tx.MaxFee, err = parseBig(req.MaxFeePerGas, false); err != nil {
			return nil, fmt.Errorf("invalid maxFeePerGas: %w", err)
		}
		if tx.Tip, err = parseBig(req.MaxPriorityFeePerGas, false); err != nil {
			return nil, fmt.Errorf("invalid maxPriorityFeePerGas: %w", err)
		}
	} else {
		if tx.GasPrice, err = parseBig(req.GasPrice, false); err != nil {
			return nil, fmt.Errorf("invalid gasPrice: %w", err)
		}
	}

	// Sign the digest this service computed, rather than handing the
	// transaction to a library that computes its own. The two must agree,
	// and the only way to be sure is for there to be one of them.
	digest, err := tx.SigningHash()
	if err != nil {
		return nil, err
	}
	sig, err := m.key.SignDigest(ctx, digest)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	// WithSignature recovers the sender and refuses if it is not this key.
	// Redundant here, where the same process just signed with its own key,
	// and not redundant at all in the threshold path where the signature
	// comes back over the network from a committee.
	signed, err := tx.WithSignature(sig, m.address)
	if err != nil {
		return nil, err
	}

	return &SignedTransaction{
		RawTx:     "0x" + hex.EncodeToString(signed.Raw),
		Signature: "0x" + hex.EncodeToString(sig),
		Hash:      "0x" + hex.EncodeToString(signed.Hash),
		From:      signed.From,
	}, nil
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
	decoded, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid data: %w", err)
	}
	return decoded, nil
}
