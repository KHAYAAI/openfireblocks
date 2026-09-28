package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A protocol message that arrives before its ceremony has been started
// must be answered with "try again", not "no such thing".
//
// This is a real race in normal operation, not a theoretical one. The
// orchestrator posts /tss/keygen/start and /tss/sign/start to each party
// in turn, and the party it reaches first can relay its round-1 message
// before the party it reaches last has been told the ceremony exists.
// From the sender's side those two states are indistinguishable, and only
// one of them was retried: the other came back 500, the sender gave up on
// that message, the round never completed, and the ceremony hung until it
// timed out.
//
// It was found by infrastructure/local/recovery-drill-local.sh, which
// failed roughly one run in three, always at the same step -- signing with
// a freshly restored committee, where every party is newly started and the
// window is at its widest.
//
// 503 rather than 500 is the whole fix: postTSSEnvelope retries a 503 and
// abandons anything else.
func TestAMessageForANotYetStartedCeremonyAsksTheSenderToRetry(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")

	ps := &PartyServer{partyID: 2, tssManager: NewTSSPartyManager(2, nil)}

	for _, tc := range []struct {
		name string
		path string
		body string
		call func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "keygen",
			path: "/tss/keygen/message",
			body: `{"ceremony_id":"never-started","from_party_id":1,"wire_bytes":"AA==","is_broadcast":true}`,
			call: ps.HandleTSSKeygenMessage,
		},
		{
			name: "signing",
			path: "/tss/sign/message",
			body: `{"sign_id":"never-started","from_party_id":1,"wire_bytes":"AA==","is_broadcast":true}`,
			call: ps.HandleTSSSignMessage,
		},
		{
			// A hung refresh is the worst of the three: the key is live
			// and holding money, and an operator who sees one time out
			// has to work out which parties moved epoch before touching
			// anything.
			name: "refresh",
			path: "/tss/reshare/message",
			body: `{"reshare_id":"never-started","from_party_id":1,"wire_bytes":"AA==","to_old_committee":false,"from_old_committee":true}`,
			call: ps.HandleTSSReshareMessage,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			tc.call(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("a message for an unstarted %s ceremony returned HTTP %d, want 503.\n"+
					"Anything else makes the sender give up, and the ceremony hangs.\nbody: %s",
					tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}
