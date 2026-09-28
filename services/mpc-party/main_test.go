package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

// buildRouter mirrors the routes main() registers, so a test can assert on
// the served surface rather than on a list of handler names that would go
// stale the moment someone adds a route.
func buildRouter(ps *PartyServer) *mux.Router {
	router := mux.NewRouter()
	router.HandleFunc("/tss/keygen/start", ps.HandleTSSKeygenStart).Methods(http.MethodPost)
	router.HandleFunc("/tss/keygen/message", ps.HandleTSSKeygenMessage).Methods(http.MethodPost)
	router.HandleFunc("/tss/keygen/status", ps.HandleTSSKeygenStatus).Methods(http.MethodGet)
	router.HandleFunc("/tss/sign/start", ps.HandleTSSSignStart).Methods(http.MethodPost)
	router.HandleFunc("/tss/sign/message", ps.HandleTSSSignMessage).Methods(http.MethodPost)
	router.HandleFunc("/tss/sign/status", ps.HandleTSSSignStatus).Methods(http.MethodGet)
	router.HandleFunc("/health", ps.HandleHealth).Methods(http.MethodGet)
	router.HandleFunc("/info", ps.HandleInfo).Methods(http.MethodGet)
	return router
}

// The party used to serve a second set of ceremony endpoints -- /round,
// /round/{n}/data, /round/{n}/broadcast and /sign -- backed by TSSWrapper,
// which was documented in its own source as "not cryptographically secure"
// and computed nothing of the sort. No workflow drove them, but they were
// served by the same binary on the same port, so anything that could reach a
// party could ask it for a "signature" and get back something
// signature-shaped and meaningless.
//
// Unused is not the same as unreachable. This pins them gone.
func TestPlaceholderCeremonyEndpointsAreNotServed(t *testing.T) {
	router := buildRouter(NewPartyServer(1))

	for _, route := range []struct {
		method, path string
	}{
		{http.MethodPost, "/round"},
		{http.MethodGet, "/round/1/data"},
		{http.MethodPost, "/round/1/broadcast"},
		{http.MethodPost, "/sign"},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s returned %d; the non-cryptographic placeholder path must not be served",
				route.method, route.path, w.Code)
		}
	}
}

// The real ceremony endpoints must still be routed -- a test that only
// checks things are gone would also pass if someone deleted everything.
func TestRealCeremonyEndpointsAreServed(t *testing.T) {
	router := buildRouter(NewPartyServer(1))

	for _, path := range []string{
		"/tss/keygen/start", "/tss/keygen/message", "/tss/sign/start", "/tss/sign/message",
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code == http.StatusNotFound {
			t.Errorf("POST %s is not routed", path)
		}
	}
}

// Health is liveness, not ceremony state.
//
// It used to report "unhealthy" when the last DKG had failed, which would
// have kubelet restart a process that is working perfectly well and is able
// to take part in the next ceremony. A failed ceremony is a ceremony
// problem, not a process problem.
func TestHandleHealthIsLivenessNotCeremonyState(t *testing.T) {
	ps := NewPartyServer(2)
	w := httptest.NewRecorder()
	ps.HandleHealth(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("health returned %d, want 200", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("health response is not JSON: %v", err)
	}
	if body["status"] != "healthy" {
		t.Errorf("status = %q, want healthy", body["status"])
	}
	if body["partyId"] != "2" {
		t.Errorf("partyId = %q, want 2", body["partyId"])
	}
}

func TestHandleInfoReportsCeremonyCounts(t *testing.T) {
	ps := NewPartyServer(3)
	ps.tssManager = NewTSSPartyManager(3, &http.Client{})

	w := httptest.NewRecorder()
	ps.HandleInfo(w, httptest.NewRequest(http.MethodGet, "/info", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("info returned %d, want 200", w.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("info response is not JSON: %v", err)
	}
	if body["partyId"] != float64(3) {
		t.Errorf("partyId = %v, want 3", body["partyId"])
	}
	// Counts, not ceremony ids: /info is operator visibility, and ceremony
	// ids are the addressing scheme for sealed key shares.
	if _, ok := body["keygenCeremonies"]; !ok {
		t.Error("info does not report keygenCeremonies")
	}
	if _, ok := body["signingCeremonies"]; !ok {
		t.Error("info does not report signingCeremonies")
	}
	for _, leaky := range []string{"ceremonyId", "keyShare", "shareId"} {
		if _, present := body[leaky]; present {
			t.Errorf("info exposes %q", leaky)
		}
	}
}
