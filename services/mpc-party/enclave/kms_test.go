package enclave

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"strings"
	"testing"

	"openfireblocks.com/services/mpc-party/enclave/enclavetest"
	"openfireblocks.com/services/mpc-party/nitroattest"
	"openfireblocks.com/services/mpc-party/nitroattest/nitroattesttest"
)

func kmsFixture() (*nitroattesttest.FakeNSM, *enclavetest.FakeKMS) {
	nsm := nitroattesttest.New()
	v := &nitroattest.Verifier{Policy: nitroattest.Policy{ImagePCR0: []string{nitroattesttest.Hex("image")}}, Roots: nsm.Roots()}
	return nsm, enclavetest.NewFakeKMS(v)
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
	k.LeakPlaintext = true
	if _, err := s.Seal(context.Background(), []byte("x"), nil); err == nil || !strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("plaintext data key accepted: %v", err)
	}
	k.LeakPlaintext = false
	k.DropRecipientBlob = true
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
	for _, b := range [][]byte{nil, []byte("junk"), {0x30, 0x80}, enclavetest.EnvelopeFor(&other.PublicKey, make([]byte, 32)), enclavetest.EnvelopeFor(&key.PublicKey, make([]byte, 16))} {
		if _, err := openRecipientBlob(key, b); err == nil {
			t.Fatalf("garbage %x opened", b[:min(len(b), 8)])
		}
	}
	if dk, err := openRecipientBlob(key, enclavetest.EnvelopeFor(&key.PublicKey, bytes.Repeat([]byte{9}, 32))); err != nil || len(dk) != 32 {
		t.Fatalf("good blob: %v", err)
	}
}
