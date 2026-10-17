package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// A stand-in Vault that behaves the way the real one does in the states
// that matter: uninitialised, initialised-and-sealed, unsealed -- and
// crucially, init succeeding exactly once no matter how many callers race.
type fakeVault struct {
	mu          sync.Mutex
	initialized bool
	sealed      bool
	unsealKey   string
	initCalls   int
	unsealCalls int
}

func (f *fakeVault) server() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/sys/seal-status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		// A sealed Vault answers 503 with a valid body -- the case a naive
		// client treats as an error and then refuses to fix.
		if f.sealed {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"initialized": f.initialized, "sealed": f.sealed, "t": 1,
		})
	})

	mux.HandleFunc("/v1/sys/init", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.initCalls++
		if f.initialized {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"errors":["Vault is already initialized"]}`)
			return
		}
		f.initialized = true
		f.sealed = true
		f.unsealKey = "test-unseal-key"
		json.NewEncoder(w).Encode(map[string]interface{}{
			"keys_base64": []string{f.unsealKey},
			"root_token":  "hvs.test-root",
		})
	})

	mux.HandleFunc("/v1/sys/unseal", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key string `json:"key"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.unsealCalls++
		if body.Key == f.unsealKey {
			f.sealed = false
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"initialized": f.initialized, "sealed": f.sealed, "t": 1,
		})
	})

	return httptest.NewServer(mux)
}

// A stand-in API server that enforces the one property the coordination
// depends on: creating a Secret that already exists is a 409.
type fakeKube struct {
	mu      sync.Mutex
	secrets map[string]map[string]string
	creates int
}

func (f *fakeKube) server() *httptest.Server {
	if f.secrets == nil {
		f.secrets = map[string]map[string]string{}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch r.Method {
		case http.MethodGet:
			name := parts[len(parts)-1]
			f.mu.Lock()
			data, ok := f.secrets[name]
			f.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"kind":"Status","code":404}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
		case http.MethodPost:
			var in struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Data map[string]string `json:"data"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			f.mu.Lock()
			defer f.mu.Unlock()
			f.creates++
			if _, exists := f.secrets[in.Metadata.Name]; exists {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"kind":"Status","code":409,"reason":"AlreadyExists"}`)
				return
			}
			f.secrets[in.Metadata.Name] = in.Data
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"metadata": in.Metadata})
		}
	}))
}

func (f *fakeKube) client(srv *httptest.Server) *kubeClient {
	return &kubeClient{host: srv.URL, namespace: "openfireblocks", token: "t", client: srv.Client()}
}

func (f *fakeKube) storedKey(t *testing.T, name string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.secrets[name]
	if !ok {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(data[secretKeyUnseal])
	if err != nil {
		t.Fatalf("stored unseal key is not base64: %v", err)
	}
	return string(raw)
}

// The whole point: an uninitialised Vault ends up initialised, its key
// stored, and the replica unsealed, with nobody doing anything by hand.
func TestBringsAnUninitialisedVaultAllTheWayToUnsealed(t *testing.T) {
	fv := &fakeVault{}
	vs := fv.server()
	defer vs.Close()
	fk := &fakeKube{}
	ks := fk.server()
	defer ks.Close()

	vault := newVaultClient(vs.URL)
	kube := fk.client(ks)

	// Two ticks: one to initialise, one to unseal. Deliberately not one
	// call doing everything -- the sidecar is a reconciler, and each tick
	// must move the world forward from whatever state it is actually in.
	for i := 0; i < 2; i++ {
		if err := reconcile(vault, kube, "vault-unseal", "vault-0"); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}

	if got := fk.storedKey(t, "vault-unseal"); got != "test-unseal-key" {
		t.Fatalf("stored unseal key = %q, want the key Vault returned", got)
	}
	fv.mu.Lock()
	defer fv.mu.Unlock()
	if fv.sealed {
		t.Fatal("vault is still sealed after reconciling twice")
	}
}

// Every replica runs this sidecar and they all start together. Exactly one
// unseal key must survive: if a loser overwrote the winner's key, the Secret
// would unseal nothing and the cluster would be permanently unopenable.
func TestConcurrentReplicasStoreExactlyOneUnsealKey(t *testing.T) {
	fv := &fakeVault{}
	vs := fv.server()
	defer vs.Close()
	fk := &fakeKube{}
	ks := fk.server()
	defer ks.Close()

	const replicas = 5
	var wg sync.WaitGroup
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			vault := newVaultClient(vs.URL)
			kube := fk.client(ks)
			for tick := 0; tick < 3; tick++ {
				_ = reconcile(vault, kube, "vault-unseal", fmt.Sprintf("vault-%d", n))
			}
		}(i)
	}
	wg.Wait()

	stored := fk.storedKey(t, "vault-unseal")
	fv.mu.Lock()
	defer fv.mu.Unlock()

	if stored != fv.unsealKey {
		t.Fatalf("stored key %q does not match Vault's actual unseal key %q -- "+
			"a losing replica overwrote the winner's and the cluster could never be opened", stored, fv.unsealKey)
	}
	if fv.sealed {
		t.Error("vault never got unsealed despite five replicas reconciling")
	}
	if fk.secrets["vault-unseal"] == nil {
		t.Error("no unseal material was stored at all")
	}
}

