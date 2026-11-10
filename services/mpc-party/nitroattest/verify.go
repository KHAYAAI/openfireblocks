// Package nitroattest checks AWS Nitro Enclave attestation documents against
// a measurement policy, and checks the TLS certificates that signing parties
// running in enclaves present to each other.
//
// What it checks, in order:
//
//  1. The document is a COSE_Sign1 structure signed by a certificate that
//     chains to the AWS Nitro root (github.com/hf/nitrite does the parsing,
//     the chain and the signature).
//  2. The document is fresh: its timestamp is no older than Policy.MaxAge
//     and not in the future beyond a small clock skew.
//  3. PCR0 (the hash of the enclave image file) is one of the allowed
//     images, and is not all zeros. A debug-mode enclave reports zeroed PCRs
//     and lets the parent instance read its memory, so it is always refused.
//  4. PCR1 and PCR2 match, when the policy lists them.
//  5. PCR3 (the hash of the parent instance's IAM role) matches the value
//     pinned for the party named in the document's user data, when the
//     policy pins one, and PCR8 (the image-signing certificate) when listed.
//  6. The user data is this package's claims structure, with a purpose the
//     caller expects.
//
// What it does not check: that the enclave's code is what you think it is.
// PCR0 identifies an image; whether that image was built from a reviewed
// commit is a reproducible-build question, answered outside this package.
//
// This file is copied verbatim into services/temporal-worker so the
// orchestrator can verify the parties it calls (each Go service builds from
// its own directory). nitroattest_sync_test.go in that module fails if the
// copies differ.
package nitroattest

import (
	"bytes"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hf/nitrite"
)

// Purposes a document can be issued for. A document minted to bind a TLS key
// must not be accepted as, say, a KMS recipient proof, and the reverse.
const (
	PurposePartyTLS = "forge-mpc-party-tls"
	PurposeKMS      = "forge-mpc-party-kms"
)

// ClaimsVersion is the version of the Claims structure this package writes.
const ClaimsVersion = 1

// Claims is what an enclave puts in the user_data field of its attestation
// document. The enclave fills it; the verifier only trusts it because the
// document is signed by the Nitro hypervisor, which copies user_data in
// unchanged from the enclave that asked.
type Claims struct {
	Version int    `json:"v"`
	Purpose string `json:"purpose"`
	PartyID int    `json:"party_id"`
}

// Marshal encodes the claims for an attestation request.
func (c Claims) Marshal() []byte {
	b, _ := json.Marshal(c)
	return b
}

// Policy says which enclaves are trusted. Hex values are SHA-384 digests as
// `nitro-cli describe-eif` prints them.
type Policy struct {
	// ImagePCR0 lists the enclave images that may hold a share. Required.
	// Two entries during a rollout (old and new image), one otherwise.
	ImagePCR0 []string `json:"image_pcr0"`
	// PCR1 (kernel and boot ramdisk) and PCR2 (application), optional.
	PCR1 []string `json:"pcr1,omitempty"`
	PCR2 []string `json:"pcr2,omitempty"`
	// PCR8 (the certificate that signed the image file), optional.
	PCR8 []string `json:"pcr8,omitempty"`
	// Parties pins per-party values, keyed by party number. PCR3 is the
	// SHA-384 of the parent instance's IAM role ARN, so pinning it ties each
	// party to the AWS account and role that runs it.
	Parties map[string]PartyPolicy `json:"parties,omitempty"`
	// RequirePartyPin refuses any party that has no entry in Parties.
	RequirePartyPin bool `json:"require_party_pin,omitempty"`
	// MaxAgeSeconds is how old a document may be. Default 3 hours; the
	// enclave refreshes its certificate well inside that.
	MaxAgeSeconds int `json:"max_age_seconds,omitempty"`
}

// PartyPolicy pins measurements for one party.
type PartyPolicy struct {
	PCR3 string `json:"pcr3,omitempty"`
}

const (
	defaultMaxAge = 3 * time.Hour
	clockSkew     = 5 * time.Minute
)

// LoadPolicy reads a policy file and validates it.
func LoadPolicy(path string) (Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("attestation policy: %w", err)
	}
	return ParsePolicy(raw)
}

// ParsePolicy decodes and validates a policy, refusing unknown fields so a
// misspelt key fails loudly instead of silently pinning nothing.
func ParsePolicy(raw []byte) (Policy, error) {
	var p Policy
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("attestation policy: %w", err)
	}
	return p, p.Validate()
}

// Validate refuses a policy that would trust nothing in particular.
func (p Policy) Validate() error {
	if len(p.ImagePCR0) == 0 {
		return errors.New("attestation policy: image_pcr0 must list at least one enclave image")
	}
	check := func(field string, values []string) error {
		for _, v := range values {
			b, err := decodeDigest(v)
			if err != nil {
				return fmt.Errorf("attestation policy: %s: %w", field, err)
			}
			if allZero(b) {
				return fmt.Errorf("attestation policy: %s lists an all-zero value, which is what a debug-mode enclave reports", field)
			}
		}
		return nil
	}
	for field, values := range map[string][]string{"image_pcr0": p.ImagePCR0, "pcr1": p.PCR1, "pcr2": p.PCR2, "pcr8": p.PCR8} {
		if err := check(field, values); err != nil {
			return err
		}
	}
	for k, pp := range p.Parties {
		if n, err := strconv.Atoi(k); err != nil || n <= 0 {
			return fmt.Errorf("attestation policy: parties key %q is not a party number", k)
		}
		if pp.PCR3 != "" {
			if err := check("parties."+k+".pcr3", []string{pp.PCR3}); err != nil {
				return err
			}
		}
	}
	if p.MaxAgeSeconds < 0 {
		return errors.New("attestation policy: max_age_seconds must not be negative")
	}
	return nil
}

