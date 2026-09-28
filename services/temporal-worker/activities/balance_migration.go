package activities

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"

	"forge-crypto/temporal-worker/internal/ethcrypto"
	"forge-crypto/temporal-worker/internal/ethrpc"
	"forge-crypto/temporal-worker/internal/ethtypes"

	"forge-crypto/temporal-worker/workflows"
)

// sweepGasLimit is a plain ETH transfer's gas cost -- a sweep moves the
// old address's entire spare balance to the new one and carries no
// calldata, so there's nothing here that would ever need more than this.
const sweepGasLimit = 21000

// newLegacySweepTx builds the exact same *types.Transaction from the same
// plain fields in both BuildSweepTransaction (to compute the hash to
// sign) and AssembleSweepTransaction (to reapply the resulting signature)
// -- a single shared constructor so the two can never drift into hashing
// one transaction and assembling a different one, which would silently
// produce a signature that doesn't match the broadcast tx.
func newLegacySweepTx(nonce uint64, gasPrice, value *big.Int, gasLimit uint64, to []byte, evmChainID int64) *ethtypes.Transaction {
	return &ethtypes.Transaction{
		ChainID:  big.NewInt(evmChainID),
		Nonce:    nonce,
		GasPrice: gasPrice,
		Gas:      gasLimit,
		To:       to,
		Value:    value,
	}
}

// BuildSweepTransaction queries the retiring address's real on-chain
// balance and gas price, and builds (but does not sign) a transaction
// moving everything above the gas cost to the replacement address.
// Returns Skipped=true rather than an error when there's nothing worth
// sweeping (balance doesn't even cover gas) -- that's an expected outcome
// for a key that was rotated before ever receiving funds, not a failure.
func (a *Activities) BuildSweepTransaction(ctx context.Context, req workflows.BuildSweepTransactionRequest) (*workflows.BuildSweepTransactionResult, error) {
	if !ethcrypto.IsHexAddress(req.OldAddress) {
		return nil, fmt.Errorf("invalid old address: %q", req.OldAddress)
	}
	toAddr, err := ethcrypto.ParseAddress(req.NewAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid new address: %q", req.NewAddress)
	}

	client, err := ethrpc.Dial(a.EthereumRPC)
	if err != nil {
		return nil, fmt.Errorf("dial RPC: %w", err)
	}

	balance, err := client.BalanceAt(ctx, req.OldAddress)
	if err != nil {
		return nil, fmt.Errorf("get balance: %w", err)
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("suggest gas price: %w", err)
	}

	fee := new(big.Int).Mul(gasPrice, big.NewInt(sweepGasLimit))
	value := new(big.Int).Sub(balance, fee)
	if value.Sign() <= 0 {
		return &workflows.BuildSweepTransactionResult{Skipped: true, BalanceWei: balance.String()}, nil
	}

	nonce, err := client.PendingNonceAt(ctx, req.OldAddress)
	if err != nil {
		return nil, fmt.Errorf("get nonce: %w", err)
	}

	hash, err := newLegacySweepTx(nonce, gasPrice, value, sweepGasLimit, toAddr, req.EVMChainID).SigningHash()
	if err != nil {
		return nil, fmt.Errorf("compute the signing hash: %w", err)
	}

	return &workflows.BuildSweepTransactionResult{
		BalanceWei:  balance.String(),
		Nonce:       nonce,
		GasLimit:    sweepGasLimit,
		GasPriceWei: gasPrice.String(),
		ValueWei:    value.String(),
		TxHashHex:   hex.EncodeToString(hash),
	}, nil
}

// AssembleSweepTransaction rebuilds the identical unsigned transaction
// BuildSweepTransaction hashed (see newLegacySweepTx), applies the
// threshold signature ExecuteRealSigning produced over that hash, and
// verifies the signature actually recovers to ExpectedFrom before
// returning anything -- a mismatch means either a bug in this
// composition or a real integrity failure, and either way the caller
// must never receive a "signed" transaction from the wrong signer.
//
// This is the same 65-byte [R||S||V] (V = 0/1 recovery id) format
// services/mpc-signer/signer.go's compactSignature produces and
// services/temporal-worker/activities/real_tss_live_test.go already
// proved crypto.SigToPub recovers correctly from a real tss-lib
// signature -- go-ethereum's types.Signer.SignatureValues expects
// exactly this shape, independent of chain-specific V encoding, which is
// why the same format works for both.
//
// Verified in this codebase against a real threshold signature from real
// mpc-party processes (see balance_migration_live_test.go); NOT verified
// against a real broadcast to a funded chain -- no funded testnet address
// or live RPC endpoint exists in this environment, so the transaction
// this produces has been proven cryptographically valid, not proven to
// actually move funds on a real network.
func (a *Activities) AssembleSweepTransaction(ctx context.Context, req workflows.AssembleSweepTransactionRequest) (*workflows.AssembleSweepTransactionResult, error) {
	toAddr, err := ethcrypto.ParseAddress(req.NewAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid new address: %q", req.NewAddress)
	}
	if !ethcrypto.IsHexAddress(req.ExpectedFrom) {
		return nil, fmt.Errorf("invalid expected sender address: %q", req.ExpectedFrom)
	}

	gasPrice, ok := new(big.Int).SetString(req.GasPriceWei, 10)
	if !ok {
		return nil, fmt.Errorf("invalid gasPriceWei %q", req.GasPriceWei)
	}
	value, ok := new(big.Int).SetString(req.ValueWei, 10)
	if !ok {
		return nil, fmt.Errorf("invalid valueWei %q", req.ValueWei)
	}

	sigBytes, err := hex.DecodeString(req.Signature)
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	if len(sigBytes) != 65 {
		return nil, fmt.Errorf("signature must be 65 bytes, got %d", len(sigBytes))
	}

	tx := newLegacySweepTx(req.Nonce, gasPrice, value, req.GasLimit, toAddr, req.EVMChainID)

	// WithSignature recovers the sender itself and refuses when it is not
	// the address expected, so the check that used to be written out here
	// now happens inside the one place that assembles a transaction.
	signed, err := tx.WithSignature(sigBytes, req.ExpectedFrom)
	if err != nil {
		return nil, fmt.Errorf("apply signature: %w", err)
	}

	return &workflows.AssembleSweepTransactionResult{
		SignedTxHex: "0x" + hex.EncodeToString(signed.Raw),
		TxHash:      "0x" + hex.EncodeToString(signed.Hash),
	}, nil
}
