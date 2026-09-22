package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// A second signature that has to be present before a party will take part
// in a ceremony.
//
// The problem it addresses is the one threshold signing does not. A
// threshold protects against a minority of *parties* being compromised.
// It says nothing about the case where the thing instructing the parties
// is compromised: an attacker with the orchestrator's credentials can ask
// every party, honestly and in the correct format, to sign whatever they
// like. Each party behaves correctly. The money leaves.
//
// Fireblocks answers this with hardware -- shares live in SGX enclaves, so
// a host compromise does not yield a share. Metaco and Taurus answer it
// with HSMs. Both are real answers and both are months of work.
//
// This is the cheaper one, taken from fystack/mpcium: require every
// ceremony request to carry a signature from an authorising key that lives
// somewhere the platform's own hosts cannot reach -- a cloud KMS, an
// offline key, a separate approval service. A party checks that signature
// before it will contribute a share. Compromising every host in the
// deployment still does not produce a ceremony, because the authorising
// key was never on any of them.
//
// What it is not: a replacement for the policy engine. Policy decides
// whether a transaction is allowed; this decides whether the request to
// sign it is authentic. A deployment wants both, and they fail
// independently -- which is the point.
//
// STATE OF COMPLETION, stated plainly because the gap is not obvious from
// this file. The verifying half is here and tested. The *signing* half is
// not: services/temporal-worker/activities/real_tss.go drives every
// ceremony and does not yet attach an authorisation, so setting
// CEREMONY_AUTHORIZER_PUBKEY today would make this party refuse every
// ceremony the platform starts. That is why it is off unless configured,
// and why "off" is reported loudly rather than silently.
//
// Finishing it means giving the worker a KMS client that signs
// CanonicalAuthorizationBytes and sends the result in the `authorization`
// and `authorization_signature` fields. The exported helpers here exist so
// that when it is written, both sides compute the same bytes.

// AuthorizerAlgorithm is how an authorising key signs.
//
// Ed25519 and ECDSA P-256 because those are what a cloud KMS will actually
// issue: AWS KMS and GCP KMS both offer P-256 signing keys, and Ed25519 is
// what an offline or HSM-backed key most often is. secp256k1 is
// deliberately absent -- it is the curve the platform's own keys use, and
// an authorising key drawn from the same family invites someone to reuse
// one for the other.
type AuthorizerAlgorithm string

const (
	AuthorizerEd25519 AuthorizerAlgorithm = "ed25519"
	AuthorizerP256    AuthorizerAlgorithm = "ecdsa-p256"
)

// AuthorizationRequest is what an authorising key signs over.
//
// Every field that changes the meaning of the ceremony is in here. A
// signature over less than this is a signature an attacker can replay
// against a different ceremony: sign "party 1 may take part in a keygen"
// and it authorises every keygen, forever.
type AuthorizationRequest struct {
	// What is being authorised: "keygen", "sign" or "reshare".
	Operation string `json:"operation"`
	// The ceremony this authorisation is good for, and nothing else.
	CeremonyID string `json:"ceremony_id"`
	// For a signing ceremony, the digest being signed, hex encoded.
	// Without it an authorisation for one transaction authorises any
	// transaction in the same ceremony.
	MessageHash string `json:"message_hash,omitempty"`
	// Seconds since the epoch. Bounds how long a captured authorisation
	// stays useful.
	IssuedAt int64 `json:"issued_at"`
}

// canonical renders the request as the exact bytes that get signed.
//
// Field order is fixed by writing them out rather than by marshalling a
// map, because two JSON encoders that order keys differently produce two
// different messages for one request -- and the verifier would then reject
// signatures the signer considers valid, or worse, accept a request whose
// re-encoding dropped a field.
func (r AuthorizationRequest) canonical() []byte {
	return []byte(strings.Join([]string{
		"openfireblocks-authorization-v1",
		r.Operation,
		r.CeremonyID,
		r.MessageHash,
		fmt.Sprintf("%d", r.IssuedAt),
	}, "\n"))
}

