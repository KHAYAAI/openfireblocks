package enclave

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
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"

	"openfireblocks.com/services/mpc-party/nitroattest"
	"openfireblocks.com/services/mpc-party/nitroattest/nitroattesttest"
)

// fakeKMS behaves like AWS KMS with a key policy that allows Decrypt and
// GenerateDataKey only with a Recipient whose attestation passes policy
// (the kms:RecipientAttestation:ImageSha384 condition), and returns the data
// key only as CMS enveloped data for the attested RSA key.
type fakeKMS struct {
	master   []byte
	verifier *nitroattest.Verifier
	// leakPlaintext makes the fake return Plaintext as well, as a misbehaving
	// or impersonated endpoint might.
	leakPlaintext bool
	// dropRecipientBlob omits CiphertextForRecipient.
	dropRecipientBlob bool
	calls             int
}

func newFakeKMS(v *nitroattest.Verifier) *fakeKMS {
	m := make([]byte, 32)
	_, _ = rand.Read(m)
	return &fakeKMS{master: m, verifier: v}
}

var errAccessDenied = errors.New("AccessDeniedException: the key policy does not allow this request")


func (k *fakeKMS) checkRecipient(doc []byte) (*rsa.PublicKey, error) {
	if doc == nil {
		// The policy condition is absent from the request: refused.
		return nil, errAccessDenied
	}
	att, err := k.verifier.Verify(doc, nitroattest.PurposeKMS)
	if err != nil {
		return nil, errAccessDenied
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

func (k *fakeKMS) blob(dk []byte, ec map[string]string) []byte {
	g, _ := gcmFor(k.master)
	n := make([]byte, g.NonceSize())
	_, _ = rand.Read(n)
	return append(n, g.Seal(nil, n, dk, contextAAD(ec))...)
}

func (k *fakeKMS) unblob(b []byte, ec map[string]string) ([]byte, error) {
	g, _ := gcmFor(k.master)
	if len(b) < g.NonceSize() {
		return nil, errors.New("InvalidCiphertextException")
	}
	dk, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], contextAAD(ec))
	if err != nil {
		return nil, errors.New("InvalidCiphertextException")
	}
	return dk, nil
}

func (k *fakeKMS) GenerateDataKey(_ context.Context, in *kms.GenerateDataKeyInput, _ ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error) {
	k.calls++
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
	if !k.dropRecipientBlob {
		out.CiphertextForRecipient = envelopeFor(pub, dk)
	}
	if k.leakPlaintext {
		out.Plaintext = dk
	}
	return out, nil
}

