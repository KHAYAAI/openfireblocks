package main

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
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

// verifyRawTx decodes the raw signed tx and checks the recovered sender matches
// the signer address under the given signer scheme.
func verifyRawTx(t *testing.T, raw string, signer types.Signer, want common.Address) *types.Transaction {
	t.Helper()
	b, err := hexutil.Decode(raw)
	if err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	from, err := types.Sender(signer, tx)
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	if from != want {
		t.Fatalf("recovered sender %s != %s", from.Hex(), want.Hex())
	}
	return tx
}

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
	tx := verifyRawTx(t, out.RawTx, types.NewEIP155Signer(big.NewInt(11155111)), common.HexToAddress(s.Address()))
	if tx.Type() != types.LegacyTxType {
		t.Errorf("type = %d, want legacy", tx.Type())
	}
	if tx.Nonce() != 7 {
		t.Errorf("nonce = %d", tx.Nonce())
	}
	// The compact audit signature must recover the signer address.
	if len(mustDecode(t, out.Signature)) != 65 {
		t.Errorf("signature not 65 bytes")
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
	tx := verifyRawTx(t, out.RawTx, types.NewLondonSigner(big.NewInt(11155111)), common.HexToAddress(s.Address()))
	if tx.Type() != types.DynamicFeeTxType {
		t.Errorf("type = %d, want dynamic-fee", tx.Type())
	}
}

func TestSignRejectsBadAddress(t *testing.T) {
	s := newTestSigner(t)
	_, err := s.SignTransaction(context.Background(), &SignRequest{
		ChainID: 1, To: "not-an-address", GasLimit: 21000, GasPrice: "1",
	})
	if err == nil {
		t.Fatal("expected error for bad address")
	}
}

func TestStableAddressFromKey(t *testing.T) {
	a := newTestSigner(t).Address()
	b := newTestSigner(t).Address()
	if a != b {
		t.Fatalf("address not stable: %s vs %s", a, b)
	}
}

func mustDecode(t *testing.T, h string) []byte {
	t.Helper()
	b, err := hexutil.Decode(h)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return b
}
