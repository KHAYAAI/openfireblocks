package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeParty struct {
	mu        sync.Mutex
	restored  map[string]any
	reshared  map[string]any
	polls     int
	migration map[string]any
}

func (f *fakeParty) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch {
	case r.URL.Path == "/tss/keygen/restore":
		f.restored = body
	case r.URL.Path == "/tss/reshare/start":
		f.reshared = body
	case strings.HasSuffix(r.URL.Path, "/status"):
		f.polls++
		st := map[string]any{"status": "in_progress"}
		if f.polls > 1 {
			st = map[string]any{"status": "completed", "key_unchanged": true, "sealed": true, "address": "addr", "migration": f.migration}
		}
		_ = json.NewEncoder(w).Encode(st)
		return
	default:
		http.NotFound(w, r)
		return
	}
	_, _ = io.WriteString(w, `{"status":"accepted"}`)
}

func setup(t *testing.T, migration map[string]any) (migrateOptions, []*fakeParty) {
	t.Helper()
	addrs := map[string]string{}
	var fakes []*fakeParty
	for i := 1; i <= 3; i++ {
		f := &fakeParty{migration: migration}
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		addrs[fmt.Sprintf("party-%d.internal:7000", i)] = srv.Listener.Addr().String()
		fakes = append(fakes, f)
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, n, a string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, n, addrs[a])
	}}}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return migrateOptions{
		ceremony: "cer-1", parties: 3, domain: "internal", port: 7000, scheme: "http", client: client,
		auths:   map[string]signedAuth{"migrate": signWith(priv, "migrate", "cer-1"), "reshare": signWith(priv, "reshare", ReshareID("cer-1"))},
		timeout: 10 * time.Second, poll: 10 * time.Millisecond, out: io.Discard,
	}, fakes
}

func TestMigrateDrivesEveryParty(t *testing.T) {
	o, fakes := setup(t, map[string]any{"legacy_versions_destroyed": 1})
	if err := migrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	for i, f := range fakes {
		if f.restored["migrate_legacy"] != true || f.restored["ceremony_id"] != "cer-1" {
			t.Fatalf("party %d restore body: %v", i+1, f.restored)
		}
		if f.reshared["reshare_id"] != "migrate-cer-1" || f.reshared["source_ceremony_id"] != "cer-1" {
			t.Fatalf("party %d reshare body: %v", i+1, f.reshared)
		}
		peers := f.reshared["peers"].(map[string]any)
		if peers["2"] != "http://party-2.internal:7000" {
			t.Fatalf("peers: %v", peers)
		}
		var a map[string]any
		_ = json.Unmarshal([]byte(f.restored["authorization"].(string)), &a)
		if a["operation"] != "migrate" || a["ceremony_id"] != "cer-1" {
			t.Fatalf("migrate authorisation: %v", a)
		}
		_ = json.Unmarshal([]byte(f.reshared["authorization"].(string)), &a)
		if a["operation"] != "reshare" || a["ceremony_id"] != "migrate-cer-1" {
			t.Fatalf("reshare authorisation: %v", a)
		}
	}
}

func TestMigrateReportsAFailedCleanUp(t *testing.T) {
	o, _ := setup(t, map[string]any{"legacy_versions_destroyed": 0, "error": "destroying versions failed"})
	if err := migrate(context.Background(), o); err == nil || !strings.Contains(err.Error(), "destroying versions failed") {
		t.Fatalf("expected the clean-up failure, got %v", err)
	}
	o2, _ := setup(t, nil)
	if err := migrate(context.Background(), o2); err == nil || !strings.Contains(err.Error(), "no migration result") {
		t.Fatalf("expected a missing migration result to fail, got %v", err)
	}
	o3, _ := setup(t, map[string]any{})
	delete(o3.auths, "reshare")
	if err := migrate(context.Background(), o3); err == nil {
		t.Fatal("ran without a reshare authorisation")
	}
}

// The signature must verify over the same canonical bytes the parties use
// (services/mpc-party/authorizer.go).
func TestAuthorisationsVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := signWith(priv, "migrate", "cer-9")
	var req struct {
		Operation  string `json:"operation"`
		CeremonyID string `json:"ceremony_id"`
		IssuedAt   int64  `json:"issued_at"`
	}
	_ = json.Unmarshal([]byte(a.Authorization), &req)
	want := fmt.Sprintf("openfireblocks-authorization-v1\nmigrate\ncer-9\n\n%d", req.IssuedAt)
	if string(canonical(req.Operation, req.CeremonyID, req.IssuedAt)) != want {
		t.Fatal("canonical form drifted from authorizer.go")
	}
	sig, _ := hex.DecodeString(a.Signature)
	if !ed25519.Verify(pub, []byte(want), sig) {
		t.Fatal("signature does not verify")
	}
}

func TestLoadAuths(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed")
	_ = os.WriteFile(seed, []byte(strings.Repeat("ab", 32)), 0o600)
	m, err := loadAuths(seed, "", "c")
	if err != nil || m["migrate"].Signature == "" || m["reshare"].Signature == "" {
		t.Fatalf("%v %v", m, err)
	}
	file := filepath.Join(dir, "a.json")
	_ = os.WriteFile(file, []byte(`{"migrate":{"authorization":"a","signature":"b"},"reshare":{"authorization":"c","signature":"d"}}`), 0o600)
	if m, err := loadAuths("", file, "c"); err != nil || m["reshare"].Signature != "d" {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := loadAuths(seed, file, "c"); err == nil {
		t.Fatal("both accepted")
	}
	if _, err := loadAuths("", "", "c"); err == nil {
		t.Fatal("neither accepted")
	}
}