func (k *fakeKMS) Decrypt(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	k.calls++
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
	if !k.dropRecipientBlob {
		out.CiphertextForRecipient = envelopeFor(pub, dk)
	}
	if k.leakPlaintext {
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

func envelopeFor(pub *rsa.PublicKey, plaintext []byte) []byte {
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

func kmsFixture() (*nitroattesttest.FakeNSM, *fakeKMS) {
	nsm := nitroattesttest.New()
	v := &nitroattest.Verifier{Policy: nitroattest.Policy{ImagePCR0: []string{nitroattesttest.Hex("image")}}, Roots: nsm.Roots()}
	return nsm, newFakeKMS(v)
}

func TestSealOpenRoundTrip(t *testing.T) {
	nsm, k := kmsFixture()
	s := &Sealer{KMS: k, KeyID: "alias/forge-party-1", Attester: nsm, PartyID: 1}
	share := []byte(`{"curve":"secp256k1","ecdsa":{"Xi":"secret"}}`)
	ec := ShareContext(1, "cer-1")
	sealed, err := s.Seal(context.Background(), share, ec)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("secret")) {
		t.Fatal("the envelope contains the plaintext")
	}
	// A restarted process has a new RSA key; it must still open the share.
	s2 := &Sealer{KMS: k, KeyID: "alias/forge-party-1", Attester: nsm, PartyID: 1}
	got, err := s2.Open(context.Background(), sealed, ec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, share) {
		t.Fatalf("opened %q", got)
	}
}

func TestOpenRefusesAnotherImage(t *testing.T) {
	nsm, k := kmsFixture()
	s := &Sealer{KMS: k, KeyID: "k", Attester: nsm, PartyID: 1}
	sealed, err := s.Seal(context.Background(), []byte("share"), ShareContext(1, "c"))
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := &Sealer{KMS: k, KeyID: "k", Attester: &pcrOverride{nsm, nitroattesttest.Digest("one byte changed")}, PartyID: 1}
	if _, err := rebuilt.Open(context.Background(), sealed, ShareContext(1, "c")); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("a rebuilt image opened the share: %v", err)
	}
	debug := &Sealer{KMS: k, KeyID: "k", Attester: &pcrOverride{nsm, make([]byte, 48)}, PartyID: 1}
	if _, err := debug.Open(context.Background(), sealed, ShareContext(1, "c")); err == nil {
		t.Fatal("a debug-mode enclave opened the share")
	}
	if _, err := rebuilt.Seal(context.Background(), []byte("x"), ShareContext(1, "c")); err == nil {
		t.Fatal("a rebuilt image obtained a data key")
	}
}

func TestOpenRefusesAnotherContext(t *testing.T) {
	nsm, k := kmsFixture()
	s := &Sealer{KMS: k, KeyID: "k", Attester: nsm, PartyID: 1}
	sealed, _ := s.Seal(context.Background(), []byte("share"), ShareContext(1, "c"))
	for _, ec := range []map[string]string{ShareContext(2, "c"), ShareContext(1, "d"), nil} {
		if _, err := s.Open(context.Background(), sealed, ec); err == nil {
			t.Fatalf("opened under context %v", ec)
		}
	}
}

func TestOpenRefusesTampering(t *testing.T) {
	nsm, k := kmsFixture()
	s := &Sealer{KMS: k, KeyID: "k", Attester: nsm, PartyID: 1}
	ec := ShareContext(1, "c")
	sealed, _ := s.Seal(context.Background(), []byte("share"), ec)
	var env Envelope
	_ = json.Unmarshal(sealed, &env)
	env.Ciphertext[0] ^= 1
	bad, _ := json.Marshal(env)
	if _, err := s.Open(context.Background(), bad, ec); err == nil {
		t.Fatal("tampered ciphertext opened")
	}
	for _, junk := range [][]byte{nil, []byte("{}"), []byte(`{"v":2}`), []byte("not json")} {
		if _, err := s.Open(context.Background(), junk, ec); err == nil {
			t.Fatalf("junk %q opened", junk)
		}
	}
}

func TestSealerRefusesAMisbehavingKMS(t *testing.T) {
	nsm, k := kmsFixture()
	s := &Sealer{KMS: k, KeyID: "k", Attester: nsm, PartyID: 1}
	k.leakPlaintext = true
	if _, err := s.Seal(context.Background(), []byte("x"), nil); err == nil || !strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("plaintext data key accepted: %v", err)
	}
	k.leakPlaintext = false
	k.dropRecipientBlob = true
	if _, err := s.Seal(context.Background(), []byte("x"), nil); err == nil || !strings.Contains(err.Error(), "CiphertextForRecipient") {
		t.Fatalf("missing recipient blob accepted: %v", err)
	}
	if _, err := (&Sealer{KMS: k, KeyID: "k", PartyID: 1}).Seal(context.Background(), []byte("x"), nil); err == nil {
		t.Fatal("sealed without an attester")
	}
}

func TestOpenRecipientBlobRefusesGarbage(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for _, b := range [][]byte{nil, []byte("junk"), {0x30, 0x80}, envelopeFor(&other.PublicKey, make([]byte, 32)), envelopeFor(&key.PublicKey, make([]byte, 16))} {
		if _, err := openRecipientBlob(key, b); err == nil {
			t.Fatalf("garbage %x opened", b[:min(len(b), 8)])
		}
	}
	if dk, err := openRecipientBlob(key, envelopeFor(&key.PublicKey, bytes.Repeat([]byte{9}, 32))); err != nil || len(dk) != 32 {
		t.Fatalf("good blob: %v", err)
	}
}
