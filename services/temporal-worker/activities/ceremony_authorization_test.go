package activities

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The golden vector.
//
// This exact byte string also appears in
// services/mpc-party/authorizer_test.go. The two modules cannot import
// each other, so this constant is the contract between them: the worker
// signs these bytes and the party verifies these bytes, and if either side
// changes how it builds them, one of the two suites goes red rather than
// production quietly producing signatures nobody accepts.
//
// Do not "fix" a failure here by editing the constant. A mismatch means
// the wire format changed, which is a deployment-breaking change that
// needs both sides moved together and a version bump in the prefix.
const goldenCanonical = "openfireblocks-authorization-v1\n" +
	"sign\n" +
	"ceremony-abc\n" +
	"deadbeef\n" +
	"1700000000"

func goldenRequest() authorizationRequest {
	return authorizationRequest{
		Operation:   "sign",
		CeremonyID:  "ceremony-abc",
		MessageHash: "deadbeef",
		IssuedAt:    1700000000,
	}
}

func TestTheCanonicalFormMatchesTheGoldenVector(t *testing.T) {
	got := string(canonicalAuthorizationBytes(goldenRequest()))
	if got != goldenCanonical {
		t.Fatalf("the canonical authorisation bytes changed.\n got: %q\nwant: %q\n\n"+
			"This is the wire format services/mpc-party verifies. Changing it "+
			"breaks every deployment mid-upgrade unless both sides move together.",
			got, goldenCanonical)
	}
}

// An omitted message hash must not shift the remaining fields.
//
// Worth its own test because the obvious bug is to skip the empty field
// rather than emit it, which silently makes a keygen authorisation
// canonicalise the same way as some signing authorisation.
func TestAnEmptyMessageHashStillOccupiesItsField(t *testing.T) {
	keygen := canonicalAuthorizationBytes(authorizationRequest{
		Operation:  "keygen",
		CeremonyID: "c1",
		IssuedAt:   42,
	})
	want := "openfireblocks-authorization-v1\nkeygen\nc1\n\n42"
	if string(keygen) != want {
		t.Fatalf("got %q, want %q", keygen, want)
	}
}

func TestNoAuthorizerIsConfiguredByDefault(t *testing.T) {
	auth, err := newCeremonyAuthorizer(func(string) string { return "" }, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth != nil {
		t.Fatal("an authoriser was built with nothing configured")
	}
	// A nil authoriser must be usable, because that is the default path
	// every existing deployment takes.
	body, sig, err := auth.authorize(context.Background(), "keygen", "c1", "")
	if err != nil || body != "" || sig != "" {
		t.Fatalf("a nil authoriser should authorise nothing without error, got (%q, %q, %v)", body, sig, err)
	}
	if auth.describe() != "none configured" {
		t.Fatalf("describe() = %q", auth.describe())
	}
}

// The end-to-end property: what this worker signs, the party's verifier
// accepts.
//
// Verified here by reimplementing the party's check with crypto/ed25519
// directly rather than by importing it, which is the same thing the party
// does and keeps the two modules independent.
func TestAnEd25519AuthorizationVerifiesTheWayThePartyWillVerifyIt(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	auth, err := newCeremonyAuthorizer(envFrom(map[string]string{
		"CEREMONY_AUTHORIZER_KEY": hex.EncodeToString(priv.Seed()),
	}), nil)
	if err != nil {
		t.Fatalf("building the authoriser: %v", err)
	}

	body, sigHex, err := auth.authorize(context.Background(), "sign", "sign-1", "abc123")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}

	var req authorizationRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("the authorisation is not readable JSON: %v", err)
	}
	if req.Operation != "sign" || req.CeremonyID != "sign-1" || req.MessageHash != "abc123" {
		t.Fatalf("the authorisation does not describe the request it was made for: %+v", req)
	}
	if age := time.Since(time.Unix(req.IssuedAt, 0)); age > time.Minute || age < -time.Second {
		t.Fatalf("issued_at is %s away from now; a party bounds this", age)
	}

	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		t.Fatalf("the signature is not hex: %v", err)
	}
	if !ed25519.Verify(pub, canonicalAuthorizationBytes(req), sig) {
		t.Fatal("the party would reject this signature")
	}
}

func TestAP256AuthorizationVerifiesTheWayThePartyWillVerifyIt(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	dir := t.TempDir()
	path := filepath.Join(dir, "authorizer.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	auth, err := newCeremonyAuthorizer(envFrom(map[string]string{
		"CEREMONY_AUTHORIZER_KEY_FILE": path,
	}), nil)
	if err != nil {
		t.Fatalf("building the authoriser: %v", err)
	}

	body, sigHex, err := auth.authorize(context.Background(), "keygen", "c-9", "")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	var req authorizationRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		t.Fatal(err)
	}
	// SHA-256 over the canonical bytes, which is what the party does for
	// P-256 and what a cloud KMS signs with for this curve.
	digest := sha256.Sum256(canonicalAuthorizationBytes(req))
	if !ecdsa.VerifyASN1(&key.PublicKey, digest[:], sig) {
		t.Fatal("the party would reject this P-256 signature")
	}
}

