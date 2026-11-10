package nitroattest_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"openfireblocks.com/services/mpc-party/nitroattest"
	"openfireblocks.com/services/mpc-party/nitroattest/nitroattesttest"
)

func policy() nitroattest.Policy {
	return nitroattest.Policy{
		ImagePCR0: []string{nitroattesttest.Hex("image")},
		PCR1:      []string{nitroattesttest.Hex("kernel")},
		PCR2:      []string{nitroattesttest.Hex("application")},
		Parties:   map[string]nitroattest.PartyPolicy{"1": {PCR3: nitroattesttest.Hex("role")}},
	}
}

func claims(party int) []byte {
	return nitroattest.Claims{Version: 1, Purpose: nitroattest.PurposePartyTLS, PartyID: party}.Marshal()
}

func mustRefuse(t *testing.T, err error, contains string) {
	t.Helper()
	var ae *nitroattest.Error
	if err == nil {
		t.Fatalf("expected a refusal containing %q, got success", contains)
	}
	if !errors.As(err, &ae) && !strings.Contains(err.Error(), "attestation policy") {
		t.Fatalf("expected an attestation refusal, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("refusal %q does not mention %q", err, contains)
	}
}

func TestVerifyAcceptsAMatchingDocument(t *testing.T) {
	nsm := nitroattesttest.New()
	v := &nitroattest.Verifier{Policy: policy(), Roots: nsm.Roots()}
	doc, err := nsm.Attest(claims(1), []byte("nonce"), []byte("pk"))
	if err != nil {
		t.Fatal(err)
	}
	att, err := v.Verify(doc, nitroattest.PurposePartyTLS)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if att.Claims.PartyID != 1 || string(att.Nonce) != "nonce" || string(att.PublicKey) != "pk" {
		t.Fatalf("unexpected attested values: %+v", att)
	}
}

func TestVerifyRefusals(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(nsm *nitroattesttest.FakeNSM, p *nitroattest.Policy) []byte // returns user data
		foreign bool
		purpose string
		want    string
	}{
		{name: "debug mode (zero PCR0)", want: "debug mode", mutate: func(n *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			for i := range n.PCRs {
				n.PCRs[i] = make([]byte, 48)
			}
			return claims(1)
		}},
		{name: "rebuilt image", want: "PCR0", mutate: func(n *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			n.PCRs[0] = nitroattesttest.Digest("image with one byte changed")
			return claims(1)
		}},
		{name: "different kernel", want: "PCR1", mutate: func(n *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			n.PCRs[1] = nitroattesttest.Digest("other kernel")
			return claims(1)
		}},
		{name: "different application", want: "PCR2", mutate: func(n *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			n.PCRs[2] = nitroattesttest.Digest("other app")
			return claims(1)
		}},
		{name: "party run under another IAM role", want: "PCR3", mutate: func(n *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			n.PCRs[3] = nitroattesttest.Digest("attacker role")
			return claims(1)
		}},
		{name: "unpinned party when pins are required", want: "requires one", mutate: func(_ *nitroattesttest.FakeNSM, p *nitroattest.Policy) []byte {
			p.RequirePartyPin = true
			return claims(2)
		}},
		// Two hours old against a one-hour policy: the signing certificate is
		// still valid, so this is the policy's age check refusing, not x509.
		{name: "document too old", want: "old", mutate: func(n *nitroattesttest.FakeNSM, p *nitroattest.Policy) []byte {
			p.MaxAgeSeconds = 3600
			n.Now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
			return claims(1)
		}},
		{name: "document from the future", want: "future", mutate: func(n *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			n.Now = func() time.Time { return time.Now().Add(time.Hour) }
			return claims(1)
		}},
		{name: "wrong purpose", purpose: nitroattest.PurposeKMS, want: "issued for", mutate: func(_ *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			return claims(1)
		}},
		{name: "no claims", want: "claims", mutate: func(_ *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			return nil
		}},
		{name: "claims with extra fields", want: "claims", mutate: func(_ *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			return []byte(`{"v":1,"purpose":"forge-mpc-party-tls","party_id":1,"admin":true}`)
		}},
		{name: "claims version", want: "version", mutate: func(_ *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			return []byte(`{"v":2,"purpose":"forge-mpc-party-tls","party_id":1}`)
		}},
		{name: "untrusted root", foreign: true, want: "Nitro root", mutate: func(_ *nitroattesttest.FakeNSM, _ *nitroattest.Policy) []byte {
			return claims(1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nsm := nitroattesttest.New()
			p := policy()
			ud := tc.mutate(nsm, &p)
			var doc []byte
			var err error
			if tc.foreign {
				doc, err = nsm.AttestWithForeignRoot(ud, nil, nil)
			} else {
				doc, err = nsm.Attest(ud, nil, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			purpose := tc.purpose
			if purpose == "" {
				purpose = nitroattest.PurposePartyTLS
			}
			_, err = (&nitroattest.Verifier{Policy: p, Roots: nsm.Roots()}).Verify(doc, purpose)
			mustRefuse(t, err, tc.want)
		})
	}
}

func TestVerifyRefusesTamperedAndGarbageDocuments(t *testing.T) {
	nsm := nitroattesttest.New()
	v := &nitroattest.Verifier{Policy: policy(), Roots: nsm.Roots()}
	doc, _ := nsm.Attest(claims(1), nil, nil)
	for i := 0; i < len(doc); i += 7 {
		bad := append([]byte(nil), doc...)
		bad[i] ^= 0x01
		if _, err := v.Verify(bad, nitroattest.PurposePartyTLS); err == nil {
			t.Fatalf("a document with byte %d flipped verified", i)
		}
	}
	for _, g := range [][]byte{nil, {}, []byte("not cbor"), {0x84, 0x40, 0xa0, 0x40, 0x40}} {
		if _, err := v.Verify(g, nitroattest.PurposePartyTLS); err == nil {
			t.Fatalf("garbage %x verified", g)
		}
	}
}

func TestPolicyValidation(t *testing.T) {
	zero := strings.Repeat("00", 48)
	bad := []string{
		`{}`,
		`{"image_pcr0":["abcd"]}`,
		`{"image_pcr0":["` + zero + `"]}`,
		`{"image_pcr0":["` + nitroattesttest.Hex("image") + `"],"pcr2":["` + zero + `"]}`,
		`{"image_pcr0":["` + nitroattesttest.Hex("image") + `"],"parties":{"x":{}}}`,
		`{"image_pcr0":["` + nitroattesttest.Hex("image") + `"],"image_pcr_0":[]}`,
		`{"image_pcr0":["` + nitroattesttest.Hex("image") + `"],"max_age_seconds":-1}`,
	}
	for _, b := range bad {
		if _, err := nitroattest.ParsePolicy([]byte(b)); err == nil {
			t.Errorf("policy %s was accepted", b)
		}
	}
	good := `{"image_pcr0":["` + nitroattesttest.Hex("image") + `"],"parties":{"1":{"pcr3":"` + nitroattesttest.Hex("role") + `"}},"require_party_pin":true}`
	if _, err := nitroattest.ParsePolicy([]byte(good)); err != nil {
		t.Fatalf("good policy refused: %v", err)
	}
}

// certFor builds a party certificate the way the enclave does, so the
// certificate checks can be exercised here without importing the enclave.
func certFor(t *testing.T, nsm *nitroattesttest.FakeNSM, cn string, attestedParty int, bindOtherKey bool) *x509.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	spki, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if bindOtherKey {
		other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		spki, _ = x509.MarshalPKIXPublicKey(&other.PublicKey)
	}
	doc, err := nsm.Attest(claims(attestedParty), nil, spki)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(7),
		Subject:         pkix.Name{CommonName: cn},
		NotBefore:       time.Now().Add(-time.Minute),
		NotAfter:        time.Now().Add(time.Hour),
		ExtraExtensions: []pkix.Extension{{Id: nitroattest.OIDAttestationExtension, Value: doc}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func TestVerifyCertificate(t *testing.T) {
	nsm := nitroattesttest.New()
	v := &nitroattest.Verifier{Policy: policy(), Roots: nsm.Roots()}

	if _, err := v.VerifyCertificate(certFor(t, nsm, "party-1.internal", 1, false)); err != nil {
		t.Fatalf("good certificate refused: %v", err)
	}
	mustRefuse(t, func() error { _, e := v.VerifyCertificate(certFor(t, nsm, "party-2.internal", 1, false)); return e }(), "attested party 1")
	mustRefuse(t, func() error { _, e := v.VerifyCertificate(certFor(t, nsm, "party-1.internal", 1, true)); return e }(), "different public key")
	mustRefuse(t, func() error { _, e := v.VerifyCertificate(certFor(t, nsm, "vault.internal", 1, false)); return e }(), "does not name a party")

	plainKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "party-1.internal"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &plainKey.PublicKey, plainKey)
	plain, _ := x509.ParseCertificate(der)
	mustRefuse(t, func() error { _, e := v.VerifyCertificate(plain); return e }(), "no attestation document")

	expired := &nitroattest.Verifier{Policy: policy(), Roots: nsm.Roots(), Now: func() time.Time { return time.Now().Add(2 * time.Hour) }}
	mustRefuse(t, func() error {
		_, e := expired.VerifyCertificate(certFor(t, nsm, "party-1.internal", 1, false))
		return e
	}(), "validity period")
}

func TestPartyFromName(t *testing.T) {
	for name, want := range map[string]int{"party-1.internal": 1, "party-12": 12, "party-0.internal": 0, "xparty-1": 0, "party-.internal": 0} {
		got, ok := nitroattest.PartyFromName(name)
		if got != want || ok != (want > 0) {
			t.Errorf("PartyFromName(%q) = %d,%v want %d", name, got, ok, want)
		}
	}
}
