package activities

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// The signing half of ceremony authorisation.
//
// services/mpc-party/authorizer.go is the verifying half: a party can be
// told to require a second signature, from a key the platform's own hosts
// cannot reach, before it will join a ceremony at all. The reason is the
// gap a threshold does not close. A threshold protects against a minority
// of parties being compromised; it says nothing about the case where the
// thing *instructing* the parties is compromised. An attacker holding this
// worker's credentials can ask every party, correctly and in the right
// format, to sign whatever they like. Every party behaves correctly. The
// money leaves.
//
// This file is what produces that signature. Until it existed, the
// verifying half could only be switched on by a deployment willing to have
// every ceremony refused.
//
// # Why there is no AWS SDK here
//
// The obvious implementation is to import the AWS KMS client and call
// Sign. It is also the wrong one, and it took writing it out to see why:
// it would put KMS credentials on this worker. The control is supposed to
// be "an attacker who owns every host in the deployment still cannot
// produce a ceremony, because the authorising key was never on any of
// them". A worker holding credentials that can make KMS sign arbitrary
// bytes holds the authorising key in every sense that matters, and the
// control degrades into an audit-log entry.
//
// So the KMS path is an *external signer*: a small service the customer
// runs, holding the cloud credentials, on infrastructure this platform
// does not administer. It receives the canonical bytes and returns a
// signature, and it is free to apply its own checks -- a second human, a
// rate limit, a business-hours window -- before it does. That is a better
// control than a co-signature, and it costs this codebase one HTTP call
// instead of a cloud SDK.
//
// The local-key signer exists alongside it for deployments that have not
// stood one up, and for tests. It is honestly weaker, and says so.

// authorizationRequest mirrors services/mpc-party's AuthorizationRequest.
//
// Duplicated rather than shared because these are separate Go modules and
// neither should depend on the other's internals. Duplication of a wire
// format is a drift risk, so it is pinned by golden vectors in
// ceremony_authorization_test.go which are byte-identical to the ones in
// the party's own test. If either side changes the encoding, both test
// suites go red.
type authorizationRequest struct {
	Operation   string `json:"operation"`
	CeremonyID  string `json:"ceremony_id"`
	MessageHash string `json:"message_hash,omitempty"`
	IssuedAt    int64  `json:"issued_at"`
}

// canonicalAuthorizationBytes is exactly what the party will verify.
//
// Field order is written out rather than produced by marshalling a map,
// for the reason the party's copy gives: two encoders that order keys
// differently produce two different messages for one request, and the
// failure is a signature the signer considers valid and the verifier does
// not -- intermittently, under load, on a key holding money.
func canonicalAuthorizationBytes(r authorizationRequest) []byte {
	return []byte(strings.Join([]string{
		"openfireblocks-authorization-v1",
		r.Operation,
		r.CeremonyID,
		r.MessageHash,
		fmt.Sprintf("%d", r.IssuedAt),
	}, "\n"))
}

// ceremonySigner produces an authorisation signature, or reports that none
// is configured.
type ceremonySigner interface {
	// sign returns the signature, hex encoded, over the canonical bytes.
	sign(ctx context.Context, canonical []byte) (string, error)
	describe() string
}

// ceremonyAuthorizer attaches authorisations to ceremony requests.
//
// A nil *ceremonyAuthorizer is valid and means "not configured", matching
// the party's own posture: the feature is off unless a deployment turns it
// on, because requiring external signing infrastructure to start would
// make the platform unusable for everyone who has not built it yet.
type ceremonyAuthorizer struct {
	signer ceremonySigner
}

