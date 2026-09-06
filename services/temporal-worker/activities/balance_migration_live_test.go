//go:build live

package activities

// Live integration test for the sweep-transaction half of balance
// migration: builds a real unsigned Ethereum transaction, gets it signed
// by a REAL threshold signature from real, separately-running mpc-party
// processes (not a plain key standing in for one, see
// balance_migration_test.go for that decomposed version), assembles the
// final signed transaction, and confirms it recovers to the DKG-derived
// threshold address -- proving the exact composition
// BalanceMigrationWorkflow drives in production, without needing a real
// funded chain to prove the cryptography is correct. Same processes and
// build-tag convention as real_tss_live_test.go:
//
//	cd services/mpc-party && go build -o /tmp/mpc-party .
//	PARTY_ID=1 PORT=7101 /tmp/mpc-party &
//	PARTY_ID=2 PORT=7102 /tmp/mpc-party &
//	PARTY_ID=3 PORT=7103 /tmp/mpc-party &
//	go test -tags live -run TestLiveBalanceMigrationSweep -v ./activities/...
//
// Does NOT exercise BuildSweepTransaction or an actual chain broadcast --
// both need a real funded address on a real RPC endpoint, which doesn't
// exist in this environment (see balance_migration.go's doc comment on
// AssembleSweepTransaction for the same disclosure). This test instead
// supplies fixed, arbitrary nonce/gas/value fields directly, the same way
// BuildSweepTransaction would have produced them from a real chain query.

import (
	"encoding/hex"
	"math/big"
	"testing"

	"go.temporal.io/sdk/testsuite"

	"forge-crypto/temporal-worker/workflows"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestLiveBalanceMigrationSweep(t *testing.T) {
	a := NewActivities("", "", "", 3, nil)

	s := &testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.SetTestTimeout(0)
	env.RegisterActivity(a)

	dkgReq := workflows.DKGCeremonyRequest{
		CustomerID:     "live-balance-migration-test",
		CeremonyID:     "live-balance-migration-ceremony",
		ChainID:        "ethereum",
		N:              3,
		K:              1, // 2-of-3
		PartyIDs:       []int{1, 2, 3},
		PartyEndpoints: []string{"http://localhost:7101", "http://localhost:7102", "http://localhost:7103"},
	}

	dkgVal, err := env.ExecuteActivity(a.ExecuteRealDKG, dkgReq)
	if err != nil {
		t.Fatalf("ExecuteRealDKG returned an error: %v", err)
	}
	var dkgResult workflows.DKGCeremonyResult
	if err := dkgVal.Get(&dkgResult); err != nil {
		t.Fatalf("failed to decode ExecuteRealDKG result: %v", err)
	}
	if dkgResult.Status != "completed" {
		t.Fatalf("DKG did not complete: %s", dkgResult.Error)
	}
	oldAddress := dkgResult.ThresholdAddress
	t.Logf("real DKG derived the 'old' threshold address to sweep from: %s", oldAddress)

	newAddress := common.HexToAddress("0x6666666666666666666666666666666666666666")
	const (
		nonce      = uint64(0)
		gasLimit   = uint64(21000)
		evmChainID = int64(11155111) // Sepolia
	)
	gasPrice := big.NewInt(20_000_000_000)
	value := big.NewInt(1_000_000_000_000_000) // 0.001 ETH, arbitrary for this test

	tx, signer := newLegacySweepTx(nonce, gasPrice, value, gasLimit, newAddress, evmChainID)
	hash := signer.Hash(tx)

	signReq := workflows.ThresholdSigningRequest{
		CeremonyID:     dkgReq.CeremonyID,
		Message:        hex.EncodeToString(hash.Bytes()),
		PartyIDs:       []int{1, 2},
		PartyEndpoints: []string{"http://localhost:7101", "http://localhost:7102"},
		ChainID:        "ethereum",
	}

	signVal, err := env.ExecuteActivity(a.ExecuteRealSigning, signReq)
	if err != nil {
		t.Fatalf("ExecuteRealSigning returned an error: %v", err)
	}
	var signResult workflows.ThresholdSigningResult
	if err := signVal.Get(&signResult); err != nil {
		t.Fatalf("failed to decode ExecuteRealSigning result: %v", err)
	}
	if signResult.Status != "completed" {
		t.Fatalf("signing did not complete: %s", signResult.Error)
	}

	assembleVal, err := env.ExecuteActivity(a.AssembleSweepTransaction, workflows.AssembleSweepTransactionRequest{
		NewAddress:   newAddress.Hex(),
		Nonce:        nonce,
		GasLimit:     gasLimit,
		GasPriceWei:  gasPrice.String(),
		ValueWei:     value.String(),
		EVMChainID:   evmChainID,
		Signature:    signResult.Signature,
		ExpectedFrom: oldAddress,
	})
	if err != nil {
		t.Fatalf("AssembleSweepTransaction returned an error: %v", err)
	}
	var assembled workflows.AssembleSweepTransactionResult
	if err := assembleVal.Get(&assembled); err != nil {
		t.Fatalf("failed to decode AssembleSweepTransaction result: %v", err)
	}
	if assembled.SignedTxHex == "" {
		t.Fatal("expected a non-empty signed tx")
	}

	// Independent verification, not just trusting the activity's own
	// internal check: recover the sender directly with crypto.SigToPub,
	// exactly as real_tss_live_test.go does for the raw signature.
	sigBytes, err := hex.DecodeString(signResult.Signature)
	if err != nil {
		t.Fatalf("failed to decode signature: %v", err)
	}
	recoveredPub, err := crypto.SigToPub(hash.Bytes(), sigBytes)
	if err != nil {
		t.Fatalf("failed to recover public key from signature: %v", err)
	}
	recoveredAddress := crypto.PubkeyToAddress(*recoveredPub).Hex()
	if recoveredAddress != oldAddress {
		t.Fatalf("sweep tx signature recovers to %s, but DKG derived %s -- INVALID", recoveredAddress, oldAddress)
	}

	t.Logf("SUCCESS: real DKG -> real threshold signature -> assembled+RLP-encoded Ethereum sweep transaction, recovering to %s: %s", recoveredAddress, assembled.SignedTxHex)
}
