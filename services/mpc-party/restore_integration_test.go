package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// The recovery claim, proven: parties that lose everything in memory can
// come back from sealed material and sign for the same key.
//
// This is the question a bank's risk committee asks about a small vendor
// holding custody infrastructure -- what happens to our assets if you
// disappear -- and until this existed the honest answer was worse than it
// looked. Shares were sealed in Vault, but the committee identities, the
// threshold, the curve and the refresh epoch that tss-lib needs to *use* a
// share were only ever in the process that died. A restarted party held a
// perfectly good share it could not sign with.
//
// Needs a real Vault. `vault server -dev`, then VAULT_ADDR and
// VAULT_TOKEN.
func TestParticipantsCanBeRestoredFromSealedMaterialAndStillSign(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")
	requireVault(t)

	// Phase 1: a normal ceremony, sealed.
	key := runKeygenOverHTTP(t, CurveEd25519)
	originalAddress := key.address

	// Phase 2: total loss. New managers, new servers, nothing in memory --
	// the same state a fleet rebuilt from scratch would be in.
	restored := make(map[int]*TSSPartyManager, len(key.managers))
	peers := make(map[int]string, len(key.managers))
	for id := range key.managers {
		mgr := NewTSSPartyManager(id, &http.Client{Timeout: 10 * time.Second})
		restored[id] = mgr

		router := mux.NewRouter()
		ps := &PartyServer{partyID: id, tssManager: mgr}
		router.HandleFunc("/tss/sign/message", ps.HandleTSSSignMessage).Methods(http.MethodPost)
		server := httptest.NewServer(router)
		t.Cleanup(server.Close)
		peers[id] = server.URL
	}

	// A fresh party holds nothing, and must say so rather than pretending.
	if _, err := restored[1].GetStatus(key.ceremonyID); err == nil {
		t.Fatal("a fresh party reported knowledge of a ceremony it never ran")
	}

	// Phase 3: restore each party from what was sealed.
	for id, mgr := range restored {
		if err := mgr.RestoreCeremony(key.ceremonyID, peers); err != nil {
			t.Fatalf("party %d: RestoreCeremony failed: %v", id, err)
		}
		status, err := mgr.GetStatus(key.ceremonyID)
		if err != nil {
			t.Fatalf("party %d: GetStatus after restore: %v", id, err)
		}
		if status.Address != originalAddress {
			t.Fatalf("party %d restored to address %s, want %s",
				id, status.Address, originalAddress)
		}
	}

	// Phase 4: the only proof that matters. Sign with the restored
	// parties and verify against the original key.
	message := []byte("signed by a committee restored from sealed material")
	committee := []int{1, 2}
	const signID = "post-restore-signing"
	for _, id := range committee {
		if err := restored[id].StartSigning(signID, key.ceremonyID, message, committee); err != nil {
			t.Fatalf("party %d: StartSigning after restore failed: %v", id, err)
		}
	}

	var signature string
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		status, err := restored[committee[0]].GetSigningStatus(signID)
		if err != nil {
			t.Fatalf("GetSigningStatus: %v", err)
		}
		if status.Status == ceremonyFailed {
			t.Fatalf("signing with restored shares failed: %s", status.Error)
		}
		if status.Status == ceremonyCompleted && status.Signature != "" {
			signature = status.Signature
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if signature == "" {
		t.Fatal("timed out signing with restored shares")
	}

	sig, err := hex.DecodeString(signature)
	if err != nil {
		t.Fatalf("signature is not hex: %v", err)
	}
	pubKey, err := hex.DecodeString(key.publicKey)
	if err != nil {
		t.Fatalf("public key is not hex: %v", err)
	}
	if !ed25519.Verify(pubKey, message, sig) {
		t.Fatal("a signature from restored shares does not verify against the original " +
			"public key; the recovery procedure does not recover the key")
	}

	t.Logf("SUCCESS: every party rebuilt from sealed material and the restored "+
		"committee signed for %s", originalAddress)
}

// The same recovery, after a proactive refresh -- which is the case that
// actually breaks.
//
// A refresh re-seals the share at the same Vault path, so it overwrites
// what the DKG sealed. If it writes the share alone, it strips the
// ceremony context that makes the share usable, and the key becomes
// unrecoverable at the exact moment it was made more secure. If it writes
// the context but keeps the old epoch, the restore succeeds, the committee
// is rebuilt at the x-coordinates the DKG used, the refreshed shares do
// not lie on that polynomial, and signing produces something that verifies
// against nothing.
//
// Both were live. The first was the code as written; the second is what
// section 4 of docs/deployment/KEY-RECOVERY.md warns an operator about,
// and a warning in a document does not help if the platform does it to
// itself. The test that settles it is the same one as always: sign with
// what came back, and check it against the address from before any of this.
func TestAKeyCanStillBeRecoveredAfterARefresh(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")
	requireVault(t)

	key := runKeygenOverHTTP(t, CurveEd25519)
	originalAddress := key.address

	const reshareID = "refresh-before-recovery"
	for id, mgr := range key.managers {
		if err := mgr.StartResharing(reshareID, key.ceremonyID, key.peers); err != nil {
			t.Fatalf("party %d: StartResharing failed: %v", id, err)
		}
	}
	done := map[int]bool{}
	deadline := time.Now().Add(3 * time.Minute)
	for len(done) < len(key.managers) && time.Now().Before(deadline) {
		for id, mgr := range key.managers {
			if done[id] {
				continue
			}
			status, err := mgr.GetResharingStatus(reshareID)
			if err != nil {
				t.Fatalf("party %d: GetResharingStatus failed: %v", id, err)
			}
			switch status.Status {
			case string(ceremonyCompleted):
				done[id] = true
			case string(ceremonyFailed):
				t.Fatalf("party %d: refresh failed: %s", id, status.Error)
			}
		}
		if len(done) < len(key.managers) {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if len(done) < len(key.managers) {
		t.Fatalf("only %d of %d parties finished the refresh", len(done), len(key.managers))
	}

	// Total loss, after the refresh. Whatever is in Vault now is all there
	// is, and it was written by the refresh rather than by the DKG.
	restored := make(map[int]*TSSPartyManager, len(key.managers))
	peers := make(map[int]string, len(key.managers))
	for id := range key.managers {
		mgr := NewTSSPartyManager(id, &http.Client{Timeout: 10 * time.Second})
		restored[id] = mgr

		router := mux.NewRouter()
		ps := &PartyServer{partyID: id, tssManager: mgr}
		router.HandleFunc("/tss/sign/message", ps.HandleTSSSignMessage).Methods(http.MethodPost)
		server := httptest.NewServer(router)
		t.Cleanup(server.Close)
		peers[id] = server.URL
	}

	for id, mgr := range restored {
		if err := mgr.RestoreCeremony(key.ceremonyID, peers); err != nil {
			t.Fatalf("party %d: restoring a refreshed key failed: %v", id, err)
		}
		status, err := mgr.GetStatus(key.ceremonyID)
		if err != nil {
			t.Fatalf("party %d: GetStatus after restore: %v", id, err)
		}
		if status.Address != originalAddress {
			t.Fatalf("party %d restored to address %s, want %s",
				id, status.Address, originalAddress)
		}
	}

	message := []byte("signed by a committee restored from refreshed material")
	committee := []int{1, 2}
	const signID = "post-refresh-restore-signing"
	for _, id := range committee {
		if err := restored[id].StartSigning(signID, key.ceremonyID, message, committee); err != nil {
			t.Fatalf("party %d: StartSigning after restore failed: %v", id, err)
		}
	}

	var signature string
	signDeadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(signDeadline) {
		status, err := restored[committee[0]].GetSigningStatus(signID)
		if err != nil {
			t.Fatalf("GetSigningStatus: %v", err)
		}
		if status.Status == ceremonyFailed {
			t.Fatalf("signing with restored refreshed shares failed: %s", status.Error)
		}
		if status.Status == ceremonyCompleted && status.Signature != "" {
			signature = status.Signature
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if signature == "" {
		t.Fatal("timed out signing with restored refreshed shares")
	}

	sig, err := hex.DecodeString(signature)
	if err != nil {
		t.Fatalf("signature is not hex: %v", err)
	}
	pubKey, err := hex.DecodeString(key.publicKey)
	if err != nil {
		t.Fatalf("public key is not hex: %v", err)
	}
	if !ed25519.Verify(pubKey, message, sig) {
		t.Fatal("a signature from shares restored after a refresh does not verify against " +
			"the original public key; a refresh has made the key unrecoverable")
	}

	t.Logf("SUCCESS: the key was refreshed, every party was destroyed, and the "+
		"committee restored from the refreshed material still signs for %s",
		originalAddress)
}

// A share sealed without its context cannot be restored into a signing
// party, and the refusal has to say so rather than loading something
// unusable.
func TestRestoringWithoutACeremonyContextIsRefused(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")
	requireVault(t)

	key := runKeygenOverHTTP(t, CurveEd25519)

	// Re-seal without the context, as an older build would have.
	mgr := key.managers[1]
	mgr.mu.Lock()
	share := mgr.ceremonies[key.ceremonyID].saveData
	mgr.mu.Unlock()

	const bareID = "sealed-without-context"
	if _, err := SealKeyShare(context.Background(), os.Getenv, 1, bareID, share); err != nil {
		t.Fatalf("sealing: %v", err)
	}

	fresh := NewTSSPartyManager(1, nil)
	err := fresh.RestoreCeremony(bareID, map[int]string{1: "http://a", 2: "http://b", 3: "http://c"})
	if err == nil {
		t.Fatal("a share sealed with no ceremony context was restored anyway")
	}
}

func TestRestoringAnUnknownCeremonyIsRefused(t *testing.T) {
	requireVault(t)

	mgr := NewTSSPartyManager(1, nil)
	if err := mgr.RestoreCeremony("never-existed", map[int]string{1: "http://a"}); err == nil {
		t.Fatal("restoring a ceremony that was never sealed succeeded")
	}
}

// requireVault gives the test somewhere to seal to.
//
// A real one if the environment already points at one -- run these against
// `vault server -dev` and they exercise the genuine article. Otherwise the
// in-process stand-in from vault_fake_test.go, so the restore path is
// covered on every run and in CI rather than skipped on every machine that
// has not installed a Vault binary. See the comment there for what each of
// those two arrangements does and does not prove.
func requireVault(t *testing.T) {
	t.Helper()
	if os.Getenv("VAULT_ADDR") != "" {
		t.Logf("using the Vault at %s", os.Getenv("VAULT_ADDR"))
		return
	}
	startFakeVault(t)
}
