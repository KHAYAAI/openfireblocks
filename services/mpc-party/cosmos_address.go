package main

import (
	"crypto/sha256"
	"fmt"

	"github.com/btcsuite/btcutil/bech32"
	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // Cosmos address derivation is defined in terms of RIPEMD160.
)

// CosmosAddress derives a bech32 address from a COMPRESSED (33-byte)
// secp256k1 public key: bech32(prefix, RIPEMD160(SHA256(pubkey))).
//
// The same derivation lives in mpc-signer/chains/cosmos.go; the two modules
// share no code, and each pins the other with a known-answer test so a
// drift in either is caught.
func CosmosAddress(compressedPubKey []byte, prefix string) (string, error) {
	if len(compressedPubKey) != 33 {
		return "", fmt.Errorf("expected a 33-byte compressed public key, got %d bytes", len(compressedPubKey))
	}
	if prefix == "" {
		return "", fmt.Errorf("a bech32 prefix is required")
	}
	sha := sha256.Sum256(compressedPubKey)
	h := ripemd160.New()
	h.Write(sha[:])
	conv, err := bech32.ConvertBits(h.Sum(nil), 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("bech32 conversion: %w", err)
	}
	return bech32.Encode(prefix, conv)
}