// newCeremonyAuthorizer builds the signer from the environment.
//
// Precedence is external signer first. A deployment that has configured
// both has an external signer and a local key, and the external one is the
// stronger of the two; silently preferring the weaker would be the wrong
// way round.
func newCeremonyAuthorizer(getenv func(string) string, client *http.Client) (*ceremonyAuthorizer, error) {
	if url := strings.TrimSpace(getenv("CEREMONY_AUTHORIZER_SIGNER_URL")); url != "" {
		return &ceremonyAuthorizer{signer: &externalSigner{
			url:    url,
			token:  strings.TrimSpace(getenv("CEREMONY_AUTHORIZER_SIGNER_TOKEN")),
			client: client,
		}}, nil
	}

	keyRef := strings.TrimSpace(getenv("CEREMONY_AUTHORIZER_KEY_FILE"))
	inline := strings.TrimSpace(getenv("CEREMONY_AUTHORIZER_KEY"))
	if keyRef == "" && inline == "" {
		return nil, nil
	}

	raw := []byte(inline)
	if keyRef != "" {
		var err error
		raw, err = os.ReadFile(keyRef)
		if err != nil {
			return nil, fmt.Errorf("CEREMONY_AUTHORIZER_KEY_FILE: %w", err)
		}
	}
	signer, err := newLocalSigner(raw)
	if err != nil {
		return nil, err
	}
	return &ceremonyAuthorizer{signer: signer}, nil
}

