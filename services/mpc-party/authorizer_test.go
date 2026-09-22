package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A second signature, from a key this host does not hold.
//
// The threat it addresses is not a compromised party -- that is what the
// threshold is for. It is a compromised *orchestrator*: an attacker with
// the credentials that drive ceremonies can ask every party, correctly and
// in good faith, to sign whatever they like, and every party will comply.
// An authorising key held in a KMS breaks that, because compromising every
// host in the deployment still does not produce the signature.

func edAuthorizer(t *testing.T) (*Authorizer, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	a, err := AuthorizerFromEnv(func(k string) string {
		switch k {
		case "CEREMONY_AUTHORIZER_PUBKEY":
			return hex.EncodeToString(pub)
		case "CEREMONY_AUTHORIZER_ALG":
			return "ed25519"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("building the authorizer: %v", err)
	}
	if a == nil {
		t.Fatal("no authorizer was built from a configured key")
	}
	return a, priv
}

func signEd(priv ed25519.PrivateKey, req AuthorizationRequest) string {
	return hex.EncodeToString(ed25519.Sign(priv, CanonicalAuthorizationBytes(req)))
}

func request() AuthorizationRequest {
	return AuthorizationRequest{
		Operation:   "sign",
		CeremonyID:  "sign-1",
		MessageHash: "abcdef",
		IssuedAt:    time.Now().Unix(),
	}
}

func TestAValidAuthorizationIsAccepted(t *testing.T) {
	a, priv := edAuthorizer(t)
	req := request()

	if err := a.Verify(req, signEd(priv, req)); err != nil {
		t.Fatalf("a valid authorisation was refused: %v", err)
	}
}

// The point of the whole mechanism: a signature from any other key is not
// an authorisation, however well formed.
func TestASignatureFromAnotherKeyIsRefused(t *testing.T) {
	a, _ := edAuthorizer(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	req := request()

	if err := a.Verify(req, signEd(other, req)); err == nil {
		t.Fatal("a signature from an unauthorised key was accepted")
	}
}

// Every field that changes the meaning of the request is signed over, so
// changing any of them after the fact invalidates the signature.
func TestTheAuthorizationIsBoundToEveryFieldThatMatters(t *testing.T) {
	a, priv := edAuthorizer(t)
	original := request()
	sig := signEd(priv, original)

	mutations := map[string]func(*AuthorizationRequest){
		"operation": func(r *AuthorizationRequest) { r.Operation = "keygen" },
		"ceremony":  func(r *AuthorizationRequest) { r.CeremonyID = "sign-2" },
		"message":   func(r *AuthorizationRequest) { r.MessageHash = "cd" },
		"issued at": func(r *AuthorizationRequest) { r.IssuedAt = original.IssuedAt - 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			altered := original
			mutate(&altered)
			if err := a.Verify(altered, sig); err == nil {
				t.Errorf("changing the %s did not invalidate the authorisation", name)
			}
		})
	}
}

// A captured authorisation must stop working. Without an expiry, one
// legitimate ceremony's authorisation is a standing grant.
func TestAStaleAuthorizationIsRefused(t *testing.T) {
	a, priv := edAuthorizer(t)
	req := request()
	req.IssuedAt = time.Now().Add(-10 * time.Minute).Unix()

	if err := a.Verify(req, signEd(priv, req)); err == nil {
		t.Fatal("a ten-minute-old authorisation was accepted against a five-minute window")
	}
}

// And one from the future is refused too: accepting it would let a signer
// with a fast clock mint authorisations valid long past their window.
func TestAnAuthorizationFromTheFutureIsRefused(t *testing.T) {
	a, priv := edAuthorizer(t)
	req := request()
	req.IssuedAt = time.Now().Add(1 * time.Hour).Unix()

	if err := a.Verify(req, signEd(priv, req)); err == nil {
		t.Fatal("an authorisation dated an hour in the future was accepted")
	}
}

func TestAMalformedSignatureIsRefused(t *testing.T) {
	a, _ := edAuthorizer(t)
	for _, sig := range []string{"", "zz", "00", "0x"} {
		if err := a.Verify(request(), sig); err == nil {
			t.Errorf("signature %q was accepted", sig)
		}
	}
}

func TestAnIncompleteRequestIsRefused(t *testing.T) {
	a, priv := edAuthorizer(t)
	for name, req := range map[string]AuthorizationRequest{
		"no operation": {CeremonyID: "c", IssuedAt: time.Now().Unix()},
		"no ceremony":  {Operation: "sign", IssuedAt: time.Now().Unix()},
	} {
		if err := a.Verify(req, signEd(priv, req)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// -- P-256, which is what a cloud KMS actually issues --

func TestAP256AuthorizationFromAKmsStyleKeyIsAccepted(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	a, err := AuthorizerFromEnv(func(k string) string {
		switch k {
		case "CEREMONY_AUTHORIZER_PUBKEY":
			return hex.EncodeToString(der)
		case "CEREMONY_AUTHORIZER_ALG":
			return "ecdsa-p256"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("building: %v", err)
	}

	req := request()
	digest := sha256.Sum256(CanonicalAuthorizationBytes(req))
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	if err := a.Verify(req, hex.EncodeToString(sig)); err != nil {
		t.Fatalf("a valid P-256 authorisation was refused: %v", err)
	}
	// And a different key's is not.
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherSig, _ := ecdsa.SignASN1(rand.Reader, other, digest[:])
	if err := a.Verify(req, hex.EncodeToString(otherSig)); err == nil {
		t.Fatal("a P-256 signature from an unauthorised key was accepted")
	}
}

// -- configuration --

// Absent means off, and off must be visible rather than inferred.
func TestNoConfiguredKeyMeansNoAuthorizer(t *testing.T) {
	a, err := AuthorizerFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a != nil {
		t.Fatal("an authorizer was built with no key configured")
	}
	if a.Describe()["enabled"] != false {
		t.Error("a nil authorizer does not report itself disabled")
	}
	// A nil authorizer verifies everything, which is what "not configured"
	// has to mean -- but the description above is how an operator finds out.
	if err := a.Verify(request(), "00"); err != nil {
		t.Errorf("a nil authorizer refused: %v", err)
	}
}

// A key that does not parse is fatal, not ignored. Carrying on without
// checking would leave a deployment believing it has a second factor.
func TestAnUnusableKeyIsAnError(t *testing.T) {
	cases := map[string]map[string]string{
		"not a key":                 {"CEREMONY_AUTHORIZER_PUBKEY": "!!!!"},
		"wrong length":              {"CEREMONY_AUTHORIZER_PUBKEY": "aabb"},
		"unknown algorithm":         {"CEREMONY_AUTHORIZER_PUBKEY": hex.EncodeToString(make([]byte, 32)), "CEREMONY_AUTHORIZER_ALG": "rsa"},
		"p256 given an ed25519 key": {"CEREMONY_AUTHORIZER_PUBKEY": hex.EncodeToString(make([]byte, 32)), "CEREMONY_AUTHORIZER_ALG": "ecdsa-p256"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := AuthorizerFromEnv(func(k string) string { return env[k] }); err == nil {
				t.Errorf("%s was accepted", name)
			}
		})
	}
}

func TestTheDescriptionSaysWhatIsEnforced(t *testing.T) {
	a, _ := edAuthorizer(t)
	d := a.Describe()

	if d["enabled"] != true || d["algorithm"] != "ed25519" {
		t.Errorf("description does not reflect the configuration: %v", d)
	}
}

// The same golden vector that appears in
// services/temporal-worker/activities/ceremony_authorization_test.go.
//
// These are separate Go modules, so neither can import the other's
// encoding and the wire format is held together by this constant existing
// identically in both places. The worker signs these bytes; this party
// verifies these bytes. If either side changes how it builds them, one of
// the two suites goes red -- which is the point, because the alternative
// is a production deployment where the worker produces signatures no party
// accepts and every ceremony fails at once.
//
// A failure here is not fixed by editing the constant. It means the wire
// format moved, and moving it requires both sides to move together and the
// "-v1" in the prefix to become "-v2".
func TestTheCanonicalFormMatchesTheWorkersGoldenVector(t *testing.T) {
	const golden = "openfireblocks-authorization-v1\n" +
		"sign\n" +
		"ceremony-abc\n" +
		"deadbeef\n" +
		"1700000000"

	got := string(CanonicalAuthorizationBytes(AuthorizationRequest{
		Operation:   "sign",
		CeremonyID:  "ceremony-abc",
		MessageHash: "deadbeef",
		IssuedAt:    1700000000,
	}))
	if got != golden {
		t.Fatalf("the canonical authorisation bytes changed.\n got: %q\nwant: %q\n\n"+
			"services/temporal-worker signs this exact form. Changing it on one "+
			"side only means every ceremony is refused.", got, golden)
	}
}

// An absent message hash still occupies its field, so a keygen
// authorisation cannot canonicalise identically to some signing one.
func TestAnAbsentMessageHashStillOccupiesItsField(t *testing.T) {
	got := string(CanonicalAuthorizationBytes(AuthorizationRequest{
		Operation:  "keygen",
		CeremonyID: "c1",
		IssuedAt:   42,
	}))
	const want = "openfireblocks-authorization-v1\nkeygen\nc1\n\n42"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The gate, exercised through the HTTP handler with the exact key the
// kind deployment uses.
//
// Everything else in this file tests Verify directly. This tests the
// thing a deployment actually depends on: that a ceremony request
// carrying no authorisation is refused, and one carrying a good
// signature is accepted, at the route.
//
// The key is the published dev keypair from
// infrastructure/kind/dependencies.yaml, so a failure here also catches
// the chart and the test drifting apart.
func TestTheCeremonyGateRefusesAndAcceptsAtTheRoute(t *testing.T) {
	t.Setenv("TSS_ALLOW_UNAUTHENTICATED_PEERS", "1")

	const devSeedHex = "d3eb80d5aa9292f3fad02c6753243fc62f1b133fa54e47809c0c5cfa9e937149"
	const devPubHex = "f137c293a32763ac84ca293a59fa62f5b8117d9513605788f5f2e9b9b2b3e659"

	seed, err := hex.DecodeString(devSeedHex)
	if err != nil {
		t.Fatal(err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if got := hex.EncodeToString(priv.Public().(ed25519.PublicKey)); got != devPubHex {
		t.Fatalf("the dev keypair in infrastructure/kind no longer matches: public half is %s, "+
			"values-kind.yaml says %s", got, devPubHex)
	}

	authorizer, err := AuthorizerFromEnv(func(k string) string {
		switch k {
		case "CEREMONY_AUTHORIZER_PUBKEY":
			return devPubHex
		case "CEREMONY_AUTHORIZER_ALG":
			return "ed25519"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("building the authorizer: %v", err)
	}
	if authorizer == nil {
		t.Fatal("no authorizer was built from a configured public key")
	}

	ps := &PartyServer{partyID: 1, tssManager: NewTSSPartyManager(1, nil), authorizer: authorizer}

	// Unauthorised: refused before anything is contributed.
	body := `{"ceremony_id":"gated","threshold":1,"curve":"ed25519",` +
		`"peers":{"1":"http://a","2":"http://b","3":"http://c"}}`
	rec := httptest.NewRecorder()
	ps.HandleTSSKeygenStart(rec, httptest.NewRequest(http.MethodPost, "/tss/keygen/start", strings.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an unauthorised keygen returned HTTP %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// Authorised: the same request, carrying what the worker would send.
	req := AuthorizationRequest{
		Operation:  "keygen",
		CeremonyID: "gated",
		IssuedAt:   time.Now().Unix(),
	}
	authJSON, err := MarshalAuthorization(req)
	if err != nil {
		t.Fatal(err)
	}
	sig := hex.EncodeToString(ed25519.Sign(priv, CanonicalAuthorizationBytes(req)))

	// The authorisation travels as a JSON *string*, not a nested object --
	// the field is `Authorization string` on both sides, so the worker
	// marshals the request and the escaped result becomes the value. Got
	// this wrong writing the test and the party answered 400 rather than
	// 403, which is the correct answer to a body it cannot parse.
	authorised := fmt.Sprintf(
		`{"ceremony_id":"gated","threshold":1,"curve":"ed25519",`+
			`"peers":{"1":"http://a","2":"http://b","3":"http://c"},`+
			`"authorization":%q,"authorization_signature":%q}`,
		string(authJSON), sig)
	rec = httptest.NewRecorder()
	ps.HandleTSSKeygenStart(rec, httptest.NewRequest(http.MethodPost, "/tss/keygen/start", strings.NewReader(authorised)))
	// Asserted exactly, not "anything but 403". A body the party cannot
	// parse also is not a 403, and a test that accepts every other status
	// passes while proving nothing.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("a correctly authorised keygen returned HTTP %d, want 202: %s",
			rec.Code, rec.Body.String())
	}

	// And an authorisation for a different ceremony must not transfer.
	replayed := fmt.Sprintf(
		`{"ceremony_id":"some-other-ceremony","threshold":1,"curve":"ed25519",`+
			`"peers":{"1":"http://a","2":"http://b","3":"http://c"},`+
			`"authorization":%q,"authorization_signature":%q}`,
		string(authJSON), sig)
	rec = httptest.NewRecorder()
	ps.HandleTSSKeygenStart(rec, httptest.NewRequest(http.MethodPost, "/tss/keygen/start", strings.NewReader(replayed)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an authorisation for one ceremony was accepted for another (HTTP %d)", rec.Code)
	}
}
