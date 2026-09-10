package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// Integrations, and the signature that makes their webhooks trustworthy.
//
// This service had no tests. The signature function is the part that most
// needed them: it used to return "sig_" plus a fresh random UUID, derived
// from neither the payload nor the secret, so the header was present and
// verified nothing for every webhook the service ever sent. A receiver
// following the documentation would have rejected all of them; one that
// trusted the header was accepting anything.

func TestASignatureVerifiesAgainstTheSecret(t *testing.T) {
	payload := []byte(`{"event":"key.created","id":"abc"}`)
	secret := "a-shared-secret"

	signature := generateSignature(payload, secret)

	// Recomputed the way a receiver following the documented convention
	// would: HMAC-SHA256 over the exact bytes, hex, sha256= prefixed.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if signature != want {
		t.Errorf("the signature does not verify against the secret:\n got %s\nwant %s",
			signature, want)
	}
}

// A signature that does not change with the payload authenticates nothing.
func TestChangingThePayloadChangesTheSignature(t *testing.T) {
	secret := "a-shared-secret"

	first := generateSignature([]byte(`{"amount":"100"}`), secret)
	second := generateSignature([]byte(`{"amount":"100000"}`), secret)

	if first == second {
		t.Error("two different payloads produced the same signature; " +
			"an attacker could change the amount and keep the header")
	}
}

// Nor does one that does not change with the secret.
func TestChangingTheSecretChangesTheSignature(t *testing.T) {
	payload := []byte(`{"event":"key.created"}`)

	if generateSignature(payload, "secret-a") == generateSignature(payload, "secret-b") {
		t.Error("two different secrets produced the same signature; " +
			"anyone could forge a webhook to any integration")
	}
}

// Deterministic. The old implementation was not, which is the single
// property that made it useless: a receiver cannot verify a value that
// differs every time the same bytes are signed.
func TestTheSameInputAlwaysProducesTheSameSignature(t *testing.T) {
	payload := []byte(`{"event":"key.created"}`)

	first := generateSignature(payload, "secret")
	second := generateSignature(payload, "secret")

	if first != second {
		t.Errorf("signing the same payload twice gave %s and %s; nothing can verify that",
			first, second)
	}
}

// The prefix is part of the convention services/webhooks uses, so a
// receiver written against one works with the other.
func TestTheSignatureCarriesItsAlgorithm(t *testing.T) {
	signature := generateSignature([]byte("{}"), "secret")

	if !strings.HasPrefix(signature, "sha256=") {
		t.Errorf("signature %q does not name its algorithm", signature)
	}
}

// -- what an integration must supply --
//
// Validation happens before anything is stored, so these reach no database.

func TestAnIntegrationNeedsANameAndAType(t *testing.T) {
	svc := &MarketplaceService{}
	ctx := context.Background()

	cases := map[string]*CreateIntegrationRequest{
		"no name": {Type: "api_key"},
		"no type": {Name: "an integration"},
		"neither": {},
	}

	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.CreateIntegration(ctx, "cust-1", req); err == nil {
				t.Error("an integration was created with a missing required field")
			}
		})
	}
}

// A webhook integration with nowhere to deliver to is not an integration.
// Catching it here means the customer is told at creation rather than
// discovering that nothing ever arrives.
func TestAWebhookIntegrationNeedsAURL(t *testing.T) {
	svc := &MarketplaceService{}

	_, err := svc.CreateIntegration(context.Background(), "cust-1",
		&CreateIntegrationRequest{Name: "notifications", Type: "webhook"})

	if err == nil {
		t.Fatal("a webhook integration was created with no URL to deliver to")
	}
	if !strings.Contains(err.Error(), "webhook_url") {
		t.Errorf("the error does not name the missing field: %v", err)
	}
}

// -- generated credentials --

func TestGeneratedSecretsAreNotReused(t *testing.T) {
	seen := map[string]bool{}

	for i := 0; i < 1_000; i++ {
		secret := generateWebhookSecret()
		if seen[secret] {
			t.Fatalf("a webhook secret was generated twice: %s", secret)
		}
		seen[secret] = true
	}
}

func TestGeneratedAPIKeysAreNotReusedAndAreRecognisable(t *testing.T) {
	seen := map[string]bool{}

	for i := 0; i < 1_000; i++ {
		key := generateAPIKey()
		if seen[key] {
			t.Fatalf("an API key was generated twice: %s", key)
		}
		seen[key] = true
		// The prefix is what lets a leaked key be recognised in a log or a
		// paste and revoked, rather than looking like any other identifier.
		if !strings.HasPrefix(key, "sk_") {
			t.Errorf("API key %q has no recognisable prefix", key)
		}
	}
}
