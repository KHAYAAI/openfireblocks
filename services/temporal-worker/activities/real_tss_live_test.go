//go:build live

package activities

// Live integration test for ExecuteRealDKG/ExecuteRealSigning against
// real, separately-running mpc-party OS processes -- not httptest, not
// in the same test binary, but the actual production topology: this
// activity code, exactly as a real Temporal worker would run it, talking
// over real HTTP to real independent processes. Gated behind a build tag
// (matching services/mpc-signer/tss's //go:build tss convention) since it
// needs those processes already running:
//
//	cd services/mpc-party && go build -o /tmp/mpc-party .
//	PARTY_ID=1 PORT=7101 /tmp/mpc-party &
//	PARTY_ID=2 PORT=7102 /tmp/mpc-party &
//	PARTY_ID=3 PORT=7103 /tmp/mpc-party &
//	go test -tags live -run TestLiveRealDKGAndSigning -v ./activities/...

import (
	"encoding/hex"
	"testing"

	"go.temporal.io/sdk/testsuite"

	"forge-crypto/temporal-worker/workflows"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestLiveRealDKGAndSigning(t *testing.T) {
	a := NewActivities("", "", "", 3, nil)

	// activity.GetLogger (called inside ExecuteRealDKG/ExecuteRealSigning)
	// panics outside a real Temporal activity execution context, so this
	// runs through Temporal's own test activity environment -- which
	// provides that context but does NOT mock or intercept the HTTP calls
	// the activity makes, so this is still exercising real network calls
	// to the real mpc-party processes, just with Temporal's plumbing
	// underneath instead of a bare context.Background().
	s := &testsuite.WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.SetTestTimeout(0) // real DKG + signing take real time; don't let the harness impose its own short default
	env.RegisterActivity(a)

	dkgReq := workflows.DKGCeremonyRequest{
		CustomerID:     "live-test-customer",
		CeremonyID:     "live-test-ceremony",
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
	if dkgResult.ThresholdAddress == "" {
		t.Fatal("DKG completed with an empty ThresholdAddress")
	}
	t.Logf("real DKG (via temporal-worker's actual activity code, against 3 real separate processes) derived address %s", dkgResult.ThresholdAddress)

	messageHash := crypto.Keccak256([]byte("openfireblocks live orchestration test: temporal-worker -> real mpc-party processes"))

	signReq := workflows.ThresholdSigningRequest{
		CeremonyID:     dkgReq.CeremonyID,
		Message:        hex.EncodeToString(messageHash),
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

	sigBytes, err := hex.DecodeString(signResult.Signature)
	if err != nil {
		t.Fatalf("failed to decode signature: %v", err)
	}
	recoveredPub, err := crypto.SigToPub(messageHash, sigBytes)
	if err != nil {
		t.Fatalf("failed to recover public key from signature: %v", err)
	}
	recoveredAddress := crypto.PubkeyToAddress(*recoveredPub).Hex()

	if recoveredAddress != dkgResult.ThresholdAddress {
		t.Fatalf("signature recovers to %s, but DKG derived %s -- INVALID", recoveredAddress, dkgResult.ThresholdAddress)
	}

	t.Logf("SUCCESS: full real orchestration (temporal-worker activities -> real mpc-party processes -> real tss-lib) produced a valid signature recovering to %s", recoveredAddress)
}