// Authorizer verifies authorisation signatures against a configured public
// key.
type Authorizer struct {
	algorithm AuthorizerAlgorithm
	ed25519   ed25519.PublicKey
	p256      *ecdsa.PublicKey
	maxAge    time.Duration
}

// AuthorizerFromEnv builds the authorizer this party will enforce, or
// reports that none is configured.
//
// Off unless configured, and that is a deliberate asymmetry with the rest
// of this codebase's fail-closed defaults. An authorising key is a piece of
// external infrastructure -- a KMS, an approval service -- and a platform
// that refused to start without one would be unusable for every deployment
// that has not stood one up yet. The compensating control is that the
// state is reported, loudly, by GET /info and on the dashboard, so
// "authorisation is off" is visible rather than assumed.
func AuthorizerFromEnv(getenv func(string) string) (*Authorizer, error) {
	raw := strings.TrimSpace(getenv("CEREMONY_AUTHORIZER_PUBKEY"))
	if raw == "" {
		return nil, nil
	}

	algorithm := AuthorizerAlgorithm(strings.TrimSpace(getenv("CEREMONY_AUTHORIZER_ALG")))
	if algorithm == "" {
		algorithm = AuthorizerEd25519
	}

	maxAge := 5 * time.Minute
	if v := strings.TrimSpace(getenv("CEREMONY_AUTHORIZER_MAX_AGE_SECONDS")); v != "" {
		var seconds int
		if _, err := fmt.Sscanf(v, "%d", &seconds); err == nil && seconds > 0 {
			maxAge = time.Duration(seconds) * time.Second
		}
	}

	a := &Authorizer{algorithm: algorithm, maxAge: maxAge}

	switch algorithm {
	case AuthorizerEd25519:
		key, err := decodeKeyBytes(raw)
		if err != nil {
			return nil, fmt.Errorf("CEREMONY_AUTHORIZER_PUBKEY: %w", err)
		}
		if len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("an ed25519 public key is %d bytes, got %d",
				ed25519.PublicKeySize, len(key))
		}
		a.ed25519 = ed25519.PublicKey(key)

	case AuthorizerP256:
		// DER/SPKI, which is what a cloud KMS hands back when you ask for
		// a public key.
		der, err := decodeKeyBytes(raw)
		if err != nil {
			return nil, fmt.Errorf("CEREMONY_AUTHORIZER_PUBKEY: %w", err)
		}
		parsed, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			return nil, fmt.Errorf("CEREMONY_AUTHORIZER_PUBKEY is not a DER-encoded public key: %w", err)
		}
		pub, ok := parsed.(*ecdsa.PublicKey)
		if !ok || pub.Curve.Params().Name != "P-256" {
			return nil, fmt.Errorf("CEREMONY_AUTHORIZER_PUBKEY is not a P-256 key")
		}
		a.p256 = pub

	default:
		return nil, fmt.Errorf("unknown CEREMONY_AUTHORIZER_ALG %q; use ed25519 or ecdsa-p256", algorithm)
	}
	return a, nil
}

func decodeKeyBytes(s string) ([]byte, error) {
	if b, err := hex.DecodeString(strings.TrimPrefix(s, "0x")); err == nil && len(b) > 0 {
		return b, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("value is neither hex nor base64")
	}
	return b, nil
}

