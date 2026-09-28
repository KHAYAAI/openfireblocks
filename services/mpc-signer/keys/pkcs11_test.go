//go:build pkcs11

package keys_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/miekg/pkcs11"

	"forge-crypto/mpc-signer/internal/ethcrypto"
	"forge-crypto/mpc-signer/keys"
	"forge-crypto/mpc-signer/keys/softhsmtest"
)

// These run against SoftHSM2 through the same PKCS#11 calls a Thales
// Luna, a YubiHSM or AWS CloudHSM receive. Run with:
//
//	CGO_ENABLED=1 go test -tags pkcs11 ./keys/...

func open(t *testing.T, cfg *keys.PKCS11Config) keys.KeySigner {
	t.Helper()
	k, err := keys.OpenPKCS11(cfg)
	if err != nil {
		t.Fatalf("OpenPKCS11: %v", err)
	}
	t.Cleanup(func() { _ = k.(io.Closer).Close() })
	return k
}

func digest(i int) []byte {
	d := sha256.Sum256([]byte(fmt.Sprintf("digest %d", i)))
	return d[:]
}

func TestAKeyGeneratedOnTheTokenSignsForItsOwnAddress(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "gen", "treasury")
	if err := keys.GeneratePKCS11Key(cfg); err != nil {
		t.Fatal(err)
	}
	k := open(t, cfg)

	if !strings.Contains(k.Describe(), `"treasury"`) || !strings.Contains(k.Describe(), `"gen"`) {
		t.Errorf("Describe() = %q", k.Describe())
	}

	// HSM ECDSA uses a random nonce, so about half of these come back
	// high-S from the token. Every one must leave here low-S and recover.
	for i := 0; i < 64; i++ {
		d := digest(i)
		sig, err := k.SignDigest(context.Background(), d)
		if err != nil {
			t.Fatal(err)
		}
		var s btcec.ModNScalar
		s.SetByteSlice(sig[32:64])
		if s.IsOverHalfOrder() {
			t.Fatalf("signature %d left the signer high-S", i)
		}
		x, y, err := ethcrypto.SigToPub(d, sig)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(ethcrypto.MarshalPubkey(x, y)); got != string(k.PublicKey()) {
			t.Fatalf("signature %d recovers to a different key", i)
		}
	}
}

// The reason for the whole mode: the token must not give the key back.
func TestTheGeneratedPrivateKeyCannotBeReadOffTheToken(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "noexport", "k")
	if err := keys.GeneratePKCS11Key(cfg); err != nil {
		t.Fatal(err)
	}
	ctx, sh := softhsmtest.Session(t, cfg)
	if err := ctx.FindObjectsInit(sh, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PRIVATE_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, "k"),
	}); err != nil {
		t.Fatal(err)
	}
	objs, _, err := ctx.FindObjects(sh, 2)
	_ = ctx.FindObjectsFinal(sh)
	if err != nil || len(objs) != 1 {
		t.Fatalf("found %d private keys: %v", len(objs), err)
	}
	attrs, err := ctx.GetAttributeValue(sh, objs[0], []*pkcs11.Attribute{pkcs11.NewAttribute(pkcs11.CKA_VALUE, nil)})
	if err == nil && len(attrs) == 1 && len(attrs[0].Value) > 0 {
		t.Fatalf("the token returned the private key's value (%d bytes)", len(attrs[0].Value))
	}
}

// The same key in software and in hardware must be the same address, and
// each side's signatures must verify under the other's public key. They
// are not byte-identical: the software path is RFC 6979 deterministic,
// while PKCS#11 CKM_ECDSA on most tokens (SoftHSM included) draws a random
// nonce. Both are valid ECDSA; the chains care only that the signature
// verifies, is low-S and recovers to the right sender.
func TestAnImportedKeyHasTheSameAddressInHardwareAsInSoftware(t *testing.T) {
	const priv = "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	cfg := softhsmtest.NewToken(t, "import", "known")
	softhsmtest.ImportKey(t, cfg, priv, false)

	hw := open(t, cfg)
	sw, _ := keys.RawKeySignerFromHex(priv)

	hwAddr, _ := keys.Address(hw)
	swAddr, _ := keys.Address(sw)
	if hwAddr != swAddr {
		t.Fatalf("hardware address %s, software address %s", hwAddr, swAddr)
	}
	// Published address for this key (the go-ethereum docs example).
	if hwAddr != "0x2c7536E3605D9C16a7a3D7b1898e529396a65c23" {
		t.Fatalf("address %s", hwAddr)
	}
	for i := 0; i < 16; i++ {
		d := digest(1000 + i)
		sig, err := hw.SignDigest(context.Background(), d)
		if err != nil {
			t.Fatal(err)
		}
		if !ethcrypto.VerifySignature(sw.PublicKey(), d, sig[:64]) {
			t.Fatal("a hardware signature does not verify under the software key")
		}
	}
}

