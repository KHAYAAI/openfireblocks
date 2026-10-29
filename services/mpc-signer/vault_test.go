package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeKV serves the two KV v2 calls the signer makes, GET and PUT of
// <mount>/data/<path>, the way Vault answers them: 404 with an empty errors
// list for a secret that does not exist, the data under data.data otherwise.
type fakeKV struct {
	mu      sync.Mutex
	secrets map[string]map[string]interface{}
	tokens  []string
}

func (f *fakeKV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, r.Header.Get("X-Vault-Token"))
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		data, ok := f.secrets[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{"data": data, "metadata": map[string]interface{}{"version": 1}},
		})
	case http.MethodPut, http.MethodPost:
		var body struct {
			Data map[string]interface{} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.secrets[path] = body.Data
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"version": 1}})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// The signer tells "no key yet" from "Vault is broken" by the client's error
// text. A Vault client upgrade that changed that text would turn every first
// start into a startup failure, or worse, every restart into a new key; this
// pins both halves: a missing key is generated and stored, and a stored key is
// read back rather than replaced.
func TestTheSigningKeyIsCreatedOnceAndThenReadFromVault(t *testing.T) {
	kv := &fakeKV{secrets: map[string]map[string]interface{}{}}
	srv := httptest.NewServer(kv)
	defer srv.Close()

	env := map[string]string{"VAULT_ADDR": srv.URL, "VAULT_TOKEN": "test-token"}
	getenv := func(k string) string { return env[k] }

	first, err := ResolveSigningKey(context.Background(), getenv)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("first start returned a %d-character key, want 64 hex characters", len(first))
	}
	stored, ok := kv.secrets["secret/data/openfireblocks/mpc-signer"]
	if !ok || stored["private_key"] != first || stored["address"] == "" {
		t.Fatalf("the generated key was not stored at the default path: %v", kv.secrets)
	}

	second, err := ResolveSigningKey(context.Background(), getenv)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if second != first {
		t.Fatal("a restart replaced the stored signing key instead of reading it")
	}
	for _, tok := range kv.tokens {
		if tok != "test-token" {
			t.Fatalf("a request went to Vault with token %q", tok)
		}
	}
}

// Anything other than "not found" must stop the signer starting, never make it
// generate a fresh key over one it simply could not read.
func TestAVaultErrorStopsStartupInsteadOfMakingANewKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("the signer tried to %s after a failed read", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
	}))
	defer srv.Close()

	getenv := func(k string) string { return map[string]string{"VAULT_ADDR": srv.URL}[k] }
	if key, err := ResolveSigningKey(context.Background(), getenv); err == nil {
		t.Fatalf("a permission error was treated as a missing key; got key of length %d", len(key))
	}
}
