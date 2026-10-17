package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Putting a signed transaction onto a public blockchain.
//
// The least reversible thing this platform does, and it had no tests --
// reaching Settle() needed a live Postgres, so nobody ever did. What
// matters here is not the happy path but the failures: a broadcast that is
// refused must be recorded as refused, and a chain nobody configured must
// be refused before anything is sent rather than after.

type fakeStore struct {
	created    []*Settlement
	updated    []*Settlement
	failCreate error
}

func (f *fakeStore) CreateSettlement(_ context.Context, s *Settlement) error {
	if f.failCreate != nil {
		return f.failCreate
	}
	// Copied, not aliased: the service mutates the settlement after
	// storing it, and a test asserting on the stored state has to see what
	// was stored rather than what it later became.
	stored := *s
	f.created = append(f.created, &stored)
	return nil
}

func (f *fakeStore) UpdateSettlement(_ context.Context, s *Settlement) error {
	stored := *s
	f.updated = append(f.updated, &stored)
	return nil
}

func (f *fakeStore) GetSettlement(_ context.Context, id string) (*Settlement, error) {
	for _, s := range f.created {
		if s.SettlementID == id {
			return s, nil
		}
	}
	return nil, errors.New("not found")
}

func (f *fakeStore) ListSettlements(_ context.Context, _ string) ([]*Settlement, error) {
	return f.created, nil
}

type fakeChain struct {
	broadcastTo  string
	broadcastErr error
	gas          uint64
	gasErr       error
	broadcasts   [][]byte
}

func (c *fakeChain) BroadcastTransaction(_ context.Context, tx []byte) (string, error) {
	c.broadcasts = append(c.broadcasts, tx)
	if c.broadcastErr != nil {
		return "", c.broadcastErr
	}
	return c.broadcastTo, nil
}

func (c *fakeChain) GetTransactionStatus(context.Context, string) (string, error) {
	return "pending", nil
}

func (c *fakeChain) EstimateGas(context.Context, []byte) (uint64, error) {
	return c.gas, c.gasErr
}

func build(t *testing.T, chain *fakeChain) (*SettlementService, *fakeStore) {
	t.Helper()
	store := &fakeStore{}
	svc := NewSettlementService(store)
	if chain != nil {
		if err := svc.RegisterChain("ethereum", chain); err != nil {
			t.Fatalf("registering the chain: %v", err)
		}
	}
	return svc, store
}

func request() *SettlementRequest {
	return &SettlementRequest{
		SigningID:         "sign-1",
		Blockchain:        "ethereum",
		SignedTransaction: "0xdeadbeef",
	}
}

func TestABroadcastTransactionIsRecorded(t *testing.T) {
	chain := &fakeChain{broadcastTo: "0xhash", gas: 21_000}
	svc, store := build(t, chain)

	settlement, err := svc.Settle(context.Background(), request())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	if settlement.TransactionHash != "0xhash" {
		t.Errorf("the settlement records hash %q, not the one the chain returned",
			settlement.TransactionHash)
	}
	if len(store.created) != 1 {
		t.Fatalf("%d settlements were stored, want 1", len(store.created))
	}
	// Stored at all, and stored with the signing request it came from --
	// without that link a broadcast cannot be traced back to what
	// authorised it.
	if store.created[0].SigningID != "sign-1" {
		t.Errorf("the stored settlement names signing request %q", store.created[0].SigningID)
	}
}

// The failure that matters. A refused broadcast must leave a record saying
// so: a settlement that silently vanishes is a transaction nobody can
// explain, and the customer's money did not move.
func TestARefusedBroadcastIsRecordedAsFailed(t *testing.T) {
	chain := &fakeChain{broadcastErr: errors.New("insufficient funds for gas")}
	svc, store := build(t, chain)

	_, err := svc.Settle(context.Background(), request())

	if err == nil {
		t.Fatal("a refused broadcast was reported as a successful settlement")
	}
	if len(store.created) != 1 {
		t.Fatalf("a refused broadcast stored %d settlements, want 1", len(store.created))
	}
	stored := store.created[0]
	if stored.Status != "failed" {
		t.Errorf("the settlement was stored with status %q, want failed", stored.Status)
	}
	// The node's own reason, kept. "Settlement failed" is not something
	// anyone can act on; "insufficient funds for gas" is.
	if !strings.Contains(stored.ErrorMessage, "insufficient funds") {
		t.Errorf("the stored settlement does not carry the node's reason: %q", stored.ErrorMessage)
	}
}

