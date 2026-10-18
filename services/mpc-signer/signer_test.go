package main

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"forge-crypto/mpc-signer/internal/ethcrypto"
	"forge-crypto/mpc-signer/internal/ethtypes"
)

// A deterministic test key so the signer address is stable.
const testKey = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

func newTestSigner(t *testing.T) *MPCSigner {
	t.Helper()
	s, err := NewMPCSigner(testKey)
	if err != nil {
		t.Fatalf("NewMPCSigner: %v", err)
	}
	return s
}

func decodeHex(t *testing.T, h string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimPrefix(h, "0x"))
	if err != nil {
		t.Fatalf("decode %q: %v", h, err)
	}
	return b
}

// These used to decode the signed transaction with go-ethereum's own
// parser and ask it who the sender was, which was the strongest available
// check while go-ethereum was a dependency.
//
// It is no longer available, and it is no longer the strongest check
// either. internal/ethtypes is pinned byte for byte to transactions
// go-ethereum produced -- the signing hash, the encoded transaction and
// the transaction hash, across both transaction types, contract creation,
// zero values and long calldata. That comparison now happens on every test
// run against a committed golden file, rather than depending on the
// library still being present.
//
// What is left to check here is this service's own behaviour: that the
// request it was given becomes the transaction it claims to have signed.

func TestSignLegacy(t *testing.T) {
	s := newTestSigner(t)
	out, err := s.SignTransaction(context.Background(), &SignRequest{
		ChainID:  11155111,
		To:       "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
		Value:    "1000000000000000",
		GasLimit: 21000,
		GasPrice: "20000000000",
		Nonce:    7,
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// A legacy transaction is bare RLP, so its first byte is a list
	// prefix. A typed one would start with its type byte instead.
	raw := decodeHex(t, out.RawTx)
	if raw[0] < 0xc0 {
		t.Errorf("first byte %#x is a type prefix; this should be a legacy transaction", raw[0])
	}
	if out.From != s.Address() {
		t.Errorf("signed as %s, want %s", out.From, s.Address())
	}
	if len(decodeHex(t, out.Signature)) != 65 {
		t.Error("the audit signature is not 65 bytes")
	}
	// The hash is keccak of exactly the bytes being returned. If it were
	// computed over anything else, a customer tracking their transaction
	// would be watching for an id that never appears on chain.
	if want := "0x" + hex.EncodeToString(ethcrypto.Keccak256(raw)); !strings.EqualFold(out.Hash, want) {
		t.Errorf("hash %s is not the keccak of the returned bytes (%s)", out.Hash, want)
	}
}

func TestSignEIP1559(t *testing.T) {
	s := newTestSigner(t)
	out, err := s.SignTransaction(context.Background(), &SignRequest{
		ChainID:              11155111,
		To:                   "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
		Value:                "0",
		Data:                 "0xabcdef",
		GasLimit:             50000,
		Nonce:                3,
		MaxFeePerGas:         "30000000000",
		MaxPriorityFeePerGas: "2000000000",
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	raw := decodeHex(t, out.RawTx)
	if raw[0] != ethtypes.DynamicFeeTxType {
		t.Errorf("first byte %#x, want the EIP-1559 type byte %#x", raw[0], ethtypes.DynamicFeeTxType)
	}
	if out.From != s.Address() {
		t.Errorf("signed as %s, want %s", out.From, s.Address())
	}
}

// The signature the service returns must recover to the address it says
// signed. This is the property the whole audit trail rests on.
func TestTheReturnedSignatureRecoversToTheSigner(t *testing.T) {
	s := newTestSigner(t)
	req := &SignRequest{
		ChainID: 11155111, To: "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
		Value: "1", GasLimit: 21000, GasPrice: "1", Nonce: 0,
	}
	out, err := s.SignTransaction(context.Background(), req)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Rebuild the digest independently of the signing path.
	tx := &ethtypes.Transaction{
		ChainID:  bigFromInt(11155111),
		Nonce:    0,
		GasPrice: bigFromInt(1),
		Gas:      21000,
		To:       mustAddress(t, req.To),
		Value:    bigFromInt(1),
	}
	digest, err := tx.SigningHash()
	if err != nil {
		t.Fatalf("signing hash: %v", err)
	}
	got, err := ethcrypto.RecoverAddress(digest, decodeHex(t, out.Signature))
	if err != nil {
		t.Fatalf("recovering: %v", err)
	}
	if got != s.Address() {
		t.Errorf("the returned signature recovers to %s, not the signer %s", got, s.Address())
	}
}

// Two signings of the same request must produce identical bytes. RFC 6979
// makes the nonce deterministic, so a difference here would mean something
// in the encoding path is not a function of its inputs.
func TestSigningIsDeterministic(t *testing.T) {
	s := newTestSigner(t)
	req := func() *SignRequest {
		return &SignRequest{
			ChainID: 1, To: "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
			Value: "5", GasLimit: 21000, GasPrice: "3", Nonce: 11,
		}
	}
	a, err := s.SignTransaction(context.Background(), req())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	b, err := s.SignTransaction(context.Background(), req())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if a.RawTx != b.RawTx || a.Hash != b.Hash {
		t.Error("the same request signed twice produced different transactions")
	}
}

// The same request on two chains must produce different bytes, or a
// signature captured on a testnet would spend on mainnet.
func TestTheChainIdChangesTheSignedBytes(t *testing.T) {
	s := newTestSigner(t)
	sign := func(chain int) string {
		out, err := s.SignTransaction(context.Background(), &SignRequest{
			ChainID: chain, To: "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
			Value: "1", GasLimit: 21000, GasPrice: "1", Nonce: 0,
		})
		if err != nil {
			t.Fatalf("chain %d: %v", chain, err)
		}
		return out.RawTx
	}
	if sign(1) == sign(11155111) {
		t.Fatal("mainnet and Sepolia produced identical bytes; the chain id is not bound in")
	}
}

func TestSignRejectsBadAddress(t *testing.T) {
	s := newTestSigner(t)
	for _, to := range []string{"not-an-address", "", "0x1234", "0x" + strings.Repeat("z", 40)} {
		_, err := s.SignTransaction(context.Background(), &SignRequest{
			ChainID: 1, To: to, GasLimit: 21000, GasPrice: "1",
		})
		if err == nil {
			t.Errorf("%q was accepted as a recipient", to)
		}
	}
}

func TestSignRejectsAnUnparseableAmount(t *testing.T) {
	s := newTestSigner(t)
	_, err := s.SignTransaction(context.Background(), &SignRequest{
		ChainID: 1, To: "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
		Value: "not a number", GasLimit: 21000, GasPrice: "1",
	})
	if err == nil {
		t.Fatal("an unparseable value was accepted")
	}
}

func TestStableAddressFromKey(t *testing.T) {
	a := newTestSigner(t).Address()
	b := newTestSigner(t).Address()
	if a != b {
		t.Fatalf("address not stable: %s vs %s", a, b)
	}
	// And it is the well-known address for this widely published key.
	if !strings.EqualFold(a, "0x70997970C51812dc3A010C7d01b50e0d17dc79C8") {
		t.Errorf("the test key derived %s", a)
	}
}

func bigFromInt(n int64) *big.Int { return big.NewInt(n) }

func mustAddress(t *testing.T, s string) []byte {
	t.Helper()
	a, err := ethcrypto.ParseAddress(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return a
}
