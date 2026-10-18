package keys

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"

	"forge-crypto/mpc-signer/internal/ethcrypto"
)

func digestOf(s string) []byte {
	d := sha256.Sum256([]byte(s))
	return d[:]
}

// Private key 1 has a published Ethereum address. If this drifts,
// everything downstream of the key abstraction is wrong.
func TestTheWellKnownAddressForPrivateKeyOne(t *testing.T) {
	k, err := RawKeySignerFromHex("0x0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	addr, err := Address(k)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf" {
		t.Fatalf("address for key 1 = %s", addr)
	}
}

func TestARawKeySignatureRecoversAndVerifies(t *testing.T) {
	k, err := RawKeySignerFromHex(strings.Repeat("11", 32))
	if err != nil {
		t.Fatal(err)
	}
	d := digestOf("hello")
	sig, err := k.SignDigest(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 || sig[64] > 1 {
		t.Fatalf("expected [R||S||V] with V in {0,1}, got %d bytes, V=%d", len(sig), sig[64])
	}
	x, y, err := ethcrypto.SigToPub(d, sig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ethcrypto.MarshalPubkey(x, y), k.PublicKey()) {
		t.Fatal("the signature does not recover to the signer's own key")
	}
}

// The property the hardware path depends on: stripping V from a good
// signature and normalising it again must give back exactly the same 65
// bytes. If this held only approximately, an HSM-produced signature
// would differ from the software one for the same key and digest.
func TestNormalisingAGoodSignatureIsTheIdentity(t *testing.T) {
	for i := 0; i < 50; i++ {
		k, err := RawKeySignerFromHex(fmt.Sprintf("%064x", 1000+i))
		if err != nil {
			t.Fatal(err)
		}
		d := digestOf(fmt.Sprintf("message %d", i))
		want, err := k.SignDigest(context.Background(), d)
		if err != nil {
			t.Fatal(err)
		}
		got, err := RecoverableSignature(d, want[:64], k.PublicKey())
		if err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("key %d: normalised %x, want %x", i, got, want)
		}
	}
}

// What an HSM returns about half the time: the high-S twin of a valid
// signature. Both verify under textbook ECDSA; every Ethereum and Bitcoin
// node rejects the high one. It must come back as the low one.
func TestAHighSSignatureIsFoldedToLowS(t *testing.T) {
	k, err := RawKeySignerFromHex(strings.Repeat("22", 32))
	if err != nil {
		t.Fatal(err)
	}
	d := digestOf("malleable")
	low, err := k.SignDigest(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}

	var s btcec.ModNScalar
	s.SetByteSlice(low[32:64])
	s.Negate() // N - S: the high twin
	high := make([]byte, 64)
	copy(high, low[:32])
	sb := s.Bytes()
	copy(high[32:], sb[:])

	if ethcrypto.VerifySignature(k.PublicKey(), d, high) {
		t.Fatal("the test is wrong: the verifier accepted a high-S signature")
	}

	got, err := RecoverableSignature(d, high, k.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, low) {
		t.Fatalf("a high-S signature normalised to %x, want the low-S %x", got, low)
	}
}

// The check that catches a misconfigured HSM: a token that signs with a
// different key from the one it reported must not produce a signature
// this service hands back.
func TestASignatureFromADifferentKeyIsRefused(t *testing.T) {
	signer, _ := RawKeySignerFromHex(strings.Repeat("33", 32))
	other, _ := RawKeySignerFromHex(strings.Repeat("44", 32))

	d := digestOf("which key")
	sig, err := signer.SignDigest(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverableSignature(d, sig[:64], other.PublicKey()); err == nil {
		t.Fatal("a signature by one key was accepted as another key's")
	}
}

func TestMalformedInputsAreRefused(t *testing.T) {
	k, _ := RawKeySignerFromHex(strings.Repeat("55", 32))
	d := digestOf("x")
	good, _ := k.SignDigest(context.Background(), d)

	zeroR := append(make([]byte, 32), good[32:64]...)
	for name, tc := range map[string]struct{ digest, rs []byte }{
		"short digest":    {d[:31], good[:64]},
		"short signature": {d, good[:63]},
		"zero R":          {d, zeroR},
		"all ones S":      {d, append(append([]byte(nil), good[:32]...), bytes.Repeat([]byte{0xff}, 32)...)},
	} {
		if _, err := RecoverableSignature(tc.digest, tc.rs, k.PublicKey()); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAPublicKeyThatIsNotOnTheCurveIsRefused(t *testing.T) {
	bad := make([]byte, 65)
	bad[0] = 4
	bad[64] = 7
	if _, _, err := coordinates(bad); err == nil {
		t.Fatal("a point off the curve produced coordinates")
	}
}

// PublicKey hands out a copy. A caller mutating the returned slice must
// not change the key every later signature is checked against.
func TestThePublicKeyCannotBeMutatedThroughTheAccessor(t *testing.T) {
	k, _ := RawKeySignerFromHex(strings.Repeat("66", 32))
	p := k.PublicKey()
	p[10] ^= 0xff
	if bytes.Equal(p, k.PublicKey()) {
		t.Fatal("mutating the returned public key changed the signer's key")
	}
}