// Verify checks an authorisation, or explains why it is not one.
func (a *Authorizer) Verify(req AuthorizationRequest, signatureHex string) error {
	if a == nil {
		return nil // not configured; see AuthorizerFromEnv
	}
	if req.Operation == "" || req.CeremonyID == "" {
		return fmt.Errorf("an authorisation must name an operation and a ceremony")
	}

	// Bounded age, checked in both directions. A stale authorisation is a
	// captured one being replayed; one from the future is a clock the
	// verifier cannot reason about, and accepting it would let a signer
	// with a fast clock mint authorisations valid long past their window.
	age := time.Since(time.Unix(req.IssuedAt, 0))
	if age > a.maxAge {
		return fmt.Errorf("the authorisation is %s old; the limit is %s", age.Truncate(time.Second), a.maxAge)
	}
	if age < -30*time.Second {
		return fmt.Errorf("the authorisation is dated %s in the future", (-age).Truncate(time.Second))
	}

	sig, err := hex.DecodeString(strings.TrimPrefix(signatureHex, "0x"))
	if err != nil {
		return fmt.Errorf("the authorisation signature is not hex")
	}

	message := req.canonical()
	switch a.algorithm {
	case AuthorizerEd25519:
		if !ed25519.Verify(a.ed25519, message, sig) {
			return fmt.Errorf("the authorisation signature does not verify against the configured authorising key")
		}
	case AuthorizerP256:
		// KMS signs the digest; ECDSA over P-256 is defined over SHA-256
		// for this curve.
		digest := sha256.Sum256(message)
		if !ecdsa.VerifyASN1(a.p256, digest[:], sig) {
			return fmt.Errorf("the authorisation signature does not verify against the configured authorising key")
		}
	default:
		return fmt.Errorf("unknown authoriser algorithm %q", a.algorithm)
	}
	return nil
}

// Describe reports the authorisation posture, for GET /info.
//
// A deployment running without an authorising key should be able to see
// that at a glance rather than infer it from the absence of an error.
func (a *Authorizer) Describe() map[string]interface{} {
	if a == nil {
		return map[string]interface{}{
			"enabled": false,
			"note": "No ceremony authoriser is configured. A compromised orchestrator can " +
				"ask every party to sign, and every party will comply. Set " +
				"CEREMONY_AUTHORIZER_PUBKEY to require a second signature from a key " +
				"the platform's own hosts cannot reach.",
		}
	}
	return map[string]interface{}{
		"enabled":         true,
		"algorithm":       string(a.algorithm),
		"max_age_seconds": int(a.maxAge.Seconds()),
	}
}

// MarshalAuthorization renders a request as the JSON an orchestrator sends
// alongside a signature. Exported so the signing side and the verifying
// side cannot drift.
func MarshalAuthorization(req AuthorizationRequest) ([]byte, error) {
	return json.Marshal(req)
}

// CanonicalAuthorizationBytes is what an authorising key must sign.
//
// Exported for the same reason: an orchestrator building the message to
// send to a KMS must produce exactly the bytes a party will verify, and
// the only safe way to guarantee that is for both to call one function.
func CanonicalAuthorizationBytes(req AuthorizationRequest) []byte {
	return req.canonical()
}

// requireAuthorization is the check every ceremony-start handler runs.
//
// Placed on the start of a ceremony rather than on each relayed message:
// a party that has agreed to take part has already contributed, and
// re-checking mid-protocol would reject its own committee's traffic. The
// decision point is "will I join this ceremony at all".
func (ps *PartyServer) requireAuthorization(op, ceremonyID, messageHash, authJSON, signature string) error {
	if ps.authorizer == nil {
		return nil
	}
	if authJSON == "" || signature == "" {
		return fmt.Errorf("this party requires an authorisation signature for every ceremony; " +
			"none was supplied")
	}
	var req AuthorizationRequest
	if err := json.Unmarshal([]byte(authJSON), &req); err != nil {
		return fmt.Errorf("the authorisation is not readable: %w", err)
	}
	// The authorisation must be for *this* request, not merely valid.
	// Verifying the signature and then acting on different parameters is
	// the confused-deputy version of having no check at all.
	if req.Operation != op {
		return fmt.Errorf("the authorisation is for %q, this is a %q", req.Operation, op)
	}
	if req.CeremonyID != ceremonyID {
		return fmt.Errorf("the authorisation is for ceremony %s, this is %s", req.CeremonyID, ceremonyID)
	}
	if messageHash != "" && !strings.EqualFold(req.MessageHash, messageHash) {
		return fmt.Errorf("the authorisation is for a different message")
	}
	return ps.authorizer.Verify(req, signature)
}