// A signature over one request must not verify against another.
//
// The whole value of putting the operation, ceremony and digest in the
// signed bytes is that swapping any of them invalidates it.
func TestAnAuthorizationDoesNotTransferToAnotherRequest(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed := goldenRequest()
	sig := ed25519.Sign(priv, canonicalAuthorizationBytes(signed))

	for name, tampered := range map[string]authorizationRequest{
		"a different operation":  {Operation: "keygen", CeremonyID: "ceremony-abc", MessageHash: "deadbeef", IssuedAt: 1700000000},
		"a different ceremony":   {Operation: "sign", CeremonyID: "ceremony-xyz", MessageHash: "deadbeef", IssuedAt: 1700000000},
		"a different message":    {Operation: "sign", CeremonyID: "ceremony-abc", MessageHash: "cafebabe", IssuedAt: 1700000000},
		"a different issue time": {Operation: "sign", CeremonyID: "ceremony-abc", MessageHash: "deadbeef", IssuedAt: 1700000001},
	} {
		if ed25519.Verify(pub, canonicalAuthorizationBytes(tampered), sig) {
			t.Fatalf("an authorisation for one request verified against %s", name)
		}
	}
}

// ---------------------------------------------------------------------------
// The external signer, which is the path a KMS-backed deployment takes
// ---------------------------------------------------------------------------

func TestTheExternalSignerReceivesTheCanonicalBytesAndItsSignatureIsUsed(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	var sawAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthHeader = r.Header.Get("Authorization")

		var in externalSignRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("unreadable request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// The signer must receive the bytes, not a digest -- a service
		// that decides what to sign has to be able to read what it is
		// signing.
		canonical, err := base64.StdEncoding.DecodeString(in.Canonical)
		if err != nil {
			t.Errorf("canonical_b64 is not base64: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(canonical) == 0 || string(canonical[:31]) != "openfireblocks-authorization-v1" {
			t.Errorf("the signer was sent something other than a canonical authorisation: %q", canonical)
		}
		_ = json.NewEncoder(w).Encode(externalSignResponse{
			SignatureHex: hex.EncodeToString(ed25519.Sign(priv, canonical)),
		})
	}))
	defer server.Close()

	auth, err := newCeremonyAuthorizer(envFrom(map[string]string{
		"CEREMONY_AUTHORIZER_SIGNER_URL":   server.URL,
		"CEREMONY_AUTHORIZER_SIGNER_TOKEN": "s3cret",
	}), server.Client())
	if err != nil {
		t.Fatal(err)
	}

	body, sigHex, err := auth.authorize(context.Background(), "sign", "s-1", "ff00")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if sawAuthHeader != "Bearer s3cret" {
		t.Fatalf("the signer was called with Authorization %q", sawAuthHeader)
	}

	var req authorizationRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	sig, _ := hex.DecodeString(sigHex)
	if !ed25519.Verify(pub, canonicalAuthorizationBytes(req), sig) {
		t.Fatal("the signature the external signer returned is not the one the party will check")
	}
}

// A signer that declines must fail the ceremony, with its reason intact.
//
// This is the control working, not a fault: the external signer is where
// a second approver, a business-hours window or a rate limit lives, and
// swallowing its refusal would defeat the purpose of having one.
func TestARefusalFromTheSignerFailsTheCeremonyWithItsReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(externalSignResponse{
			Error: "second approver has not signed off",
		})
	}))
	defer server.Close()

	auth, err := newCeremonyAuthorizer(envFrom(map[string]string{
		"CEREMONY_AUTHORIZER_SIGNER_URL": server.URL,
	}), server.Client())
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = auth.authorize(context.Background(), "sign", "s-1", "ff00")
	if err == nil {
		t.Fatal("a refusal from the signer produced no error")
	}
	if !contains(err.Error(), "second approver has not signed off") {
		t.Fatalf("the signer's reason was lost: %v", err)
	}
}

func TestAnUnreachableSignerDoesNotSilentlyProduceAnUnauthorizedRequest(t *testing.T) {
	auth, err := newCeremonyAuthorizer(envFrom(map[string]string{
		// A port nothing is listening on.
		"CEREMONY_AUTHORIZER_SIGNER_URL": "http://127.0.0.1:1/sign",
	}), &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	body, sig, err := auth.authorize(context.Background(), "keygen", "c-1", "")
	if err == nil {
		t.Fatal("an unreachable signer produced no error")
	}
	if body != "" || sig != "" {
		t.Fatal("an unreachable signer produced an authorisation anyway")
	}
}

// The external signer wins when both are configured, because it is the
// stronger of the two.
func TestTheExternalSignerTakesPrecedenceOverALocalKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	auth, err := newCeremonyAuthorizer(envFrom(map[string]string{
		"CEREMONY_AUTHORIZER_SIGNER_URL": "http://signer.internal/sign",
		"CEREMONY_AUTHORIZER_KEY":        hex.EncodeToString(priv.Seed()),
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.signer.(*externalSigner); !ok {
		t.Fatalf("a local key won over an external signer: %T", auth.signer)
	}
}

func TestAnUnusableKeyIsAnErrorRatherThanASilentDowngrade(t *testing.T) {
	for name, value := range map[string]string{
		"not a key at all":         "this is not a key",
		"an ed25519 key too short": hex.EncodeToString([]byte("short")),
	} {
		if _, err := newCeremonyAuthorizer(envFrom(map[string]string{
			"CEREMONY_AUTHORIZER_KEY": value,
		}), nil); err == nil {
			t.Fatalf("%s was accepted as an authorising key", name)
		}
	}
	if _, err := newCeremonyAuthorizer(envFrom(map[string]string{
		"CEREMONY_AUTHORIZER_KEY_FILE": "/nonexistent/authorizer.pem",
	}), nil); err == nil {
		t.Fatal("a missing key file was accepted")
	}
}

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
