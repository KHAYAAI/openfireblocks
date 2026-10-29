package workflows_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/encoding/protojson"

	"forge-crypto/temporal-worker/workflows"
)

// Replay tests: the guard for Temporal SDK upgrades and workflow edits.
//
// A workflow that is running when the worker is upgraded is replayed by the
// new worker from its recorded history, and must make exactly the same
// decisions it made before; if it does not, Temporal fails the workflow with
// a non-determinism error. That is how a dependency bump can break a
// signing in flight, and nothing in the other workflow tests would notice:
// they run each workflow once, on one SDK.
//
// testdata/histories holds real histories, recorded against a Temporal dev
// server by TestRecordWorkflowHistories (one per path through each
// workflow, approvals included). TestRecordedHistoriesReplay replays every
// one of them on the SDK this module builds with, on every run, with no
// server. Re-record only on purpose -- after a workflow change that is
// meant to change its decisions, behind workflow.GetVersion -- and keep the
// old files, since workflows started on the old code may still be running.

const historiesDir = "testdata/histories"

func registerWorkflows(r worker.WorkflowRegistry) {
	r.RegisterWorkflow(workflows.TransactionSettlementWorkflow)
	r.RegisterWorkflow(workflows.DKGCeremonyWorkflow)
	r.RegisterWorkflow(workflows.ThresholdSigningWorkflow)
	r.RegisterWorkflow(workflows.KeyRotationWorkflow)
	r.RegisterWorkflow(workflows.BalanceMigrationWorkflow)
	r.RegisterWorkflow(workflows.ProvisionKeyWorkflow)
}

