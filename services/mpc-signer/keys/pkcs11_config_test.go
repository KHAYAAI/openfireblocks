package keys

import (
	"strings"
	"testing"
)

func setEnv(t *testing.T, kv map[string]string) {
	for _, k := range []string{EnvPKCS11Library, EnvPKCS11TokenLabel, EnvPKCS11Slot, EnvPKCS11PIN, EnvPKCS11KeyLabel} {
		t.Setenv(k, kv[k])
	}
}

func TestNoHSMVariablesMeansSoftwareMode(t *testing.T) {
	setEnv(t, nil)
	cfg, err := PKCS11ConfigFromEnv()
	if cfg != nil || err != nil {
		t.Fatalf("got %+v, %v", cfg, err)
	}
}

func TestAFullHSMConfigurationIsAccepted(t *testing.T) {
	setEnv(t, map[string]string{
		EnvPKCS11Library: "/lib/x.so", EnvPKCS11TokenLabel: "t", EnvPKCS11PIN: " p in ", EnvPKCS11KeyLabel: "k",
	})
	cfg, err := PKCS11ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PIN != " p in " {
		t.Errorf("the PIN was altered: %q", cfg.PIN)
	}
}

// Every partial configuration is refused, and the error names what is
// missing -- but never the PIN's value.
func TestAPartialHSMConfigurationIsRefused(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"library only": {EnvPKCS11Library: "/lib/x.so"},
		"no PIN":       {EnvPKCS11Library: "/lib/x.so", EnvPKCS11TokenLabel: "t", EnvPKCS11KeyLabel: "k"},
		"no token":     {EnvPKCS11Library: "/lib/x.so", EnvPKCS11PIN: "secret-pin", EnvPKCS11KeyLabel: "k"},
		"no key label": {EnvPKCS11Library: "/lib/x.so", EnvPKCS11TokenLabel: "t", EnvPKCS11PIN: "secret-pin"},
		"PIN only":     {EnvPKCS11PIN: "secret-pin"},
		"both slot and token": {EnvPKCS11Library: "/lib/x.so", EnvPKCS11TokenLabel: "t", EnvPKCS11Slot: "3",
			EnvPKCS11PIN: "secret-pin", EnvPKCS11KeyLabel: "k"},
		"slot not a number": {EnvPKCS11Library: "/lib/x.so", EnvPKCS11Slot: "one",
			EnvPKCS11PIN: "secret-pin", EnvPKCS11KeyLabel: "k"},
	} {
		setEnv(t, env)
		cfg, err := PKCS11ConfigFromEnv()
		if err == nil {
			t.Errorf("%s: accepted %+v", name, cfg)
			continue
		}
		if strings.Contains(err.Error(), "secret-pin") {
			t.Errorf("%s: the error contains the PIN", name)
		}
	}
}

func TestASlotNumberIsAnAlternativeToATokenLabel(t *testing.T) {
	setEnv(t, map[string]string{
		EnvPKCS11Library: "/lib/x.so", EnvPKCS11Slot: "7", EnvPKCS11PIN: "p", EnvPKCS11KeyLabel: "k",
	})
	cfg, err := PKCS11ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasSlot || cfg.Slot != 7 {
		t.Fatalf("slot = %d (set %v)", cfg.Slot, cfg.HasSlot)
	}
}
