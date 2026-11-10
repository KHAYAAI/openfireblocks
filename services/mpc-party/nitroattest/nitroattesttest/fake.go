//go:build !nitro

// Package nitroattesttest mints attestation documents signed by a throwaway
// root, in the exact format the Nitro hypervisor uses, so the verifier and
// everything built on it can be tested without Nitro hardware.
//
// A document from here never verifies against the real AWS Nitro root, so
// it cannot be used to impersonate an enclave. The package still refuses to
// compile into a binary built with the nitro tag, so an enclave image can
// never contain a software attester.
package nitroattesttest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"sync"
	"time"

	"github.com/fxamacker/cbor/v2"
)

// FakeNSM stands in for the Nitro Secure Module.
type FakeNSM struct {
	mu       sync.Mutex
	rootKey  *ecdsa.PrivateKey
	root     *x509.Certificate
	PCRs     map[uint][]byte
	ModuleID string
	// Now overrides the timestamp written into documents.
	Now func() time.Time
	// Calls counts Attest calls.
	Calls int
}

// New returns a fake with PCR0, PCR1, PCR2 and PCR3 set to distinct
// non-zero values and the rest zero, as a production enclave reports.
func New() *FakeNSM {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake.nitro-enclaves"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	root, _ := x509.ParseCertificate(der)
	f := &FakeNSM{rootKey: key, root: root, ModuleID: "i-0fake-enc0fake", PCRs: map[uint][]byte{}}
	for i := uint(0); i < 16; i++ {
		f.PCRs[i] = make([]byte, 48)
	}
	f.PCRs[0] = Digest("image")
	f.PCRs[1] = Digest("kernel")
	f.PCRs[2] = Digest("application")
	f.PCRs[3] = Digest("role")
	return f
}

// Digest is a stable fake 48-byte PCR value for a label.
func Digest(label string) []byte {
	h := sha512.Sum384([]byte(label))
	return h[:]
}

// Hex is Digest as hex, the form a policy file uses.
func Hex(label string) string { return hex.EncodeToString(Digest(label)) }

// Roots is the pool a Verifier must use to accept this fake's documents.
func (f *FakeNSM) Roots() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(f.root)
	return p
}

// Attest mints a document, as the NSM Attestation request does.
func (f *FakeNSM) Attest(userData, nonce, publicKey []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	now := time.Now()
	if f.Now != nil {
		now = f.Now()
	}
	return f.sign(now, userData, nonce, publicKey, f.rootKey, f.root)
}

// AttestWithForeignRoot mints a document signed by a root the verifier does
// not trust, for negative tests.
func (f *FakeNSM) AttestWithForeignRoot(userData, nonce, publicKey []byte) ([]byte, error) {
	other := New()
	return f.sign(time.Now(), userData, nonce, publicKey, other.rootKey, other.root)
}

func (f *FakeNSM) sign(now time.Time, userData, nonce, publicKey []byte, caKey *ecdsa.PrivateKey, ca *x509.Certificate) ([]byte, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber:       serial,
		Subject:            pkix.Name{CommonName: f.ModuleID},
		NotBefore:          now.Add(-time.Hour),
		NotAfter:           now.Add(3 * time.Hour),
		SignatureAlgorithm: x509.ECDSAWithSHA384,
		KeyUsage:           x509.KeyUsageDigitalSignature,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}

	pcrs := map[uint][]byte{}
	for k, v := range f.PCRs {
		pcrs[k] = append([]byte(nil), v...)
	}
	payload := map[string]any{
		"module_id":   f.ModuleID,
		"timestamp":   uint64(now.UnixMilli()),
		"digest":      "SHA384",
		"pcrs":        pcrs,
		"certificate": leafDER,
		"cabundle":    [][]byte{ca.Raw},
		"public_key":  nilIfEmpty(publicKey),
		"user_data":   nilIfEmpty(userData),
		"nonce":       nilIfEmpty(nonce),
	}
	payloadBytes, err := cbor.Marshal(payload)
	if err != nil {
		return nil, err
	}
	protected, err := cbor.Marshal(map[int]int{1: -35}) // alg: ES384
	if err != nil {
		return nil, err
	}
	sigStruct, err := cbor.Marshal([]any{"Signature1", protected, []byte{}, payloadBytes})
	if err != nil {
		return nil, err
	}
	digest := sha512.Sum384(sigStruct)
	r, s, err := ecdsa.Sign(rand.Reader, leafKey, digest[:])
	if err != nil {
		return nil, err
	}
	sig := make([]byte, 96)
	r.FillBytes(sig[:48])
	s.FillBytes(sig[48:])
	return cbor.Marshal([]any{protected, map[int]any{}, payloadBytes, sig})
}

func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