func TestRecordedHistoriesReplay(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(historiesDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no recorded histories in %s; record them with TestRecordWorkflowHistories", historiesDir)
	}
	sort.Strings(files)
	for _, f := range files {
		f := f
		t.Run(strings.TrimSuffix(filepath.Base(f), ".json"), func(t *testing.T) {
			replayer := worker.NewWorkflowReplayer()
			registerWorkflows(replayer)
			if err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, f); err != nil {
				t.Fatalf("history no longer replays on this SDK and code: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Recording
// ---------------------------------------------------------------------------

// Stand-ins for the activities, by the names the workflows call them with.
// The recorded histories hold the activities' results, so what matters is
// that they take each workflow down a real path, not what they compute.
func registerFakeActivities(w worker.Worker) {
	reg := func(name string, fn interface{}) {
		w.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
	reg("CheckPolicy", func(_ context.Context, req workflows.TransactionRequest) (*workflows.PolicyCheckResult, error) {
		switch req.CustomerID {
		case "denied":
			return &workflows.PolicyCheckResult{Approved: false, Denials: []string{"amount over limit"}, Reason: "amount over limit"}, nil
		case "needs-approval", "needs-approval-rejected":
			return &workflows.PolicyCheckResult{Approved: true, RequiresApproval: true, ApprovalReasons: []string{"amount over approval threshold"}}, nil
		}
		return &workflows.PolicyCheckResult{Approved: true}, nil
	})
	reg("OpenApprovalRequest", func(_ context.Context, in workflows.OpenApprovalRequestInput) (*workflows.OpenApprovalRequestResult, error) {
		return &workflows.OpenApprovalRequestResult{ApprovalID: "appr-" + in.WorkflowID, RequiredApprovals: 2, ExpiresInSeconds: 3600}, nil
	})
	reg("GetRecordedApprovalStatus", func(_ context.Context, _ string) (*workflows.RecordedApprovalStatus, error) {
		return &workflows.RecordedApprovalStatus{Status: "approved", DistinctApprovals: 2, RequiredApprovals: 2}, nil
	})
	reg("CloseApprovalRequest", func(_ context.Context, _ workflows.CloseApprovalRequestInput) error { return nil })
	reg("SignTransaction", func(_ context.Context, _ workflows.TransactionRequest) (*workflows.SignResult, error) {
		return &workflows.SignResult{RequestID: "sign-1", SignedTx: "0xsigned", TxHash: "0xtxhash", From: "0xfrom"}, nil
	})
	reg("BroadcastTransaction", func(_ context.Context, signed string) (*workflows.BroadcastResult, error) {
		return &workflows.BroadcastResult{TxHash: "0xbroadcast-" + signed}, nil
	})
	reg("MonitorTransaction", func(_ context.Context, _ string) (*workflows.MonitorResult, error) {
		return &workflows.MonitorResult{BlockNumber: 100, Confirmations: 3, Success: true}, nil
	})
	reg("ExecuteRealDKG", func(_ context.Context, req workflows.DKGCeremonyRequest) (*workflows.DKGCeremonyResult, error) {
		if req.CeremonyID == "dkg-fails" {
			return nil, errors.New("party 2 unreachable")
		}
		return &workflows.DKGCeremonyResult{CeremonyID: req.CeremonyID, Status: "completed", ThresholdAddress: "0xaddress", ThresholdPubKey: "04pub"}, nil
	})
	reg("ExecuteRealSigning", func(_ context.Context, req workflows.ThresholdSigningRequest) (*workflows.ThresholdSigningResult, error) {
		return &workflows.ThresholdSigningResult{Signature: "0xsig", Status: "completed"}, nil
	})
	reg("ActivateKeyPair", func(_ context.Context, _ workflows.ActivateKeyPairRequest) error { return nil })
	reg("SetKeyPairStatus", func(_ context.Context, _ workflows.SetKeyPairStatusRequest) error { return nil })
	reg("SetCeremonyStatus", func(_ context.Context, _ workflows.SetCeremonyStatusRequest) error { return nil })
	reg("DeactivateOldKeyShares", func(_ context.Context, req workflows.DeactivateSharesRequest) (*workflows.DeactivateSharesResult, error) {
		return &workflows.DeactivateSharesResult{DeactivatedCount: len(req.PartyIDs)}, nil
	})
	reg("BuildSweepTransaction", func(_ context.Context, req workflows.BuildSweepTransactionRequest) (*workflows.BuildSweepTransactionResult, error) {
		if req.OldAddress == "0xempty" {
			return &workflows.BuildSweepTransactionResult{Skipped: true, BalanceWei: "0"}, nil
		}
		return &workflows.BuildSweepTransactionResult{BalanceWei: "1000000", Nonce: 7, GasLimit: 21000, GasPriceWei: "10", ValueWei: "790000", TxHashHex: "0xsweep"}, nil
	})
	reg("AssembleSweepTransaction", func(_ context.Context, _ workflows.AssembleSweepTransactionRequest) (*workflows.AssembleSweepTransactionResult, error) {
		return &workflows.AssembleSweepTransactionResult{SignedTxHex: "0xsweepsigned", TxHash: "0xsweephash"}, nil
	})
}

type scenario struct {
	name     string
	workflow interface{}
	arg      interface{}
	// drive runs after the workflow starts, for workflows that wait on people.
	drive func(ctx context.Context, c client.Client, workflowID string) error
}

func scenarios() []scenario {
	tx := func(customer, initiator string) workflows.TransactionRequest {
		return workflows.TransactionRequest{
			CustomerID: customer, CustomerTier: "pro", ChainID: 11155111,
			To: "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045", Value: "1000",
			GasLimit: 21000, GasPrice: "20000000000", InitiatedByUserID: initiator,
		}
	}
	decide := func(decisions ...workflows.ApprovalDecisionSignal) func(context.Context, client.Client, string) error {
		return func(ctx context.Context, c client.Client, id string) error {
			// Wait until the workflow is waiting for decisions.
			for i := 0; ; i++ {
				if _, err := c.QueryWorkflow(ctx, id, "", workflows.ApprovalStateQueryName); err == nil {
					break
				} else if i > 100 {
					return fmt.Errorf("approval step never opened: %w", err)
				}
				time.Sleep(100 * time.Millisecond)
			}
			for _, d := range decisions {
				if err := c.SignalWorkflow(ctx, id, "", workflows.ApprovalDecisionSignalName, d); err != nil {
					return err
				}
			}
			return nil
		}
	}
	ceremony := func(id string) workflows.DKGCeremonyRequest {
		return workflows.DKGCeremonyRequest{CustomerID: "c1", CeremonyID: id, PartyIDs: []int{1, 2, 3},
			PartyEndpoints: []string{"https://party-1:7000", "https://party-2:7000", "https://party-3:7000"}}
	}
	return []scenario{
		{name: "settlement-confirmed", workflow: workflows.TransactionSettlementWorkflow, arg: tx("ok", "u1")},
		{name: "settlement-denied-by-policy", workflow: workflows.TransactionSettlementWorkflow, arg: tx("denied", "u1")},
		{name: "settlement-approved-two-of-n", workflow: workflows.TransactionSettlementWorkflow, arg: tx("needs-approval", "u1"),
			drive: decide(
				workflows.ApprovalDecisionSignal{ApproverUserID: "u1", Decision: "approve"}, // the initiator: ignored
				workflows.ApprovalDecisionSignal{ApproverUserID: "u2", Decision: "approve"},
				workflows.ApprovalDecisionSignal{ApproverUserID: "u2", Decision: "approve"}, // counted once
				workflows.ApprovalDecisionSignal{ApproverUserID: "u3", Decision: "approve"},
			)},
		{name: "settlement-rejected", workflow: workflows.TransactionSettlementWorkflow, arg: tx("needs-approval-rejected", "u1"),
			drive: decide(workflows.ApprovalDecisionSignal{ApproverUserID: "u2", Decision: "reject"})},
		{name: "dkg-completed", workflow: workflows.DKGCeremonyWorkflow, arg: ceremony("dkg-1")},
		{name: "dkg-failed", workflow: workflows.DKGCeremonyWorkflow, arg: ceremony("dkg-fails")},
		{name: "threshold-signing", workflow: workflows.ThresholdSigningWorkflow,
			arg: workflows.ThresholdSigningRequest{CeremonyID: "dkg-1", Message: "0xabc", PartyIDs: []int{1, 2}, ChainID: "11155111"}},
		{name: "provision-key", workflow: workflows.ProvisionKeyWorkflow,
			arg: workflows.ProvisionKeyRequest{Ceremony: ceremony("prov-1"), KeyID: "key-1"}},
		{name: "provision-key-dkg-failed", workflow: workflows.ProvisionKeyWorkflow,
			arg: workflows.ProvisionKeyRequest{Ceremony: ceremony("dkg-fails"), KeyID: "key-2"}},
		{name: "key-rotation-with-share-retirement", workflow: workflows.KeyRotationWorkflow,
			arg: workflows.KeyRotationRequest{NewCeremony: ceremony("rot-new"), OldKeyID: "key-old", NewKeyID: "key-new",
				OldCeremonyID: "rot-old", OldPartyIDs: []int{1, 2, 3}, ShareRetention: 2 * time.Second}},
		{name: "balance-migration", workflow: workflows.BalanceMigrationWorkflow,
			arg: workflows.BalanceMigrationRequest{OldCeremonyID: "rot-old", OldAddress: "0xold", NewAddress: "0xnew", PartyIDs: []int{1, 2}}},
		{name: "balance-migration-nothing-to-sweep", workflow: workflows.BalanceMigrationWorkflow,
			arg: workflows.BalanceMigrationRequest{OldCeremonyID: "rot-old", OldAddress: "0xempty", NewAddress: "0xnew"}},
	}
}

// Records testdata/histories against a Temporal server, when
// TEMPORAL_RECORD_ADDR names one (for example "127.0.0.1:7233", from
// `temporal server start-dev`). Overwrites existing files of the same name:
// see the note at the top before doing that.
func TestRecordWorkflowHistories(t *testing.T) {
	addr := os.Getenv("TEMPORAL_RECORD_ADDR")
	if addr == "" {
		t.Skip("TEMPORAL_RECORD_ADDR not set; recording needs a Temporal server")
	}
	c, err := client.Dial(client.Options{HostPort: addr})
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer c.Close()

	queue := fmt.Sprintf("replay-record-%d", time.Now().UnixNano())
	w := worker.New(c, queue, worker.Options{})
	registerWorkflows(w)
	registerFakeActivities(w)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	if err := os.MkdirAll(historiesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, s := range scenarios() {
		id := fmt.Sprintf("%s-%d", s.name, time.Now().UnixNano())
		run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: queue}, s.workflow, s.arg)
		if err != nil {
			t.Fatalf("%s: start: %v", s.name, err)
		}
		if s.drive != nil {
			if err := s.drive(ctx, c, id); err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
		}
		var out json.RawMessage
		if err := run.Get(ctx, &out); err != nil {
			t.Fatalf("%s: workflow failed: %v", s.name, err)
		}
		t.Logf("%s: %s", s.name, out)

		hist := &historypb.History{}
		it := c.GetWorkflowHistory(ctx, id, run.GetRunID(), false, 0)
		for it.HasNext() {
			ev, err := it.Next()
			if err != nil {
				t.Fatalf("%s: history: %v", s.name, err)
			}
			hist.Events = append(hist.Events, ev)
		}
		data, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(hist)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(historiesDir, s.name+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