// authorize returns the JSON authorisation and its signature, or two empty
// strings when authorisation is not configured.
//
// messageHash is required for a signing ceremony and empty otherwise.
// Without it, an authorisation for one transaction authorises any
// transaction in the same ceremony, which is most of the value gone.
func (c *ceremonyAuthorizer) authorize(ctx context.Context, operation, ceremonyID, messageHash string) (string, string, error) {
	if c == nil {
		return "", "", nil
	}
	req := authorizationRequest{
		Operation:   operation,
		CeremonyID:  ceremonyID,
		MessageHash: messageHash,
		// The party bounds how old an authorisation may be, so this is
		// what makes a captured one stop working.
		IssuedAt: time.Now().Unix(),
	}
	signature, err := c.signer.sign(ctx, canonicalAuthorizationBytes(req))
	if err != nil {
		return "", "", fmt.Errorf("signing the ceremony authorisation: %w", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", "", fmt.Errorf("marshalling the ceremony authorisation: %w", err)
	}
	return string(body), signature, nil
}

func (c *ceremonyAuthorizer) describe() string {
	if c == nil {
		return "none configured"
	}
	return c.signer.describe()
}

// ---------------------------------------------------------------------------
// External signer -- the KMS path
// ---------------------------------------------------------------------------

// externalSigner asks a service the customer runs to sign the canonical
// bytes.
//
// The bytes are sent rather than a digest, and the service decides how to
// hash them. Sending a digest would let a caller who can reach the signer
// obtain a signature over anything at all: the signer could no longer tell
// an authorisation from an arbitrary message, and a service whose job is
// to decide what to sign must be able to read what it is signing.
type externalSigner struct {
	url    string
	token  string
	client *http.Client
}

type externalSignRequest struct {
	// Base64 rather than hex, because the canonical form is text with
	// newlines and a signer's operator should be able to decode and read
	// it when deciding whether to approve.
	Canonical string `json:"canonical_b64"`
	Purpose   string `json:"purpose"`
}

type externalSignResponse struct {
	SignatureHex string `json:"signature_hex"`
	Error        string `json:"error"`
}

func (s *externalSigner) sign(ctx context.Context, canonical []byte) (string, error) {
	payload, err := json.Marshal(externalSignRequest{
		Canonical: base64.StdEncoding.EncodeToString(canonical),
		Purpose:   "openfireblocks-ceremony-authorization",
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	client := s.client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("reaching the authorisation signer at %s: %w", s.url, err)
	}
	defer resp.Body.Close()

	var out externalSignResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("the authorisation signer returned an unreadable response (HTTP %d): %w",
			resp.StatusCode, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// A refusal is a legitimate answer, not a fault. A signer that
		// declines -- outside business hours, second approver absent,
		// rate limited -- is the control working, and the message it
		// gives is the one an operator needs to see.
		if out.Error != "" {
			return "", fmt.Errorf("the authorisation signer refused: %s", out.Error)
		}
		return "", fmt.Errorf("the authorisation signer returned HTTP %d", resp.StatusCode)
	}
	if out.SignatureHex == "" {
		return "", fmt.Errorf("the authorisation signer returned no signature")
	}
	return out.SignatureHex, nil
}

func (s *externalSigner) describe() string {
	return fmt.Sprintf("external signer at %s", s.url)
}

// ---------------------------------------------------------------------------
// Local key
// ---------------------------------------------------------------------------

// localSigner holds an authorising key in this process.
//
// Weaker than the external signer, and the difference is worth stating
// rather than leaving to be inferred: an attacker with root on this host
// reads this key and can then authorise ceremonies at will, which is the
// exact attack the feature exists to stop. It is still worth having.
// Compromising the API gateway, the database or a party no longer yields
// ceremonies -- only compromising *this* host does, which is a smaller
// target and one that can be hardened separately.
//
// For the full property, the key belongs somewhere this deployment does
// not administer, and that is what CEREMONY_AUTHORIZER_SIGNER_URL is for.
type localSigner struct {
	ed25519 ed25519.PrivateKey
	p256    *ecdsa.PrivateKey
}

func newLocalSigner(raw []byte) (*localSigner, error) {
	// PEM first, since that is what a key file usually is.
	if block, _ := pem.Decode(raw); block != nil {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			if ecKey, ecErr := x509.ParseECPrivateKey(block.Bytes); ecErr == nil {
				key = ecKey
			} else {
				return nil, fmt.Errorf("the authorising key is not a usable PKCS#8 or EC private key: %w", err)
			}
		}
		switch k := key.(type) {
		case ed25519.PrivateKey:
			return &localSigner{ed25519: k}, nil
		case *ecdsa.PrivateKey:
			if k.Curve.Params().Name != "P-256" {
				return nil, fmt.Errorf("the authorising key is on %s; only P-256 is supported", k.Curve.Params().Name)
			}
			return &localSigner{p256: k}, nil
		default:
			return nil, fmt.Errorf("the authorising key is a %T; use Ed25519 or ECDSA P-256", key)
		}
	}

	// Otherwise a raw Ed25519 seed or private key, hex or base64, which is
	// the convenient form for a test or a Kubernetes Secret.
	trimmed := strings.TrimSpace(string(raw))
	decoded, err := hex.DecodeString(strings.TrimPrefix(trimmed, "0x"))
	if err != nil || len(decoded) == 0 {
		decoded, err = base64.StdEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("the authorising key is not PEM, hex or base64")
		}
	}
	switch len(decoded) {
	case ed25519.SeedSize:
		return &localSigner{ed25519: ed25519.NewKeyFromSeed(decoded)}, nil
	case ed25519.PrivateKeySize:
		return &localSigner{ed25519: ed25519.PrivateKey(decoded)}, nil
	default:
		return nil, fmt.Errorf("an ed25519 key is %d or %d bytes, got %d",
			ed25519.SeedSize, ed25519.PrivateKeySize, len(decoded))
	}
}

func (s *localSigner) sign(_ context.Context, canonical []byte) (string, error) {
	if s.ed25519 != nil {
		return hex.EncodeToString(ed25519.Sign(s.ed25519, canonical)), nil
	}
	// SHA-256 for P-256, matching what the party verifies and what a cloud
	// KMS signs with for this curve.
	digest := sha256.Sum256(canonical)
	sig, err := ecdsa.SignASN1(rand.Reader, s.p256, digest[:])
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sig), nil
}

func (s *localSigner) describe() string {
	if s.ed25519 != nil {
		return "local ed25519 key (weaker: an attacker with root on this host holds it)"
	}
	return "local ecdsa-p256 key (weaker: an attacker with root on this host holds it)"
}
