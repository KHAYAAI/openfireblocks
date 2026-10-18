package main

import (
	"context"
	"strings"
	"testing"

	"forge-crypto/mpc-signer/keys"
)

func hsmEnv(t *testing.T, extra map[string]string) func(string) string {
	t.Setenv(keys.EnvPKCS11Library, "/nonexistent/libpkcs11.so")
	t.Setenv(keys.EnvPKCS11TokenLabel, "t")
	t.Setenv(keys.EnvPKCS11PIN, "p")
	t.Setenv(keys.EnvPKCS11KeyLabel, "k")
	return func(k string) string { return extra[k] }
}

func TestHSMModeRefusesASoftwareKeyAlongsideIt(t *testing.T) {
	for _, v := range []string{"VAULT_ADDR", "MPC_SIGNER_PRIVATE_KEY"} {
		getenv := hsmEnv(t, map[string]string{v: "set"})
		_, _, _, err := resolveSigner(context.Background(), getenv)
		if err == nil || !strings.Contains(err.Error(), v) {
			t.Errorf("%s alongside HSM mode: got %v", v, err)
		}
	}
}

// HSM mode that cannot open its token fails; it does not fall back to a
// generated software key.
func TestHSMModeThatCannotReachItsTokenFailsInsteadOfFallingBack(t *testing.T) {
	getenv := hsmEnv(t, nil)
	signer, _, _, err := resolveSigner(context.Background(), getenv)
	if err == nil {
		t.Fatalf("started with %s", signer.Key().Describe())
	}
}

func TestWithoutHSMVariablesTheSoftwarePathIsUnchanged(t *testing.T) {
	const key = "0x0000000000000000000000000000000000000000000000000000000000000001"
	getenv := func(k string) string {
		if k == "MPC_SIGNER_PRIVATE_KEY" {
			return key
		}
		return ""
	}
	signer, keyHex, hardware, err := resolveSigner(context.Background(), getenv)
	if err != nil {
		t.Fatal(err)
	}
	if hardware || keyHex != key || signer.Address() != "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf" {
		t.Fatalf("hardware=%v keyHex=%q address=%s", hardware, keyHex, signer.Address())
	}
}
