//go:build pkcs11

// Package softhsmtest gives tests a real PKCS#11 token: SoftHSM2, with a
// fresh token directory per test so nothing leaks between them.
//
// SoftHSM is a software implementation of the PKCS#11 interface, not an
// HSM. What it proves is that this service speaks PKCS#11 correctly --
// sessions, login, object search, attributes, C_Sign -- which is the part
// that is the same on every vendor's hardware. What it cannot prove is
// anything about a specific vendor's quirks; that is the first-deployment
// check in docs/engineering/PKCS11-HSM-SIGNING.md.
package softhsmtest

import (
	"encoding/asn1"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miekg/pkcs11"

	"forge-crypto/mpc-signer/internal/ethcrypto"
	"forge-crypto/mpc-signer/keys"
)

// Library returns the SoftHSM module path, from SOFTHSM2_LIBRARY or the
// Debian/Ubuntu default.
func Library() string {
	if p := os.Getenv("SOFTHSM2_LIBRARY"); p != "" {
		return p
	}
	return "/usr/lib/softhsm/libsofthsm2.so"
}

const (
	PIN   = "123456"
	sopin = "12345678"
)

// NewToken initialises an empty token and returns a config pointing at
// it, with KeyLabel set. Skips the test if SoftHSM is not installed,
// unless REQUIRE_SOFTHSM is set -- which CI sets, so a CI image missing
// SoftHSM fails rather than passing by testing nothing.
//
// The token is created through PKCS#11 itself (C_InitToken, C_InitPIN)
// rather than softhsm2-util: SoftHSM discovers tokens made by another
// process only at C_Initialize, and a Cryptoki module is initialised once
// per process.
func NewToken(t testing.TB, tokenLabel, keyLabel string) *keys.PKCS11Config {
	t.Helper()
	ctx := keepalive(t)

	slots, err := ctx.GetSlotList(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range slots {
		info, err := ctx.GetTokenInfo(s)
		if err != nil || info.Flags&pkcs11.CKF_TOKEN_INITIALIZED != 0 {
			continue
		}
		if err := ctx.InitToken(s, sopin, tokenLabel); err != nil {
			t.Fatalf("C_InitToken: %v", err)
		}
		// The slot list changes after C_InitToken; find the token again.
		sh := openByLabel(t, ctx, tokenLabel)
		if err := ctx.Login(sh, pkcs11.CKU_SO, sopin); err != nil {
			t.Fatalf("SO login: %v", err)
		}
		if err := ctx.InitPIN(sh, PIN); err != nil {
			t.Fatalf("C_InitPIN: %v", err)
		}
		_ = ctx.Logout(sh)
		_ = ctx.CloseSession(sh)
		return &keys.PKCS11Config{Library: Library(), TokenLabel: tokenLabel, PIN: PIN, KeyLabel: keyLabel}
	}
	t.Fatal("SoftHSM has no free slot")
	return nil
}

var (
	keepaliveCtx *pkcs11.Ctx
)

// keepalive initialises the module once for the life of the test binary
// and never finalises it. The signer under test sees
// CKR_CRYPTOKI_ALREADY_INITIALIZED, as it would in a process where
// something else owns the module, and so leaves finalisation alone.
func keepalive(t testing.TB) *pkcs11.Ctx {
	t.Helper()
	if keepaliveCtx != nil {
		return keepaliveCtx
	}
	if _, err := os.Stat(Library()); err != nil {
		if os.Getenv("REQUIRE_SOFTHSM") != "" {
			t.Fatalf("SoftHSM is required but %s is missing", Library())
		}
		t.Skipf("SoftHSM not installed (%s)", Library())
	}
	d, err := os.MkdirTemp("", "softhsm-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(d, "tokens"), 0o700); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(d, "softhsm2.conf")
	body := "directories.tokendir = " + filepath.Join(d, "tokens") + "\nobjectstore.backend = file\nlog.level = ERROR\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Setenv("SOFTHSM2_CONF", conf)

	ctx := pkcs11.New(Library())
	if ctx == nil {
		t.Fatalf("cannot load %s", Library())
	}
	if err := ctx.Initialize(); err != nil {
		t.Fatalf("C_Initialize: %v", err)
	}
	keepaliveCtx = ctx
	return ctx
}

func openByLabel(t testing.TB, ctx *pkcs11.Ctx, label string) pkcs11.SessionHandle {
	t.Helper()
	slots, err := ctx.GetSlotList(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range slots {
		info, err := ctx.GetTokenInfo(s)
		if err != nil || strings.TrimRight(info.Label, " ") != label {
			continue
		}
		sh, err := ctx.OpenSession(s, pkcs11.CKF_SERIAL_SESSION|pkcs11.CKF_RW_SESSION)
		if err != nil {
			t.Fatal(err)
		}
		return sh
	}
	t.Fatalf("token %q not found", label)
	return 0
}

// Session opens a logged-in session on cfg's token for tests that need
// to reach past the signer -- to import a known key, or to try to read
// one back out.
func Session(t testing.TB, cfg *keys.PKCS11Config) (*pkcs11.Ctx, pkcs11.SessionHandle) {
	t.Helper()
	ctx := keepalive(t)
	sh := openByLabel(t, ctx, cfg.TokenLabel)
	if err := ctx.Login(sh, pkcs11.CKU_USER, cfg.PIN); err != nil {
		if perr, ok := err.(pkcs11.Error); !ok || uint(perr) != pkcs11.CKR_USER_ALREADY_LOGGED_IN {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = ctx.CloseSession(sh) })
	return ctx, sh
}

// ImportKey puts a known private key on the token -- sensitive and
// non-extractable once there -- so a test can compare the HSM path
// against the software path for the same key. Production keys are
// generated on the token instead; this exists only so there is a key
// both paths can hold.
func ImportKey(t testing.TB, cfg *keys.PKCS11Config, privHex string, extractable bool) {
	t.Helper()
	ImportMismatchedPair(t, cfg, privHex, privHex, extractable)
}

// ImportMismatchedPair imports a private key and, under the same label,
// the public key of pubOfHex -- which is ImportKey when the two are the
// same, and a half-finished key rotation when they are not.
func ImportMismatchedPair(t testing.TB, cfg *keys.PKCS11Config, privHex, pubOfHex string, extractable bool) {
	t.Helper()
	raw, err := keys.RawKeySignerFromHex(pubOfHex)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ethcrypto.HexToECDSA(privHex)
	if err != nil {
		t.Fatal(err)
	}
	d := make([]byte, 32)
	priv.D.FillBytes(d)
	point, err := asn1.Marshal(raw.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	params := []byte{0x06, 0x05, 0x2b, 0x81, 0x04, 0x00, 0x0a}

	ctx, sh := Session(t, cfg)
	if _, err := ctx.CreateObject(sh, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PRIVATE_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_KEY_TYPE, pkcs11.CKK_EC),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_PRIVATE, true),
		pkcs11.NewAttribute(pkcs11.CKA_SIGN, true),
		pkcs11.NewAttribute(pkcs11.CKA_SENSITIVE, !extractable),
		pkcs11.NewAttribute(pkcs11.CKA_EXTRACTABLE, extractable),
		pkcs11.NewAttribute(pkcs11.CKA_EC_PARAMS, params),
		pkcs11.NewAttribute(pkcs11.CKA_VALUE, d),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, cfg.KeyLabel),
	}); err != nil {
		t.Fatalf("importing the private key: %v", err)
	}
	if _, err := ctx.CreateObject(sh, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PUBLIC_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_KEY_TYPE, pkcs11.CKK_EC),
		pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
		pkcs11.NewAttribute(pkcs11.CKA_VERIFY, true),
		pkcs11.NewAttribute(pkcs11.CKA_EC_PARAMS, params),
		pkcs11.NewAttribute(pkcs11.CKA_EC_POINT, point),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, cfg.KeyLabel),
	}); err != nil {
		t.Fatalf("importing the public key: %v", err)
	}
}
