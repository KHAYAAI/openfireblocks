package workflows

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// BalanceMigrationWorkflow sweeps any balance remaining at a retiring
// threshold address to its replacement -- the part of "key rotation" that
// actually matters for custody (see docs/security/key-rotation.md
// section 1: generating a new key and never moving funds off the old one
// isn't a rotation, it's provisioning a second key). Signs with the OLD
// ceremony's threshold key via the same real tss-lib signing path
// ThresholdSigningWorkflow uses (ExecuteRealSigning), not a re-derived or
// simulated one -- the old key material never left Vault/mpc-party's
// in-memory ceremony state to make this possible.
//
// Ethereum only today: BuildSweepTransaction/AssembleSweepTransaction
// construct a real go-ethereum LegacyTx, get it hashed and signed the
// exact way real_tss_live_test.go already proved recovers correctly to a
// tss-lib-derived address, then verify the recovered sender matches
// OldAddress before returning anything -- a mismatched signature is
// refused rather than handed back as if it were valid. Not run against a
// real funded chain in this codebase (see the doc comment on
// AssembleSweepTransaction's activity): the RLP-encode/sign/verify
// composition is proven, actual broadcast against a funded address isn't.
func BalanceMigrationWorkflow(ctx workflow.Context, req BalanceMigrationRequest) (*BalanceMigrationResult, error) {
	logger := workflow.GetLogger(ctx)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout:    5 * time.Minute,
		ScheduleToCloseTimeout: 15 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var built BuildSweepTransactionResult
	if err := workflow.ExecuteActivity(ctx, "BuildSweepTransaction", BuildSweepTransactionRequest{
		OldAddress: req.OldAddress,
		NewAddress: req.NewAddress,
		EVMChainID: req.EVMChainID,
	}).Get(ctx, &built); err != nil {
		logger.Error("failed to build sweep transaction", "error", err)
		return &BalanceMigrationResult{Status: "failed", Error: err.Error()}, nil
	}
	if built.Skipped {
		logger.Info("nothing to migrate: old address balance doesn't cover gas", "oldAddress", req.OldAddress, "balanceWei", built.BalanceWei)
		return &BalanceMigrationResult{Status: "skipped_zero_balance", SweptWei: built.BalanceWei}, nil
	}

	var signResult ThresholdSigningResult
	if err := workflow.ExecuteActivity(ctx, "ExecuteRealSigning", ThresholdSigningRequest{
		CeremonyID:     req.OldCeremonyID,
		Message:        built.TxHashHex,
		PartyIDs:       req.PartyIDs,
		PartyEndpoints: req.PartyEndpoints,
		ChainID:        req.ChainID,
	}).Get(ctx, &signResult); err != nil {
		logger.Error("threshold signing of sweep tx failed", "error", err)
		return &BalanceMigrationResult{Status: "failed", Error: err.Error()}, nil
	}
	if signResult.Status != "completed" {
		logger.Error("threshold signing of sweep tx did not complete", "error", signResult.Error)
		return &BalanceMigrationResult{Status: "failed", Error: signResult.Error}, nil
	}

	var assembled AssembleSweepTransactionResult
	if err := workflow.ExecuteActivity(ctx, "AssembleSweepTransaction", AssembleSweepTransactionRequest{
		NewAddress:   req.NewAddress,
		Nonce:        built.Nonce,
		GasLimit:     built.GasLimit,
		GasPriceWei:  built.GasPriceWei,
		ValueWei:     built.ValueWei,
		EVMChainID:   req.EVMChainID,
		Signature:    signResult.Signature,
		ExpectedFrom: req.OldAddress,
	}).Get(ctx, &assembled); err != nil {
		logger.Error("failed to assemble signed sweep transaction", "error", err)
		return &BalanceMigrationResult{Status: "failed", Error: err.Error()}, nil
	}

	var broadcastResult BroadcastResult
	if err := workflow.ExecuteActivity(ctx, "BroadcastTransaction", assembled.SignedTxHex).Get(ctx, &broadcastResult); err != nil {
		// The signed tx itself is real and valid (verified inside
		// AssembleSweepTransaction) even if broadcasting it failed --
		// report what was actually produced rather than "failed", so an
		// operator can rebroadcast assembled.SignedTxHex by hand.
		logger.Warn("sweep tx built and signed but broadcast failed", "error", err)
		return &BalanceMigrationResult{
			Status:      "signed_not_broadcast",
			SweptWei:    built.ValueWei,
			SignedTxHex: assembled.SignedTxHex,
			Error:       fmt.Sprintf("broadcast failed: %v", err),
		}, nil
	}

	logger.Info("balance migration completed", "oldAddress", req.OldAddress, "newAddress", req.NewAddress, "sweptWei", built.ValueWei, "txHash", broadcastResult.TxHash)
	return &BalanceMigrationResult{
		Status:      "completed",
		SweptWei:    built.ValueWei,
		SignedTxHex: assembled.SignedTxHex,
		TxHash:      broadcastResult.TxHash,
	}, nil
}
