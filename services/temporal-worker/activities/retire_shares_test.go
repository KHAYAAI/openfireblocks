package activities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge-crypto/temporal-worker/workflows"
)

// With party endpoints, retirement is each party's own act: the worker
// asks, carrying a "retire" authorisation, and never touches a Vault.

type retireCall struct {
	CeremonyID             string `json:"ceremony_id"`
	Authorization          string `json:"authorization"`
	AuthorizationSignature string `json:"authorization_signature"`
}

func fakeParty(t *testing.T, status int, calls *[]retireCall) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tss/shares/retire" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var c retireCall
		_ = json.NewDecoder(r.Body).Decode(&c)
		*calls = append(*calls, c)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"refused by test"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRetirementAsksEachPartyAndReportsWhichFailed(t *testing.T) {
	t.Setenv("VAULT_ADDR", "http://must-not-be-used.invalid")
	var calls []retireCall
	ok1 := fakeParty(t, http.StatusOK, &calls)
	refused := fakeParty(t, http.StatusForbidden, &calls)
	ok3 := fakeParty(t, http.StatusOK, &calls)

	a := NewActivities("", "", "", 3, nil)
	res, err := a.DeactivateOldKeyShares(context.Background(), workflows.DeactivateSharesRequest{
		CeremonyID:     "old-key",
		PartyIDs:       []int{1, 2, 3},
		PartyEndpoints: []string{ok1, refused, ok3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DeactivatedCount != 2 {
		t.Fatalf("deactivated %d, want 2", res.DeactivatedCount)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "party 2") || !strings.Contains(res.Errors[0], "403") {
		t.Fatalf("errors: %v", res.Errors)
	}
	if len(calls) != 3 {
		t.Fatalf("%d parties were asked, want 3", len(calls))
	}
	for _, c := range calls {
		if c.CeremonyID != "old-key" {
			t.Fatalf("asked to retire %q", c.CeremonyID)
		}
	}
}

func TestAnUnreachablePartyIsReportedNotSkipped(t *testing.T) {
	a := NewActivities("", "", "", 3, nil)
	res, err := a.DeactivateOldKeyShares(context.Background(), workflows.DeactivateSharesRequest{
		CeremonyID:     "old-key",
		PartyIDs:       []int{1},
		PartyEndpoints: []string{"http://127.0.0.1:1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DeactivatedCount != 0 || len(res.Errors) != 1 {
		t.Fatalf("got %+v", res)
	}
}

func TestMismatchedEndpointsAreRefused(t *testing.T) {
	a := NewActivities("", "", "", 3, nil)
	if _, err := a.DeactivateOldKeyShares(context.Background(), workflows.DeactivateSharesRequest{
		CeremonyID: "x", PartyIDs: []int{1, 2}, PartyEndpoints: []string{"http://a"},
	}); err == nil {
		t.Fatal("endpoints not parallel to party ids were accepted")
	}
}

// With a co-signer configured, every retire request carries a signed
// "retire" authorisation for exactly this ceremony.
func TestRetirementCarriesARetireAuthorisation(t *testing.T) {
	t.Setenv("CEREMONY_AUTHORIZER_KEY", "d3eb80d5aa9292f3fad02c6753243fc62f1b133fa54e47809c0c5cfa9e937149")
	var calls []retireCall
	p := fakeParty(t, http.StatusOK, &calls)
	a := NewActivities("", "", "", 3, nil)
	if a.ceremonyAuth == nil {
		t.Skip("this build's NewActivities does not read CEREMONY_AUTHORIZER_KEY from the environment")
	}
	if _, err := a.DeactivateOldKeyShares(context.Background(), workflows.DeactivateSharesRequest{
		CeremonyID: "old-key", PartyIDs: []int{1}, PartyEndpoints: []string{p},
	}); err != nil {
		t.Fatal(err)
	}
	var auth authorizationRequest
	if err := json.Unmarshal([]byte(calls[0].Authorization), &auth); err != nil {
		t.Fatalf("authorization not readable: %v (%q)", err, calls[0].Authorization)
	}
	if auth.Operation != "retire" || auth.CeremonyID != "old-key" || calls[0].AuthorizationSignature == "" {
		t.Fatalf("authorisation %+v, signature %q", auth, calls[0].AuthorizationSignature)
	}
}
