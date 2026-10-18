package keys

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PKCS11Config says which key, on which token, behind which library.
//
// It lives outside the build tag so that configuration is parsed -- and
// a half-configured deployment is refused -- identically whether or not
// this binary can actually talk to an HSM. A binary built without
// PKCS#11 support that is handed HSM_PKCS11_* must fail loudly, not fall
// back to a software key and let the operator believe otherwise.
type PKCS11Config struct {
	// Library is the vendor's PKCS#11 module, e.g.
	// /usr/lib/softhsm/libsofthsm2.so or /opt/cloudhsm/lib/libcloudhsm_pkcs11.so.
	Library string
	// TokenLabel selects the token by label. Preferred over Slot: slot
	// numbers are assigned by the module and can change across restarts.
	TokenLabel string
	// Slot selects the token by slot ID when TokenLabel is empty.
	Slot    uint
	HasSlot bool
	// PIN is the user PIN. Read from the environment, which in the chart
	// is a secretKeyRef; never from a flag or a config file.
	PIN string
	// KeyLabel names the key pair (both halves carry the same CKA_LABEL).
	KeyLabel string
}

// Environment variable names.
const (
	EnvPKCS11Library    = "HSM_PKCS11_LIBRARY"
	EnvPKCS11TokenLabel = "HSM_PKCS11_TOKEN_LABEL"
	EnvPKCS11Slot       = "HSM_PKCS11_SLOT"
	EnvPKCS11PIN        = "HSM_PKCS11_PIN"
	EnvPKCS11KeyLabel   = "HSM_PKCS11_KEY_LABEL"
)

// PKCS11ConfigFromEnv reads HSM_PKCS11_*.
//
// Returns (nil, nil) when none of them is set: HSM mode is off. Returns
// an error when some are set and others are not, because every partial
// configuration has the same bad outcome -- a service that starts, signs
// with something, and is not signing with the HSM its operator
// configured.
func PKCS11ConfigFromEnv() (*PKCS11Config, error) {
	get := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	cfg := &PKCS11Config{
		Library:    get(EnvPKCS11Library),
		TokenLabel: get(EnvPKCS11TokenLabel),
		PIN:        os.Getenv(EnvPKCS11PIN), // a PIN may legitimately have spaces
		KeyLabel:   get(EnvPKCS11KeyLabel),
	}
	slot := get(EnvPKCS11Slot)

	if cfg.Library == "" && cfg.TokenLabel == "" && slot == "" && cfg.PIN == "" && cfg.KeyLabel == "" {
		return nil, nil
	}

	if slot != "" {
		n, err := strconv.ParseUint(slot, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s=%q is not a slot number", EnvPKCS11Slot, slot)
		}
		cfg.Slot, cfg.HasSlot = uint(n), true
	}

	var missing []string
	if cfg.Library == "" {
		missing = append(missing, EnvPKCS11Library)
	}
	if cfg.TokenLabel == "" && !cfg.HasSlot {
		missing = append(missing, EnvPKCS11TokenLabel+" (or "+EnvPKCS11Slot+")")
	}
	if cfg.PIN == "" {
		missing = append(missing, EnvPKCS11PIN)
	}
	if cfg.KeyLabel == "" {
		missing = append(missing, EnvPKCS11KeyLabel)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("HSM signing is partly configured; also set %s, or unset every HSM_PKCS11_* "+
			"variable to sign with a software key", strings.Join(missing, ", "))
	}
	if cfg.TokenLabel != "" && cfg.HasSlot {
		return nil, fmt.Errorf("set %s or %s, not both", EnvPKCS11TokenLabel, EnvPKCS11Slot)
	}
	return cfg, nil
}

func (c *PKCS11Config) token() string {
	if c.TokenLabel != "" {
		return fmt.Sprintf("token %q", c.TokenLabel)
	}
	return fmt.Sprintf("slot %d", c.Slot)
}
