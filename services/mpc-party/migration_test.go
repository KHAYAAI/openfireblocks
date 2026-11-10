package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"openfireblocks.com/services/mpc-party/enclave"
)

func shareVaultPath(id int, cer string) string {
	cfg, _ := vaultShareConfigFromEnv(os.Getenv, id, cer)
	return cfg.mount + "/data/" + cfg.keyPath
}

func waitResharing(t *testing.T, c *enclaveCluster, reshareID string) map[int]*ResharingStatusResult {
	t.Helper()
	done := map[int]*ResharingStatusResult{}
	deadline := time.Now().Add(3 * time.Minute)
	for len(done) < len(c.parties) && time.Now().Before(deadline) {
		for i, p := range c.parties {
			st, err := p.mgr.GetResharingStatus(reshareID)
			if err != nil {
				t.Fatal(err)
			}
			if st.Status == string(ceremonyFailed) {
				t.Fatalf("party %d: refresh failed: %s", i, st.Error)
			}
			if st.Status == string(ceremonyCompleted) {
				done[i] = st
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(done) != len(c.parties) {
		t.Fatal("refresh timed out")
	}
	return done
}

func signAndVerify(t *testing.T, c *enclaveCluster, signID, cer, pubHex string, committee []int) {
	t.Helper()
	msg := []byte("signed after migration into enclave sealing: " + signID)
	for _, i := range committee {
		if err := c.parties[i].mgr.StartSigning(signID, cer, msg, committee); err != nil {
			t.Fatalf("party %d: %v", i, err)
		}
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		st, err := c.parties[committee[0]].mgr.GetSigningStatus(signID)
		if err != nil {
			t.Fatal(err)
		}
		if st.Status == ceremonyFailed {
			t.Fatalf("signing failed: %s", st.Error)
		}
		if st.Signature != "" {
			sig, _ := hex.DecodeString(st.Signature)
			pub, _ := hex.DecodeString(pubHex)
			if !ed25519.Verify(pub, msg, sig) {
				t.Fatal("signature does not verify against the original key")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("signing timed out")
}

// A key generated before enclave mode, sealed in the clear in Vault, moves
// into enclave sealing: loaded under a "migrate" authorisation, blocked from
// signing, refreshed inside the enclaves, sealed as KMS envelopes, and every
// clear-text version destroyed. The key, and so its address, is unchanged.
func TestMigrateALegacyKeyIntoEnclaveSealing(t *testing.T) {
	t.Setenv("TSS_PREPARAMS_POOL", "0")
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")
	startFakeVault(t)
	fv := lastFakeVault

	// Before: an ordinary key, sealed the old way.
	legacy := runKeygenOverHTTP(t, CurveEd25519)
	cer := legacy.ceremonyID
	for i := 1; i <= 3; i++ {
		live := fv.liveVersions(shareVaultPath(i, cer))
		if len(live) != 1 || live[0]["save_data"] == nil {
			t.Fatalf("party %d: expected one clear-text version before migration, got %d", i, len(live))
		}
	}
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "")

	c := newEnclaveCluster(t, 3)

	// An ordinary restore refuses the clear-text share in enclave mode.
	if err := c.parties[1].mgr.RestoreCeremony(cer, c.peers); err == nil || !strings.Contains(err.Error(), "not sealed by an enclave") {
		t.Fatalf("restore of a legacy share in enclave mode: %v", err)
	}

	for i, p := range c.parties {
		if err := p.mgr.MigrateLegacyCeremony(cer, c.peers); err != nil {
			t.Fatalf("party %d: %v", i, err)
		}
	}
	// Loaded, but not usable until refreshed.
	if err := c.parties[1].mgr.StartSigning("too-early", cer, []byte("x"), []int{1, 2}); err == nil || !strings.Contains(err.Error(), "migration") {
		t.Fatalf("a migrating key signed: %v", err)
	}

	for i, p := range c.parties {
		if err := p.mgr.StartResharing("migrate-"+cer, cer, c.peers); err != nil {
			t.Fatalf("party %d: %v", i, err)
		}
	}
	for i, st := range waitResharing(t, c, "migrate-"+cer) {
		if !st.KeyUnchanged || !st.Sealed || st.Migration == nil || st.Migration.Error != "" || st.Migration.LegacyVersionsDestroyed != 1 {
			t.Fatalf("party %d: %+v %+v", i, st, st.Migration)
		}
	}

	// After: Vault holds one live version per party, and it is an envelope.
	for i := 1; i <= 3; i++ {
		live := fv.liveVersions(shareVaultPath(i, cer))
		if len(live) != 1 {
			t.Fatalf("party %d: %d live versions after migration, want 1", i, len(live))
		}
		if live[0]["save_data"] != nil || live[0]["enclave_envelope"] == nil {
			t.Fatalf("party %d: the live version is not an envelope", i)
		}
	}

	signAndVerify(t, c, "after-migration", cer, legacy.publicKey, []int{1, 3})

	// A restart restores normally from the envelopes, and signs.
	for i := 1; i <= 3; i++ {
		c.startParty(t, i)
	}
	for i, p := range c.parties {
		if err := p.mgr.RestoreCeremony(cer, c.peers); err != nil {
			t.Fatalf("party %d restore after migration: %v", i, err)
		}
	}
	signAndVerify(t, c, "after-restart", cer, legacy.publicKey, []int{2, 3})

	// Migrating again is refused: there is nothing in the clear to migrate.
	c.startParty(t, 1)
	if err := c.parties[1].mgr.MigrateLegacyCeremony(cer, c.peers); err == nil || !strings.Contains(err.Error(), "already enclave-sealed") {
		t.Fatalf("second migration: %v", err)
	}
}

func TestMigrationIsRefusedOutsideEnclaveMode(t *testing.T) {
	resetEnclaveState(t)
	startFakeVault(t)
	mgr := NewTSSPartyManager(1, http.DefaultClient)
	if err := mgr.MigrateLegacyCeremony("c", map[int]string{1: "x"}); err == nil || !strings.Contains(err.Error(), "enclave mode") {
		t.Fatalf("migration outside enclave mode: %v", err)
	}
}

// The restore endpoint takes migrate_legacy only in enclave mode, and only
// with an authorisation for "migrate": a restore authorisation cannot be
// replayed to pull a clear-text share into an enclave.
func TestMigrateLegacyNeedsItsOwnAuthorisation(t *testing.T) {
	resetEnclaveState(t)
	authz, priv := edAuthorizer(t)
	ps := &PartyServer{partyID: 1, authorizer: authz, tssManager: NewTSSPartyManager(1, http.DefaultClient)}
	body := func(op string) []byte {
		req := AuthorizationRequest{Operation: op, CeremonyID: "c", IssuedAt: time.Now().Unix()}
		raw, _ := json.Marshal(req)
		b, _ := json.Marshal(tssRestoreRequest{CeremonyID: "c", Peers: map[int]string{1: "https://party-1.internal"},
			MigrateLegacy: true, Authorization: string(raw), AuthorizationSignature: signEd(priv, req)})
		return b
	}
	call := func(b []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		ps.HandleTSSRestore(w, httptest.NewRequest(http.MethodPost, "/tss/keygen/restore", bytes.NewReader(b)))
		return w
	}
	if w := call(body("migrate")); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "enclave mode") {
		t.Fatalf("outside enclave mode: %d %s", w.Code, w.Body)
	}
	registerEnclaveSealer(1, &enclave.Sealer{})
	if w := call(body("restore")); w.Code != http.StatusForbidden {
		t.Fatalf("a restore authorisation was accepted for a migration: %d %s", w.Code, w.Body)
	}
	// With the right authorisation the request gets past authorisation (and
	// then fails here only because there is no Vault).
	if w := call(body("migrate")); w.Code == http.StatusForbidden {
		t.Fatalf("a migrate authorisation was refused: %s", w.Body)
	}
}
