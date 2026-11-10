//go:build !nitro

// Package enclavetest provides a stand-in for AWS KMS that enforces an
// attestation key policy and answers in KMS's Recipient format (CMS
// enveloped data), for tests that run without AWS. Excluded from nitro builds.
package enclavetest

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/kms"

	"openfireblocks.com/services/mpc-party/nitroattest"
)

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func aad(ec map[string]string) []byte {
	keys := make([]string, 0, len(ec))
	for k := range ec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%d:%s=%d:%s|", len(k), k, len(ec[k]), ec[k])
	}
	return []byte(b.String())
}

// FakeKMS behaves like AWS KMS with a key policy that allows Decrypt and
// GenerateDataKey only with a Recipient whose attestation passes policy
// (the kms:RecipientAttestation:ImageSha384 condition), and returns the data
// key only as CMS enveloped data for the attested RSA key.
type FakeKMS struct {
	master   []byte
	verifier *nitroattest.Verifier
	// LeakPlaintext makes the fake return Plaintext as well, as a misbehaving
	// or impersonated endpoint might.
	LeakPlaintext bool
	// DropRecipientBlob omits CiphertextForRecipient.
	DropRecipientBlob bool
	Calls             int
	mu                sync.Mutex
}

// NewFakeKMS returns a fake whose key policy is the verifier's policy.
func NewFakeKMS(v *nitroattest.Verifier) *FakeKMS {
	m := make([]byte, 32)
	_, _ = rand.Read(m)
	return &FakeKMS{master: m, verifier: v}
}

var ErrAccessDenied = errors.New("AccessDeniedException: the key policy does not allow this request")

func (k *FakeKMS) checkRecipient(doc []byte) (*rsa.PublicKey, error) {
	if doc == nil {
		// The policy condition is absent from the request: refused.
		return nil, ErrAccessDenied
	}
	att, err := k.verifier.Verify(doc, nitroattest.PurposeKMS)
	if err != nil {
		return nil, ErrAccessDenied
	}
	pub, err := x509.ParsePKIXPublicKey(att.PublicKey)
	if err != nil {
		return nil, errors.New("ValidationException: bad recipient public key")
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("ValidationException: recipient key is not RSA")
	}
	return rsaPub, nil
}

func (k *FakeKMS) blob(dk []byte, ec map[string]string) []byte {
	g, _ := gcm(k.master)
	n := make([]byte, g.NonceSize())
	_, _ = rand.Read(n)
	return append(n, g.Seal(nil, n, dk, aad(ec))...)
}

func (k *FakeKMS) unblob(b []byte, ec map[string]string) ([]byte, error) {
	g, _ := gcm(k.master)
	if len(b) < g.NonceSize() {
		return nil, errors.New("InvalidCiphertextException")
	}
	dk, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], aad(ec))
	if err != nil {
		return nil, errors.New("InvalidCiphertextException")
	}
	return dk, nil
}

func (k *FakeKMS) GenerateDataKey(_ context.Context, in *kms.GenerateDataKeyInput, _ ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error) {
	k.mu.Lock()
	k.Calls++
	k.mu.Unlock()
	var doc []byte
	if in.Recipient != nil {
		doc = in.Recipient.AttestationDocument
	}
	pub, err := k.checkRecipient(doc)
	if err != nil {
		return nil, err
	}
	dk := make([]byte, 32)
	_, _ = rand.Read(dk)
	out := &kms.GenerateDataKeyOutput{CiphertextBlob: k.blob(dk, in.EncryptionContext)}
	if !k.DropRecipientBlob {
		out.CiphertextForRecipient = EnvelopeFor(pub, dk)
	}
	if k.LeakPlaintext {
		out.Plaintext = dk
	}
	return out, nil
}

func (k *FakeKMS) Decrypt(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	k.mu.Lock()
	k.Calls++
	k.mu.Unlock()
	var doc []byte
	if in.Recipient != nil {
		doc = in.Recipient.AttestationDocument
	}
	pub, err := k.checkRecipient(doc)
	if err != nil {
		return nil, err
	}
	dk, err := k.unblob(in.CiphertextBlob, in.EncryptionContext)
	if err != nil {
		return nil, err
	}
	out := &kms.DecryptOutput{}
	if !k.DropRecipientBlob {
		out.CiphertextForRecipient = EnvelopeFor(pub, dk)
	}
	if k.LeakPlaintext {
		out.Plaintext = dk
	}
	return out, nil
}

// CMS EnvelopedData as KMS produces it: RSAES-OAEP(SHA-256) key transport,
// AES-256-CBC content encryption.
type tContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     tEnvelopedData `asn1:"explicit,tag:0"`
}
type tEnvelopedData struct {
	Version              int
	RecipientInfos       []tKeyTrans `asn1:"set"`
	EncryptedContentInfo tEncryptedContentInfo
}
type tKeyTrans struct {
	Version                int
	RecipientIdentifier    []byte `asn1:"tag:0"`
	KeyEncryptionAlgorithm pkix.AlgorithmIdentifier
	EncryptedKey           []byte
}
type tEncryptedContentInfo struct {
	ContentType                asn1.ObjectIdentifier
	ContentEncryptionAlgorithm pkix.AlgorithmIdentifier
	EncryptedContent           asn1.RawValue
}

func EnvelopeFor(pub *rsa.PublicKey, plaintext []byte) []byte {
	cek := make([]byte, 32)
	iv := make([]byte, 16)
	_, _ = rand.Read(cek)
	_, _ = rand.Read(iv)
	pad := 16 - len(plaintext)%16
	padded := append(append([]byte(nil), plaintext...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(cek)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	ek, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, cek, nil)
	if err != nil {
		panic(err)
	}
	ivDER, _ := asn1.Marshal(iv)
	der, err := asn1.Marshal(tContentInfo{
		ContentType: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 3},
		Content: tEnvelopedData{
			Version: 2,
			RecipientInfos: []tKeyTrans{{
				Version:                2,
				RecipientIdentifier:    []byte("ski"),
				KeyEncryptionAlgorithm: pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 7}},
				EncryptedKey:           ek,
			}},
			EncryptedContentInfo: tEncryptedContentInfo{
				ContentType:                asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1},
				ContentEncryptionAlgorithm: pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}, Parameters: asn1.RawValue{FullBytes: ivDER}},
				EncryptedContent:           asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, Bytes: ct},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return der
}
