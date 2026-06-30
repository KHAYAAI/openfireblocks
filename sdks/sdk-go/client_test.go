package openfireblocks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSign(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sign" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k1" {
			t.Errorf("auth header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(SignResult{
			RequestID: "r1", SignedTx: "0xabc", TxHash: "0xhash",
			From: "0xfrom", Status: "signed",
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "k1")
	res, err := c.Sign(context.Background(), SignRequest{
		ChainID: 11155111, To: "0xabc", GasLimit: 21000, GasPrice: "1", Nonce: 0,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if res.RequestID != "r1" || res.Status != "signed" {
		t.Errorf("unexpected result %+v", res)
	}
}

func TestSign_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"policy denied"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "k1")
	_, err := c.Sign(context.Background(), SignRequest{ChainID: 1, To: "0x", GasLimit: 21000, GasPrice: "1"})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d", apiErr.StatusCode)
	}
}

func TestGetTransactionEscapesID(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "k1")
	if _, err := c.GetTransaction(context.Background(), "a b"); err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if seen != "/transactions/a%20b" {
		t.Errorf("escaped path = %q", seen)
	}
}
