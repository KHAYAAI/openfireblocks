package main

import (
	"context"
	"encoding/json"
	"fmt"

	tsskeygen "github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	vault "github.com/hashicorp/vault/api"
)

// vault_seal.go seals this party's DKG key share -- the actual private
// material, never the full key, never seen by any other party -- in
// Vault's KV v2 engine, so it survives process restarts instead of living
// only in one process's RAM. Same client library and env-var convention as
// services/mpc-signer/vault.go (VAULT_ADDR / VAULT_TOKEN / VAULT_KV_MOUNT),
// deliberately: a different pattern per service would be its own kind of
// risk in code that handles key material.
//
// Precedence, matching mpc-signer's ResolveSigningKey:
//   - VAULT_ADDR unset: sealing is skipped (in-memory only, matching prior
//     behavior) -- a valid, non-error state for local dev/test, exactly as
//     in mpc-signer/vault.go.
//   - VAULT_ADDR set: sealing is required. A write failure fails the whole
//     ceremony (see completeCeremony in tss_party.go) -- a key share that
//     only ever existed in one process's memory is not durably generated,
//     regardless of what the in-memory ceremony state claims.
//
// Scope of this increment: seal-on-completion and read-back, verified
// round-trip-correct against a real Vault dev server. Rehydrating a full
// ceremony (sortedIDs/peers/threshold, not just the raw share) after a
// party process restarts -- so it can resume signing without repeating
// DKG -- needs that surrounding context persisted too, which this does
// not yet do; see docs/security/audit-checklist.md.

type vaultShareConfig struct {
	addr    string
	token   string
	mount   string
	keyPath string
}

func vaultShareConfigFromEnv(getenv func(string) string, partyID int, ceremonyID string) (vaultShareConfig, bool) {
	addr := getenv("VAULT_ADDR")
	if addr == "" {
		return vaultShareConfig{}, false
	}
	return vaultShareConfig{
		addr:    addr,
		token:   getenv("VAULT_TOKEN"),
		mount:   orDefaultVault(getenv("VAULT_KV_MOUNT"), "secret"),
		keyPath: fmt.Sprintf("%s/party-%d/%s", orDefaultVault(getenv("VAULT_KEY_SHARE_PATH"), "openfireblocks/mpc-party"), partyID, ceremonyID),
	}, true
}

// SealKeyShare persists this party's LocalPartySaveData to Vault, encrypted
// at rest by Vault's storage backend and never written to disk in
// plaintext by this process. Returns (false, nil) if VAULT_ADDR isn't set
// -- not an error, just "sealing wasn't configured for this run."
func SealKeyShare(ctx context.Context, getenv func(string) string, partyID int, ceremonyID string, saveData *tsskeygen.LocalPartySaveData) (bool, error) {
	cfg, configured := vaultShareConfigFromEnv(getenv, partyID, ceremonyID)
	if !configured {
		return false, nil
	}

	client, err := vault.NewClient(&vault.Config{Address: cfg.addr})
	if err != nil {
		return false, fmt.Errorf("vault client: %w", err)
	}
	if cfg.token != "" {
		client.SetToken(cfg.token)
	}

	raw, err := json.Marshal(saveData)
	if err != nil {
		return false, fmt.Errorf("marshal key share: %w", err)
	}

	kv := client.KVv2(cfg.mount)
	if _, err := kv.Put(ctx, cfg.keyPath, map[string]interface{}{
		"party_id":    partyID,
		"ceremony_id": ceremonyID,
		"save_data":   string(raw),
	}); err != nil {
		return false, fmt.Errorf("vault write %s: %w", cfg.keyPath, err)
	}
	return true, nil
}

// LoadKeyShare reads a previously-sealed key share back from Vault. Used
// today only for round-trip verification (see vault_seal_test.go); a
// party process resuming after a restart to actively sign again would
// also need the ceremony's sortedIDs/peers/threshold context, not sealed
// by this increment -- see the package doc comment above.
func LoadKeyShare(ctx context.Context, getenv func(string) string, partyID int, ceremonyID string) (*tsskeygen.LocalPartySaveData, error) {
	cfg, configured := vaultShareConfigFromEnv(getenv, partyID, ceremonyID)
	if !configured {
		return nil, fmt.Errorf("VAULT_ADDR not set; sealing is not configured")
	}

	client, err := vault.NewClient(&vault.Config{Address: cfg.addr})
	if err != nil {
		return nil, fmt.Errorf("vault client: %w", err)
	}
	if cfg.token != "" {
		client.SetToken(cfg.token)
	}

	kv := client.KVv2(cfg.mount)
	secret, err := kv.Get(ctx, cfg.keyPath)
	if err != nil {
		return nil, fmt.Errorf("vault read %s: %w", cfg.keyPath, err)
	}
	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("no sealed key share found at %s", cfg.keyPath)
	}

	raw, ok := secret.Data["save_data"].(string)
	if !ok || raw == "" {
		return nil, fmt.Errorf("sealed key share at %s is malformed", cfg.keyPath)
	}

	var saveData tsskeygen.LocalPartySaveData
	if err := json.Unmarshal([]byte(raw), &saveData); err != nil {
		return nil, fmt.Errorf("unmarshal sealed key share: %w", err)
	}
	return &saveData, nil
}

func orDefaultVault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
