package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Share retirement: each party destroys its own share, only its own, and
// only when authorised.

func retireRequest(ps *PartyServer, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	ps.HandleTSSRetire(rec, httptest.NewRequest(http.MethodPost, "/tss/shares/retire", strings.NewReader(body)))
	return rec
}

func TestRetiringDestroysThisPartysShareAndOnlyItsOwn(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")
	requireVault(t)
	key := runKeygenOverHTTP(t, CurveEd25519)
	ctx := context.Background()

	for id := range key.managers {
		if _, _, err := LoadSealedShare(ctx, os.Getenv, id, key.ceremonyID); err != nil {
			t.Fatalf("party %d has no sealed share to begin with: %v", id, err)
		}
	}

	ps := &PartyServer{partyID: 1, tssManager: key.managers[1]}
	rec := retireRequest(ps, fmt.Sprintf(`{"ceremony_id":%q}`, key.ceremonyID))
	if rec.Code != http.StatusOK {
		t.Fatalf("retire returned %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"sealed_destroyed":true`) || !strings.Contains(rec.Body.String(), `"held_in_memory":true`) {
		t.Fatalf("retire response: %s", rec.Body)
	}

	// Gone from Vault -- permanently, not soft-deleted.
	if _, _, err := LoadSealedShare(ctx, os.Getenv, 1, key.ceremonyID); err == nil {
		t.Fatal("party 1's sealed share survived retirement")
	}
	// Gone from memory: it cannot go on signing until a restart.
	if _, err := key.managers[1].GetStatus(key.ceremonyID); err == nil {
		t.Fatal("party 1 still holds the retired ceremony in memory")
	}
	// And a restore from sealed material now fails, rather than bringing
	// the retired share back.
	if err := NewTSSPartyManager(1, nil).RestoreCeremony(key.ceremonyID, key.peers); err == nil {
		t.Fatal("a retired share could be restored")
	}
	// The other parties' shares are untouched: retirement is local.
	for _, id := range []int{2, 3} {
		if _, _, err := LoadSealedShare(ctx, os.Getenv, id, key.ceremonyID); err != nil {
			t.Fatalf("retiring party 1 destroyed party %d's share: %v", id, err)
		}
	}
}

func TestRetiringNeedsARetireAuthorisation(t *testing.T) {
	const seedHex = "d3eb80d5aa9292f3fad02c6753243fc62f1b133fa54e47809c0c5cfa9e937149"
	seed, _ := hex.DecodeString(seedHex)
	priv := ed25519.NewKeyFromSeed(seed)
	pub := hex.EncodeToString(priv.Public().(ed25519.PublicKey))
	authorizer, err := AuthorizerFromEnv(func(k string) string {
		switch k {
		case "CEREMONY_AUTHORIZER_PUBKEY":
			return pub
		case "CEREMONY_AUTHORIZER_ALG":
			return "ed25519"
		}
		return ""
	})
	if err != nil || authorizer == nil {
		t.Fatalf("authorizer: %v", err)
	}
	ps := &PartyServer{partyID: 1, tssManager: NewTSSPartyManager(1, nil), authorizer: authorizer}

	signed := func(op, ceremony string) string {
		req := AuthorizationRequest{Operation: op, CeremonyID: ceremony, IssuedAt: time.Now().Unix()}
		j, _ := MarshalAuthorization(req)
		sig := hex.EncodeToString(ed25519.Sign(priv, CanonicalAuthorizationBytes(req)))
		return fmt.Sprintf(`{"ceremony_id":"old-key","authorization":%q,"authorization_signature":%q}`, string(j), sig)
	}

	if rec := retireRequest(ps, `{"ceremony_id":"old-key"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("unauthorised retirement: %d", rec.Code)
	}
	// An authorisation for something else -- a restore of the same
	// ceremony -- must not be usable to destroy it.
	if rec := retireRequest(ps, signed("restore", "old-key")); rec.Code != http.StatusForbidden {
		t.Fatalf("a restore authorisation retired a share: %d", rec.Code)
	}
	if rec := retireRequest(ps, signed("retire", "another-key")); rec.Code != http.StatusForbidden {
		t.Fatalf("an authorisation for another ceremony retired this one: %d", rec.Code)
	}
	// Correctly authorised: accepted (nothing sealed here, no Vault).
	t.Setenv("VAULT_ADDR", "")
	rec := retireRequest(ps, signed("retire", "old-key"))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorised retirement: %d %s", rec.Code, rec.Body)
	}
}

func TestRetiringWithoutACeremonyIDIsABadRequest(t *testing.T) {
	ps := &PartyServer{partyID: 1, tssManager: NewTSSPartyManager(1, nil)}
	if rec := retireRequest(ps, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
}