func TestAnExtractableKeyIsRefused(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "extractable", "leaky")
	softhsmtest.ImportKey(t, cfg, strings.Repeat("21", 32), true)
	_, err := keys.OpenPKCS11(cfg)
	if err == nil || !strings.Contains(err.Error(), "SENSITIVE") && !strings.Contains(err.Error(), "EXTRACTABLE") {
		t.Fatalf("an extractable key was accepted: %v", err)
	}
}

func TestAMissingKeyIsRefusedAndNeverCreated(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "missing", "typo")
	if _, err := keys.OpenPKCS11(cfg); err == nil || !strings.Contains(err.Error(), "no private key") {
		t.Fatalf("expected a missing-key error, got %v", err)
	}
	// And opening did not create one.
	if _, err := keys.OpenPKCS11(cfg); err == nil {
		t.Fatal("a key appeared after a failed open")
	}
}

func TestGeneratingOverAnExistingLabelIsRefused(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "twice", "k")
	if err := keys.GeneratePKCS11Key(cfg); err != nil {
		t.Fatal(err)
	}
	if err := keys.GeneratePKCS11Key(cfg); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a second key with the same label was created: %v", err)
	}
}

// Two key pairs under one label: refuse, do not pick one.
func TestDuplicateLabelsAreRefused(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "dupes", "same")
	softhsmtest.ImportKey(t, cfg, strings.Repeat("31", 32), false)
	softhsmtest.ImportKey(t, cfg, strings.Repeat("32", 32), false)
	if _, err := keys.OpenPKCS11(cfg); err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Fatalf("duplicate labels were accepted: %v", err)
	}
}

// A public object that belongs to a different private key -- what a
// half-finished rotation leaves behind. The self-test signature must
// catch it before the service hands out an address it cannot sign for.
func TestAPublicKeyThatDoesNotMatchThePrivateKeyIsRefused(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "mismatch", "rotated")
	softhsmtest.ImportMismatchedPair(t, cfg, strings.Repeat("41", 32), strings.Repeat("42", 32), false)

	if _, err := keys.OpenPKCS11(cfg); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a mismatched key pair was accepted: %v", err)
	}
}

func TestAWrongPINIsRefusedWithoutEchoingIt(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "pin", "k")
	if err := keys.GeneratePKCS11Key(cfg); err != nil {
		t.Fatal(err)
	}
	bad := *cfg
	bad.PIN = "wrong-pin-987"
	_, err := keys.OpenPKCS11(&bad)
	if err == nil {
		t.Fatal("a wrong PIN was accepted")
	}
	if strings.Contains(err.Error(), bad.PIN) {
		t.Fatalf("the error message contains the PIN: %v", err)
	}
}

func TestAnUnknownTokenIsRefused(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "real", "k")
	bad := *cfg
	bad.TokenLabel = "not-there"
	if _, err := keys.OpenPKCS11(&bad); err == nil || !strings.Contains(err.Error(), "not-there") {
		t.Fatalf("expected an unknown-token error, got %v", err)
	}
}

// One PKCS#11 session, many goroutines: the signer serialises.
func TestConcurrentSigningIsSafe(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "concurrent", "k")
	if err := keys.GeneratePKCS11Key(cfg); err != nil {
		t.Fatal(err)
	}
	k := open(t, cfg)

	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				d := digest(g*100 + i)
				sig, err := k.SignDigest(context.Background(), d)
				if err != nil {
					errs <- err
					return
				}
				if !ethcrypto.VerifySignature(k.PublicKey(), d, sig[:64]) {
					errs <- fmt.Errorf("goroutine %d signature %d does not verify", g, i)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// Every session on a token dropped -- what an HSM restart looks like
// from here. The signer must reconnect and carry on with the same key.
func TestSigningSurvivesTheSessionBeingClosedUnderIt(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "reconnect", "k")
	if err := keys.GeneratePKCS11Key(cfg); err != nil {
		t.Fatal(err)
	}
	k := open(t, cfg)
	before := k.PublicKey()

	ctx, sh := softhsmtest.Session(t, cfg)
	info, err := ctx.GetSessionInfo(sh)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctx.CloseAllSessions(info.SlotID); err != nil {
		t.Fatal(err)
	}

	d := digest(7)
	sig, err := k.SignDigest(context.Background(), d)
	if err != nil {
		t.Fatalf("signing after the sessions were closed: %v", err)
	}
	if string(k.PublicKey()) != string(before) {
		t.Fatal("the key changed across the reconnect")
	}
	if !ethcrypto.VerifySignature(before, d, sig[:64]) {
		t.Fatal("the signature after reconnecting does not verify")
	}
}

func TestADigestThatIsNot32BytesIsRefused(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "short", "k")
	if err := keys.GeneratePKCS11Key(cfg); err != nil {
		t.Fatal(err)
	}
	k := open(t, cfg)
	if _, err := k.SignDigest(context.Background(), make([]byte, 20)); err == nil {
		t.Fatal("a 20-byte digest was signed")
	}
}