// A chain nobody configured has to be refused before anything is sent.
func TestAnUnconfiguredChainIsRefusedWithoutBroadcasting(t *testing.T) {
	chain := &fakeChain{broadcastTo: "0xhash"}
	svc, store := build(t, chain)

	req := request()
	req.Blockchain = "dogecoin"

	_, err := svc.Settle(context.Background(), req)

	if err == nil {
		t.Fatal("a settlement was accepted for a chain with no client")
	}
	if len(chain.broadcasts) != 0 {
		t.Error("a transaction was broadcast to a chain client that was not the requested chain")
	}
	if len(store.created) != 0 {
		t.Error("a settlement was stored for a chain that cannot be settled on")
	}
}

// An incomplete request must not reach a chain. Each of these fields is
// required for the settlement to mean anything, and a broadcast of an empty
// transaction is a call to a node that can only fail.
func TestAnIncompleteRequestNeverReachesTheChain(t *testing.T) {
	cases := map[string]func(*SettlementRequest){
		"no signing id":  func(r *SettlementRequest) { r.SigningID = "" },
		"no blockchain":  func(r *SettlementRequest) { r.Blockchain = "" },
		"no transaction": func(r *SettlementRequest) { r.SignedTransaction = "" },
	}

	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			chain := &fakeChain{broadcastTo: "0xhash"}
			svc, store := build(t, chain)

			req := request()
			break_(req)

			if _, err := svc.Settle(context.Background(), req); err == nil {
				t.Error("an incomplete settlement request was accepted")
			}
			if len(chain.broadcasts) != 0 {
				t.Error("an incomplete request was broadcast anyway")
			}
			if len(store.created) != 0 {
				t.Error("an incomplete request was stored")
			}
		})
	}
}

// Gas estimation is advisory. A node that cannot estimate is not a reason
// to refuse to send a transaction that is already signed -- the fee is
// fixed in the signed bytes, and the estimate is only recorded.
func TestAFailedGasEstimateDoesNotStopTheBroadcast(t *testing.T) {
	chain := &fakeChain{broadcastTo: "0xhash", gasErr: errors.New("execution reverted")}
	svc, _ := build(t, chain)

	settlement, err := svc.Settle(context.Background(), request())

	if err != nil {
		t.Fatalf("a failed gas estimate blocked a signed transaction: %v", err)
	}
	if len(chain.broadcasts) != 1 {
		t.Error("the transaction was not broadcast")
	}
	if settlement.GasUsed != 0 {
		t.Errorf("gas was recorded as %d despite the estimate failing", settlement.GasUsed)
	}
}

// Two settlements must not share an id. They are the handle a customer and
// an operator use to find one transaction among many.
func TestEachSettlementGetsItsOwnIdentity(t *testing.T) {
	chain := &fakeChain{broadcastTo: "0xhash"}
	svc, _ := build(t, chain)

	first, err := svc.Settle(context.Background(), request())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	second, err := svc.Settle(context.Background(), request())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	if first.SettlementID == second.SettlementID {
		t.Error("two settlements were given the same id")
	}
}

// The reported average confirmation time is an average.
//
// It used to be `avg = (avg + sample) / 2`, which weights the newest sample
// at a half and everything older into nothing. This is the number an SLO
// would be written against, and a figure that is really "roughly the last
// couple of confirmations" cannot support a claim about the service --
// while looking exactly like a mean until somebody checks.
func TestConfirmationTimeIsAveragedNotBlended(t *testing.T) {
	metrics := &SettlementMetrics{}

	for _, seconds := range []float64{10, 20, 30} {
		metrics.recordConfirmationTime(seconds)
	}

	// The mean of 10, 20, 30 is 20. The old blend gave 22.5.
	if metrics.AvgConfirmationTime != 20 {
		t.Errorf("10s, 20s and 30s averaged to %.2fs, want 20s",
			metrics.AvgConfirmationTime)
	}
}

// The first sample is reported at its own value. Under the old arithmetic
// it was halved, because the average started at zero and was blended with
// it -- so the very first confirmation the platform ever measured was
// reported at half its true duration.
func TestTheFirstConfirmationIsNotHalved(t *testing.T) {
	metrics := &SettlementMetrics{}

	metrics.recordConfirmationTime(12)

	if metrics.AvgConfirmationTime != 12 {
		t.Errorf("a single 12s confirmation averaged to %.2fs", metrics.AvgConfirmationTime)
	}
}

// One outlier must not dominate. A single 10-minute confirmation among
// ninety-nine fast ones should move the average a little, not most of the
// way to the outlier.
func TestOneSlowConfirmationDoesNotDominate(t *testing.T) {
	metrics := &SettlementMetrics{}

	for i := 0; i < 99; i++ {
		metrics.recordConfirmationTime(10)
	}
	metrics.recordConfirmationTime(600)

	// (99*10 + 600) / 100 = 15.9
	if got := metrics.AvgConfirmationTime; got < 15.8 || got > 16.0 {
		t.Errorf("99 fast confirmations and one slow one averaged to %.2fs, want about 15.9s", got)
	}
}