// A replica joining an already-initialised cluster must not try to
// initialise, and must unseal with the stored key.
func TestLateReplicaUnsealsWithTheStoredKey(t *testing.T) {
	fv := &fakeVault{initialized: true, sealed: true, unsealKey: "existing-key"}
	vs := fv.server()
	defer vs.Close()
	fk := &fakeKube{secrets: map[string]map[string]string{
		"vault-unseal": {
			secretKeyUnseal: base64.StdEncoding.EncodeToString([]byte("existing-key")),
			secretKeyRoot:   base64.StdEncoding.EncodeToString([]byte("hvs.root")),
		},
	}}
	ks := fk.server()
	defer ks.Close()

	if err := reconcile(newVaultClient(vs.URL), fk.client(ks), "vault-unseal", "vault-2"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	fv.mu.Lock()
	defer fv.mu.Unlock()
	if fv.sealed {
		t.Fatal("a late replica did not unseal with the stored key")
	}
	if fv.initCalls != 0 {
		t.Errorf("a late replica called init %d times; it must never re-initialise a live cluster", fv.initCalls)
	}
}

// An unsealed replica must be left alone. A sidecar that submits keys on
// every tick is noise at best and, with a multi-share config, actively
// harmful -- it would restart unseal progress.
func TestUnsealedVaultIsLeftAlone(t *testing.T) {
	fv := &fakeVault{initialized: true, sealed: false, unsealKey: "k"}
	vs := fv.server()
	defer vs.Close()
	fk := &fakeKube{}
	ks := fk.server()
	defer ks.Close()

	for i := 0; i < 3; i++ {
		if err := reconcile(newVaultClient(vs.URL), fk.client(ks), "vault-unseal", "vault-0"); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
	fv.mu.Lock()
	defer fv.mu.Unlock()
	if fv.unsealCalls != 0 {
		t.Errorf("submitted %d unseal keys to an already-unsealed Vault", fv.unsealCalls)
	}
}

// Vault being unreachable is the normal state while it starts. It must not
// be treated as an error worth crashing over -- the sidecar and Vault start
// together, and a crash-looping sidecar takes the replica's readiness with
// it.
func TestUnreachableVaultIsNotAnError(t *testing.T) {
	fk := &fakeKube{}
	ks := fk.server()
	defer ks.Close()

	// Nothing listening on port 1.
	err := reconcile(newVaultClient("http://127.0.0.1:1"), fk.client(ks), "vault-unseal", "vault-0")
	if err != nil {
		t.Fatalf("an unreachable Vault produced an error: %v", err)
	}
}

// An initialised cluster whose Secret has been deleted cannot be recovered
// by this sidecar. It must not silently spin: the operator has to find out
// that the key is gone.
func TestInitialisedButNoStoredKeyDoesNotSilentlySpin(t *testing.T) {
	fv := &fakeVault{initialized: true, sealed: true, unsealKey: "lost"}
	vs := fv.server()
	defer vs.Close()
	fk := &fakeKube{}
	ks := fk.server()
	defer ks.Close()

	if err := reconcile(newVaultClient(vs.URL), fk.client(ks), "vault-unseal", "vault-0"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	fv.mu.Lock()
	defer fv.mu.Unlock()
	// It must not have guessed at a key.
	if fv.unsealCalls != 0 {
		t.Errorf("attempted %d unseals with no key available", fv.unsealCalls)
	}
	if !fv.sealed {
		t.Error("vault was somehow unsealed without a key")
	}
}
