package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bnb-chain/tss-lib/v2/common"
	tsscommon "github.com/bnb-chain/tss-lib/v2/tss"
)

// A broadcast message with nothing in it: enough to travel the relay path.
type stubMessage struct{}

func (stubMessage) Type() string                  { return "stub" }
func (stubMessage) GetTo() []*tsscommon.PartyID   { return nil }
func (stubMessage) GetFrom() *tsscommon.PartyID   { return nil }
func (stubMessage) IsBroadcast() bool             { return true }
func (stubMessage) IsToOldCommittee() bool        { return false }
func (stubMessage) IsToOldAndNewCommittees() bool { return false }
func (stubMessage) WireBytes() ([]byte, *tsscommon.MessageRouting, error) {
	return []byte("x"), nil, nil
}
func (stubMessage) WireMsg() *tsscommon.MessageWrapper { return nil }
func (stubMessage) String() string                     { return "stub" }

// The bug this pins: a party whose last round emits a message and then
// finishes has both the message and its result ready, and a driver that
// selected between them dropped the message half the time. The party
// reported success and its partner waited for ever. Offering both on every
// iteration makes a random choice show itself immediately -- the old driver
// delivers about half of them -- where the real failure needed a loaded CI
// runner and a lucky ordering.
func TestASigningPartyDeliversItsLastMessageBeforeItFinishes(t *testing.T) {
	var delivered int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&delivered, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()

	m := &TSSPartyManager{client: peer.Client(), signings: map[string]*tssSigningCeremony{}}

	const rounds = 200
	for i := 0; i < rounds; i++ {
		id := fmt.Sprintf("sign-%d", i)
		ceremony := &tssSigningCeremony{
			status:      ceremonyInProgress,
			selfPartyID: 1,
			peers:       map[int]string{1: "unused", 2: peer.URL},
			curve:       CurveEd25519,
		}
		m.signings[id] = ceremony

		outCh := make(chan tsscommon.Message, 4)
		endCh := make(chan common.SignatureData, 1)
		errCh := make(chan *tsscommon.Error, 1)
		outCh <- stubMessage{}
		endCh <- common.SignatureData{Signature: []byte{1}}

		m.driveSigning(id, ceremony, outCh, endCh, errCh)

		if ceremony.status != ceremonyCompleted {
			t.Fatalf("round %d: status %s (%s), want completed", i, ceremony.status, ceremony.errorMessage)
		}
	}
	if got := atomic.LoadInt32(&delivered); got != rounds {
		t.Fatalf("the peer received %d of %d final messages; the rest were dropped when the party finished", got, rounds)
	}
}

func TestRelayPendingSendsEverythingQueuedAndStops(t *testing.T) {
	outCh := make(chan tsscommon.Message, 8)
	for i := 0; i < 5; i++ {
		outCh <- stubMessage{}
	}
	var sent int
	if err := relayPending(outCh, func(tsscommon.Message) error { sent++; return nil }); err != nil {
		t.Fatal(err)
	}
	if sent != 5 || len(outCh) != 0 {
		t.Fatalf("sent %d, %d left; want 5 sent and none left", sent, len(outCh))
	}

	// Returns instead of blocking when nothing is queued.
	if err := relayPending(outCh, func(tsscommon.Message) error { t.Fatal("nothing was queued"); return nil }); err != nil {
		t.Fatal(err)
	}

	// A failed send is reported, and stops the flush.
	outCh <- stubMessage{}
	outCh <- stubMessage{}
	calls := 0
	err := relayPending(outCh, func(tsscommon.Message) error { calls++; return fmt.Errorf("peer down") })
	if err == nil || calls != 1 {
		t.Fatalf("err=%v after %d calls; want the error after the first", err, calls)
	}
}
