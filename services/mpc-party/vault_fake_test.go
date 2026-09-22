package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// An in-process stand-in for Vault's KV v2 engine, enough of it that the
// real hashicorp/vault/api client cannot tell the difference.
//
// Why this exists rather than `vault server -dev`: the recovery procedure
// in docs/deployment/KEY-RECOVERY.md is only a control if it is tested on
// every change, and a test that needs an operator to have installed a
// binary is a test that skips in CI and then skips forever. The three
// restore tests did exactly that until this existed.
//
// What it does and does not prove. It exercises this platform's seal,
// load and restore code against the real client library, over real HTTP,
// with the real request and response shapes -- so a mistake in how a
// share or its ceremony context is written or read back is caught here.
// It does not prove anything about Vault: not its storage, not its
// encryption at rest, not its auth. That round trip belongs to
// vault_seal_test.go, which still requires a real server and still skips
// without one. Two different claims, two different tests; conflating them
// would be the dishonest version of this.
type fakeVault struct {
	mu      sync.Mutex
	secrets map[string]map[string]interface{}
	version map[string]int
}

// startFakeVault brings up the stand-in and points this test's environment
// at it. Returns its address.
func startFakeVault(t *testing.T) string {
	t.Helper()

	fv := &fakeVault{
		secrets: make(map[string]map[string]interface{}),
		version: make(map[string]int),
	}
	server := httptest.NewServer(fv)
	t.Cleanup(server.Close)

	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "fake-root-token")
	return server.URL
}

func (fv *fakeVault) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// KVv2 addresses secrets at <mount>/data/<path>; everything before
	// "/data/" is the mount, everything after is the secret path. Keyed on
	// the whole thing, because two parties sealing the same ceremony id
	// differ only in the path and must not collide.
	const prefix = "/v1/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, prefix)
	if !strings.Contains(path, "/data/") {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodPut, http.MethodPost:
		fv.put(w, r, path)
	case http.MethodGet:
		fv.get(w, path)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (fv *fakeVault) put(w http.ResponseWriter, r *http.Request, path string) {
	var wrapped struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&wrapped); err != nil {
		writeVaultErrors(w, http.StatusBadRequest, fmt.Sprintf("malformed body: %v", err))
		return
	}

	fv.mu.Lock()
	fv.version[path]++
	version := fv.version[path]
	fv.secrets[path] = wrapped.Data
	fv.mu.Unlock()

	// A write returns version metadata at the top level, not nested -- the
	// client's extractVersionMetadata distinguishes the two cases, and
	// getting it wrong here would make every seal look like a failure.
	writeJSON(w, http.StatusOK, map[string]interface{}{"data": versionMetadata(version)})
}

func (fv *fakeVault) get(w http.ResponseWriter, path string) {
	fv.mu.Lock()
	data, ok := fv.secrets[path]
	version := fv.version[path]
	fv.mu.Unlock()

	if !ok {
		// An empty 404 is what the client reads as "no such secret", which
		// it surfaces as ErrSecretNotFound rather than as a transport
		// failure. A 404 carrying a body would be read as an error.
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// A read nests both under "data".
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data": map[string]interface{}{
			"data":     data,
			"metadata": versionMetadata(version),
		},
	})
}

func versionMetadata(version int) map[string]interface{} {
	return map[string]interface{}{
		"created_time":  time.Now().UTC().Format(time.RFC3339Nano),
		"deletion_time": "",
		"destroyed":     false,
		"version":       version,
	}
}

func writeVaultErrors(w http.ResponseWriter, status int, messages ...string) {
	writeJSON(w, status, map[string]interface{}{"errors": messages})
}
