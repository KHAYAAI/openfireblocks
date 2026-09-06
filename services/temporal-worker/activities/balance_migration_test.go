package activities

import (
	"context"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"forge-crypto/temporal-worker/workflows"
)

// TestAssembleSweepTransaction_ValidSignature proves the real
// build-hash-sign-assemble-verify composition works: build an unsigned
// sweep tx, sign its hash with a plain key (standing in for what a real
// tss-lib threshold signature produces -- crypto.Sign returns the exact
// same 65-byte [R||S||V] shape tss-lib's completeSigning assembles, see
// balance_migration.go's doc comment), and confirm AssembleSweepTransaction
// both accepts it and returns a signed tx that actually recovers to the
// signer's address. No RPC/live chain needed -- this is pure crypto, the
// same reason real_tss_live_test.go's recovery check doesn't need one
// either. balance_migration_live_test.go covers the remaining piece (a
// REAL threshold signature, not a plain-key stand-in) against real
// mpc-party processes.
func TestAssembleSweepTransaction_ValidSignature(t *testing.T) {
	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	fromAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	toAddr := common.HexToAddress("0x4444444444444444444444444444444444444444")

	const (
		nonce      = uint64(7)
		gasLimit   = uint64(21000)
		evmChainID = int64(11155111) // Sepolia
	)
	gasPrice := big.NewInt(20_000_000_000) // 20 gwei
	value := big.NewInt(1_000_000_000_000_000)

	tx, signer := newLegacySweepTx(nonce, gasPrice, value, gasLimit, toAddr, evmChainID)
	hash := signer.Hash(tx)

	sig, err := crypto.Sign(hash.Bytes(), privKey)
	if err != nil {
		t.Fatalf("sign hash: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("expected a 65-byte signature, got %d", len(sig))
	}

	a := NewActivities("", "", "", 3, nil)
	result, err := a.AssembleSweepTransaction(context.Background(), workflows.AssembleSweepTransactionRequest{
		NewAddress:   toAddr.Hex(),
		Nonce:        nonce,
		GasLimit:     gasLimit,
		GasPriceWei:  gasPrice.String(),
		ValueWei:     value.String(),
		EVMChainID:   evmChainID,
		Signature:    hex.EncodeToString(sig),
		ExpectedFrom: fromAddr.Hex(),
	})
	if err != nil {
		t.Fatalf("AssembleSweepTransaction returned an error: %v", err)
	}
	if result.SignedTxHex == "" || result.TxHash == "" {
		t.Fatalf("expected a non-empty signed tx and hash, got %+v", result)
	}
	t.Logf("assembled a real signed sweep tx recovering to %s: %s", fromAddr.Hex(), result.SignedTxHex)
}

// TestAssembleSweepTransaction_RejectsWrongSigner proves the sender-recovery
// check actually rejects a mismatch instead of silently returning an
// invalid transaction -- e.g. a signature from the wrong committee, or a
// bug elsewhere in the pipeline that hashed one tx but signed another.
func TestAssembleSweepTransaction_RejectsWrongSigner(t *testing.T) {
	signerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate signer key: %v", err)
	}
	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	claimedFrom := crypto.PubkeyToAddress(otherKey.PublicKey) // NOT the actual signer
	toAddr := common.HexToAddress("0x5555555555555555555555555555555555555555")

	const (
		nonce      = uint64(1)
		gasLimit   = uint64(21000)
		evmChainID = int64(11155111)
	)
	gasPrice := big.NewInt(20_000_000_000)
	value := big.NewInt(1)

	tx, signer := newLegacySweepTx(nonce, gasPrice, value, gasLimit, toAddr, evmChainID)
	hash := signer.Hash(tx)

	sig, err := crypto.Sign(hash.Bytes(), signerKey)
	if err != nil {
		t.Fatalf("sign hash: %v", err)
	}

	a := NewActivities("", "", "", 3, nil)
	_, err = a.AssembleSweepTransaction(context.Background(), workflows.AssembleSweepTransactionRequest{
		NewAddress:   toAddr.Hex(),
		Nonce:        nonce,
		GasLimit:     gasLimit,
		GasPriceWei:  gasPrice.String(),
		ValueWei:     value.String(),
		EVMChainID:   evmChainID,
		Signature:    hex.EncodeToString(sig),
		ExpectedFrom: claimedFrom.Hex(),
	})
	if err == nil {
		t.Fatal("expected AssembleSweepTransaction to reject a signature that doesn't recover to ExpectedFrom, got no error")
	}
	t.Logf("correctly rejected mismatched signature: %v", err)
}
