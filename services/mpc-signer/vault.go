package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	vault "github.com/hashicorp/vault/api"
)

// vault.go integrates HashiCorp Vault for signing-key management.
//
// Phase 1 stores the single shared signing key in Vault's KV v2 engine so it
// survives restarts and is never baked into an image or env var in production.
// Phase 2 moves to per-customer Binance TSS-Lib key shares, one Vault path per
// share, with the key never reconstructed in a single place.
//
// Behaviour of ResolveSigningKey:
//   - VAULT_ADDR set: read the key from Vault; if absent, generate one and
//     persist it (fail-closed — any Vault error aborts startup).
//   - else MPC_SIGNER_PRIVATE_KEY set: use it (local/dev convenience).
//   - else: return "" so NewMPCSigner generates an ephemeral key.

type vaultConfig struct {
	addr    string
	token   string
	mount   string
	keyPath string
}

// ResolveSigningKey returns the hex-encoded signing private key according to the
// precedence described above.
func ResolveSigningKey(ctx context.Context, getenv func(string) string) (string, error) {
	addr := getenv("VAULT_ADDR")
	if addr == "" {
		// No Vault configured: fall back to env key (may be empty → ephemeral).
		return getenv("MPC_SIGNER_PRIVATE_KEY"), nil
	}

	cfg := vaultConfig{
		addr:    addr,
		token:   getenv("VAULT_TOKEN"),
		mount:   orDefault(getenv("VAULT_KV_MOUNT"), "secret"),
		keyPath: orDefault(getenv("VAULT_KEY_PATH"), "openfireblocks/mpc-signer"),
	}
	return loadOrCreateVaultKey(ctx, cfg)
}

func loadOrCreateVaultKey(ctx context.Context, cfg vaultConfig) (string, error) {
	client, err := vault.NewClient(&vault.Config{Address: cfg.addr})
	if err != nil {
		return "", fmt.Errorf("vault client: %w", err)
	}
	if cfg.token != "" {
		client.SetToken(cfg.token)
	}

	kv := client.KVv2(cfg.mount)

	// Try to read an existing key.
	secret, err := kv.Get(ctx, cfg.keyPath)
	if err == nil && secret != nil && secret.Data != nil {
		if raw, ok := secret.Data["private_key"].(string); ok && raw != "" {
			return raw, nil
		}
	} else if err != nil && !isVaultNotFound(err) {
		return "", fmt.Errorf("vault read %s: %w", cfg.keyPath, err)
	}

	// Not present: generate a new key and persist it.
	priv, err := crypto.GenerateKey()
	if err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	hexKey := fmt.Sprintf("%x", crypto.FromECDSA(priv))
	address := crypto.PubkeyToAddress(priv.PublicKey).Hex()

	if _, err := kv.Put(ctx, cfg.keyPath, map[string]interface{}{
		"private_key": hexKey,
		"address":     address,
	}); err != nil {
		return "", fmt.Errorf("vault write %s: %w", cfg.keyPath, err)
	}
	return hexKey, nil
}

func isVaultNotFound(err error) bool {
	// KVv2.Get returns an error containing this phrase for a missing secret.
	return err != nil && strings.Contains(err.Error(), "not found")
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
