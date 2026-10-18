package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

// Proving a key refresh refreshed something, and refreshed the right thing.
//
// Two properties have to hold at once, and each is worthless without the
// other:
//
//   1. The key does not change. A "refresh" that produces a different
//      public key has generated a new key and stranded the customer's
//      money at the old address.
//   2. The shares do change. A refresh that leaves the shares as they were
//      has done nothing at all, while reporting success -- which is worse
//      than not having the feature, because an operator now believes the
//      compromise window resets on a schedule when it does not.
//
// The test that settles both is the last one: sign with the refreshed
// shares, and have crypto/ed25519 verify against the address from before
// the refresh. That can only pass if the new shares control the old key.

// shareFingerprint is a stable digest of what this party actually holds.
//
// Serialised through the same path Vault sealing uses, so a change here
// means a change in what gets sealed -- which is the thing that has to
// differ after a refresh.
func shareFingerprint(t *testing.T, mgr *TSSPartyManager, ceremonyID string) string {
	t.Helper()
	mgr.mu.Lock()
	ceremony, ok := mgr.ceremonies[ceremonyID]
	mgr.mu.Unlock()
	if !ok {
		t.Fatalf("party %d has no ceremony %s", mgr.partyID, ceremonyID)
	}
	ceremony.mu.Lock()
	share := ceremony.saveData
	ceremony.mu.Unlock()
	if share == nil {
		t.Fatalf("party %d holds no share for %s", mgr.partyID, ceremonyID)
	}
	raw, err := json.Marshal(share)
	if err != nil {
		t.Fatalf("serialising the share: %v", err)
	}
	return hex.EncodeToString(raw[:min(len(raw), 512)])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestKeyRefreshKeepsTheKeyAndChangesTheShares(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")

	key := runKeygenOverHTTP(t, CurveEd25519)

	before := map[int]string{}
	for id, mgr := range key.managers {
		before[id] = shareFingerprint(t, mgr, key.ceremonyID)
	}

	const reshareID = "refresh-1"
	for id, mgr := range key.managers {
		if err := mgr.StartResharing(reshareID, key.ceremonyID, key.peers); err != nil {
			t.Fatalf("party %d: StartResharing failed: %v", id, err)
		}
	}

	// Every party must finish. A refresh in which one party keeps its old
	// share leaves that party unable to sign with the others.
	done := map[int]*ResharingStatusResult{}
	deadline := time.Now().Add(3 * time.Minute)
	for len(done) < len(key.managers) && time.Now().Before(deadline) {
		for id, mgr := range key.managers {
			if _, finished := done[id]; finished {
				continue
			}
			status, err := mgr.GetResharingStatus(reshareID)
			if err != nil {
				t.Fatalf("party %d: GetResharingStatus failed: %v", id, err)
			}
			switch status.Status {
			case string(ceremonyCompleted):
				done[id] = status
			case string(ceremonyFailed):
				t.Fatalf("party %d: refresh failed: %s", id, status.Error)
			}
		}
		if len(done) < len(key.managers) {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if len(done) != len(key.managers) {
		t.Fatalf("only %d of %d parties completed the refresh", len(done), len(key.managers))
	}

	// Property 1: the key is the same.
	for id, status := range done {
		if status.Address != key.address {
			t.Fatalf("party %d reports address %s after the refresh, was %s -- "+
				"the refresh produced a different key", id, status.Address, key.address)
		}
		if !status.KeyUnchanged {
			t.Errorf("party %d did not assert the key was unchanged", id)
		}
	}

	// Property 2: the shares are not.
	for id, mgr := range key.managers {
		after := shareFingerprint(t, mgr, key.ceremonyID)
		if after == before[id] {
			t.Fatalf("party %d holds an identical share after the refresh; "+
				"nothing was refreshed, and the compromise window did not reset", id)
		}
	}

	// The proof that ties them together: sign with the refreshed shares,
	// verify against the key from before the refresh.
	message := []byte("signed after a proactive key refresh")
	committee := []int{1, 2}
	const signID = "post-refresh-signing"
	for _, id := range committee {
		if err := key.managers[id].StartSigning(signID, key.ceremonyID, message, committee); err != nil {
			t.Fatalf("party %d: StartSigning after refresh failed: %v", id, err)
		}
	}

	var signature string
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		status, err := key.managers[committee[0]].GetSigningStatus(signID)
		if err != nil {
			t.Fatalf("GetSigningStatus failed: %v", err)
		}
		if status.Status == ceremonyFailed {
			t.Fatalf("signing with refreshed shares failed: %s", status.Error)
		}
		if status.Status == ceremonyCompleted && status.Signature != "" {
			signature = status.Signature
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if signature == "" {
		t.Fatal("timed out signing with the refreshed shares")
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
		t.Fatal("a signature from the refreshed shares does not verify against the " +
			"original public key; the refresh did not preserve the key")
	}

	t.Logf("SUCCESS: shares refreshed on all %d parties, key unchanged at %s, "+
		"and the refreshed shares still sign for it", len(key.managers), key.address)
}

// A refresh of something that was never generated must be refused rather
// than starting a ceremony that can only fail halfway.
func TestRefreshingAnUnknownCeremonyIsRefused(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")

	mgr := NewTSSPartyManager(1, nil)
	err := mgr.StartResharing("r1", "no-such-ceremony", map[int]string{1: "http://x"})

	if err == nil {
		t.Fatal("a refresh of an unknown ceremony was accepted")
	}
}

func TestARefreshIdRefusesToBeReused(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")

	key := runKeygenOverHTTP(t, CurveEd25519)
	mgr := key.managers[1]

	if err := mgr.StartResharing("dup", key.ceremonyID, key.peers); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if err := mgr.StartResharing("dup", key.ceremonyID, key.peers); err == nil {
		t.Fatal("the same refresh id was accepted twice; the second would race the first " +
			"for the same key's share")
	}
}
