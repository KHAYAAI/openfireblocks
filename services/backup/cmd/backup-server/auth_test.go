package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(token string, allow bool, header string, path string) int {
	h := requireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }), token, allow)
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestTheRightTokenIsAdmitted(t *testing.T) {
	if got := serve("s3cret", false, "Bearer s3cret", "/restore"); got != http.StatusOK {
		t.Fatalf("got %d", got)
	}
}

func TestAWrongOrMissingTokenIsRefused(t *testing.T) {
	for _, h := range []string{"", "Bearer wrong", "s3cret", "Bearer s3cre", "Bearer s3cret2"} {
		if got := serve("s3cret", false, h, "/dr/failover"); got != http.StatusUnauthorized {
			t.Errorf("header %q: got %d, want 401", h, got)
		}
	}
}

func TestWithNoTokenConfiguredTheServiceRefusesToAct(t *testing.T) {
	if got := serve("", false, "", "/restore"); got != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503: an unconfigured service must not serve restore", got)
	}
	if got := serve("", false, "Bearer ", "/restore"); got != http.StatusServiceUnavailable {
		t.Fatalf("an empty bearer must not pass when no token is configured, got %d", got)
	}
}

func TestDevelopmentMayOptOutExplicitly(t *testing.T) {
	if got := serve("", true, "", "/backup/full"); got != http.StatusOK {
		t.Fatalf("got %d", got)
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	if got := serve("s3cret", false, "", "/health"); got != http.StatusOK {
		t.Fatalf("got %d", got)
	}
}