func (p Policy) maxAge() time.Duration {
	if p.MaxAgeSeconds > 0 {
		return time.Duration(p.MaxAgeSeconds) * time.Second
	}
	return defaultMaxAge
}

func decodeDigest(v string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(v))
	if err != nil {
		return nil, fmt.Errorf("%q is not hex", v)
	}
	if len(b) != 48 {
		return nil, fmt.Errorf("%q is %d bytes; a Nitro PCR is a 48-byte SHA-384", v, len(b))
	}
	return b, nil
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// Verifier checks documents against one policy.
type Verifier struct {
	Policy Policy
	// Roots overrides the AWS Nitro root. Tests only; production leaves it nil.
	Roots *x509.CertPool
	// Now overrides the clock. Tests only.
	Now func() time.Time
}

// Attested is what a verified document says.
type Attested struct {
	ModuleID  string
	Timestamp time.Time
	PCRs      map[uint][]byte
	PublicKey []byte
	Nonce     []byte
	Claims    Claims
}

// Error is returned for every refusal, so callers can tell a policy refusal
// from an I/O error.
type Error struct{ Reason string }

func (e *Error) Error() string { return "attestation refused: " + e.Reason }

func refuse(format string, args ...any) error {
	return &Error{Reason: fmt.Sprintf(format, args...)}
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Verify checks a raw attestation document and returns what it attests.
// wantPurpose is the purpose the caller is checking for.
func (v *Verifier) Verify(doc []byte, wantPurpose string) (att *Attested, err error) {
	if err := v.Policy.Validate(); err != nil {
		return nil, err
	}
	// nitrite panics on an ECDSA curve it does not know. The document comes
	// from the network, so a crafted one must be a refusal, not a crash.
	defer func() {
		if r := recover(); r != nil {
			att, err = nil, refuse("malformed attestation document (%v)", r)
		}
	}()
	now := v.now()
	res, verr := nitrite.Verify(doc, nitrite.VerifyOptions{Roots: v.Roots, CurrentTime: now})
	if verr != nil {
		return nil, refuse("document does not verify against the Nitro root: %v", verr)
	}
	if res == nil || res.Document == nil || !res.SignatureOK {
		return nil, refuse("document signature is not valid")
	}
	d := res.Document

	ts := time.UnixMilli(int64(d.Timestamp))
	if now.Sub(ts) > v.Policy.maxAge() {
		return nil, refuse("document is %s old; the policy allows %s", now.Sub(ts).Round(time.Second), v.Policy.maxAge())
	}
	if ts.Sub(now) > clockSkew {
		return nil, refuse("document is dated %s in the future", ts.Sub(now).Round(time.Second))
	}

	pcr := func(i uint) []byte { return d.PCRs[i] }
	if p0 := pcr(0); p0 == nil || allZero(p0) {
		return nil, refuse("PCR0 is all zeros: the enclave runs in debug mode, and the parent instance can read its memory")
	}
	if err := matchAny("PCR0 (enclave image)", pcr(0), v.Policy.ImagePCR0); err != nil {
		return nil, err
	}
	for _, c := range []struct {
		idx  uint
		name string
		vals []string
	}{{1, "PCR1 (kernel)", v.Policy.PCR1}, {2, "PCR2 (application)", v.Policy.PCR2}, {8, "PCR8 (signing certificate)", v.Policy.PCR8}} {
		if len(c.vals) > 0 {
			if err := matchAny(c.name, pcr(c.idx), c.vals); err != nil {
				return nil, err
			}
		}
	}

	var claims Claims
	dec := json.NewDecoder(bytes.NewReader(d.UserData))
	dec.DisallowUnknownFields()
	if len(d.UserData) == 0 || dec.Decode(&claims) != nil {
		return nil, refuse("user data is not a claims structure")
	}
	if claims.Version != ClaimsVersion {
		return nil, refuse("claims version %d, expected %d", claims.Version, ClaimsVersion)
	}
	if claims.Purpose != wantPurpose {
		return nil, refuse("document was issued for %q, not %q", claims.Purpose, wantPurpose)
	}
	if claims.PartyID <= 0 {
		return nil, refuse("claims carry no party number")
	}
	pp, pinned := v.Policy.Parties[strconv.Itoa(claims.PartyID)]
	if !pinned && v.Policy.RequirePartyPin {
		return nil, refuse("party %d has no entry in the policy, and the policy requires one", claims.PartyID)
	}
	if pp.PCR3 != "" {
		if err := matchAny(fmt.Sprintf("PCR3 (IAM role) for party %d", claims.PartyID), pcr(3), []string{pp.PCR3}); err != nil {
			return nil, err
		}
	}

	return &Attested{
		ModuleID:  d.ModuleID,
		Timestamp: ts,
		PCRs:      d.PCRs,
		PublicKey: d.PublicKey,
		Nonce:     d.Nonce,
		Claims:    claims,
	}, nil
}

func matchAny(name string, got []byte, allowed []string) error {
	if got == nil {
		return refuse("%s is missing from the document", name)
	}
	for _, a := range allowed {
		want, err := decodeDigest(a)
		if err == nil && bytes.Equal(want, got) {
			return nil
		}
	}
	sorted := append([]string(nil), allowed...)
	sort.Strings(sorted)
	return refuse("%s is %s, which the policy does not allow (allowed: %s)", name, hex.EncodeToString(got), strings.Join(sorted, ", "))
}
