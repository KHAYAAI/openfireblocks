package main

import (
	"context"
	"fmt"

	"forge-crypto/mpc-signer/keys"
)

// resolveSigner decides where the signing key lives.
//
// HSM_PKCS11_* set: the key is on a PKCS#11 token and nothing else is
// consulted. Otherwise, the existing order: Vault, then
// MPC_SIGNER_PRIVATE_KEY, then an ephemeral key.
//
// HSM mode refuses to start if a software key is also configured. Not
// because the software key would be used -- it would not -- but because
// a deployment carrying both is one where somebody believes the key is
// in hardware while a copy of a key sits in Vault or an environment
// variable. That is exactly the state the mode exists to rule out, so it
// is an error to fix, not a precedence rule to learn.
func resolveSigner(ctx context.Context, getenv func(string) string) (signer *MPCSigner, keyHex string, hardware bool, err error) {
	cfg, err := keys.PKCS11ConfigFromEnv()
	if err != nil {
		return nil, "", false, err
	}
	if cfg != nil {
		for _, v := range []string{"VAULT_ADDR", "MPC_SIGNER_PRIVATE_KEY"} {
			if getenv(v) != "" {
				return nil, "", false, fmt.Errorf("HSM signing is configured and so is %s; a hardware "+
					"deployment must not also carry a software key -- unset %s", v, v)
			}
		}
		key, err := keys.OpenPKCS11(cfg)
		if err != nil {
			return nil, "", false, fmt.Errorf("opening the HSM key: %w", err)
		}
		signer, err := NewMPCSignerFromKey(key)
		return signer, "", true, err
	}

	keyHex, err = ResolveSigningKey(ctx, getenv)
	if err != nil {
		return nil, "", false, fmt.Errorf("failed to resolve signing key: %w", err)
	}
	signer, err = NewMPCSigner(keyHex)
	return signer, keyHex, false, err
}
