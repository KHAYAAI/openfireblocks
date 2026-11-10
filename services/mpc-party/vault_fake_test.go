package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
	// Every version ever written, and which were destroyed, as KV v2 keeps
	// them: a migration must be checked against the history, not the latest.
	history   map[string][]map[string]interface{}
	destroyed map[string]map[int]bool
}

// lastFakeVault is the most recently started fake, for tests that inspect
// version history directly.
var lastFakeVault *fakeVault

// startFakeVault brings up the stand-in and points this test's environment
// at it. Returns its address.
func startFakeVault(t *testing.T) string {
	t.Helper()

	fv := &fakeVault{
		secrets:   make(map[string]map[string]interface{}),
		version:   make(map[string]int),
		history:   make(map[string][]map[string]interface{}),
		destroyed: make(map[string]map[int]bool),
	}
	lastFakeVault = fv
	server := httptest.NewServer(fv)
	t.Cleanup(server.Close)

	t.Setenv("VAULT_ADDR", server.URL)
	t.Setenv("VAULT_TOKEN", "fake-root-token")
	// So a test's own log lines can say which Vault they proved anything against.
	t.Setenv("OFB_FAKE_VAULT", "1")
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
	// DeleteMetadata: DELETE <mount>/metadata/<path> destroys every
	// version. Stored under the /data/ spelling of the same path.
	if r.Method == http.MethodDelete && strings.Contains(path, "/metadata/") {
		dataPath := strings.Replace(path, "/metadata/", "/data/", 1)
		fv.mu.Lock()
		delete(fv.secrets, dataPath)
		delete(fv.version, dataPath)
		delete(fv.history, dataPath)
		delete(fv.destroyed, dataPath)
		fv.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet && strings.Contains(path, "/metadata/") {
		fv.metadata(w, strings.Replace(path, "/metadata/", "/data/", 1))
		return
	}
	if (r.Method == http.MethodPut || r.Method == http.MethodPost) && strings.Contains(path, "/destroy/") {
		fv.destroy(w, r, strings.Replace(path, "/destroy/", "/data/", 1))
		return
	}
	if !strings.Contains(path, "/data/") {
		http.NotFound(w, r)
		return
	}
	if v := r.URL.Query().Get("version"); v != "" && r.Method == http.MethodGet {
		fv.getVersion(w, path, v)
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
	fv.history[path] = append(fv.history[path], wrapped.Data)
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

// vaultUnderTest names which of the two a test actually ran against, so a
// passing log line cannot be read as a stronger claim than it is.
func vaultUnderTest() string {
	if os.Getenv("OFB_FAKE_VAULT") == "1" {
		return "the in-process KV v2 stand-in"
	}
	return "a real Vault"
}

func (fv *fakeVault) metadata(w http.ResponseWriter, path string) {
	fv.mu.Lock()
	defer fv.mu.Unlock()
	hist, ok := fv.history[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	versions := map[string]interface{}{}
	for i := range hist {
		n := i + 1
		versions[fmt.Sprint(n)] = map[string]interface{}{
			"created_time":  time.Now().UTC().Format(time.RFC3339Nano),
			"deletion_time": "",
			"destroyed":     fv.destroyed[path][n],
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"data": map[string]interface{}{
		"current_version": len(hist),
		"oldest_version":  1,
		"max_versions":    0,
		"versions":        versions,
		"created_time":    time.Now().UTC().Format(time.RFC3339Nano),
		"updated_time":    time.Now().UTC().Format(time.RFC3339Nano),
	}})
}

func (fv *fakeVault) destroy(w http.ResponseWriter, r *http.Request, path string) {
	var body struct {
		Versions []int `json:"versions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeVaultErrors(w, http.StatusBadRequest, "malformed body")
		return
	}
	fv.mu.Lock()
	if fv.destroyed[path] == nil {
		fv.destroyed[path] = map[int]bool{}
	}
	for _, v := range body.Versions {
		fv.destroyed[path][v] = true
	}
	fv.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (fv *fakeVault) getVersion(w http.ResponseWriter, path, v string) {
	var n int
	fmt.Sscanf(v, "%d", &n)
	fv.mu.Lock()
	hist := fv.history[path]
	gone := fv.destroyed[path][n]
	fv.mu.Unlock()
	if n < 1 || n > len(hist) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	data := hist[n-1]
	meta := versionMetadata(n)
	if gone {
		data = nil
		meta["destroyed"] = true
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"data": map[string]interface{}{"data": data, "metadata": meta}})
}

// liveVersions returns the versions of a path whose data still exists.
func (fv *fakeVault) liveVersions(path string) []map[string]interface{} {
	fv.mu.Lock()
	defer fv.mu.Unlock()
	var out []map[string]interface{}
	for i, d := range fv.history[path] {
		if !fv.destroyed[path][i+1] {
			out = append(out, d)
		}
	}
	return out
}
