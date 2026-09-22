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
// CeremonyContext is everything other than the share that a party needs to
// sign again after a restart.
//
// Sealed alongside the share, and the reason is the gap this closes: a
// share on its own is not enough to sign. tss-lib needs the committee's
// party identities, the threshold, and -- since proactive refresh -- which
// epoch the share's coordinates belong to. Without those, a party that
// restarts holds a perfectly good share it cannot use, and a recovery
// procedure that restores shares recovers nothing.
type CeremonyContext struct {
	Threshold    int    `json:"threshold"`
	TotalParties int    `json:"total_parties"`
	Curve        string `json:"curve"`
	Epoch        int    `json:"epoch"`
	PublicKeyHex string `json:"public_key_hex"`
	Address      string `json:"address"`
}

func SealKeyShare(ctx context.Context, getenv func(string) string, partyID int, ceremonyID string, share *KeyShare) (bool, error) {
	return SealKeyShareWithContext(ctx, getenv, partyID, ceremonyID, share, nil)
}

// SealKeyShareWithContext seals the share and the context needed to use it.
func SealKeyShareWithContext(ctx context.Context, getenv func(string) string, partyID int, ceremonyID string, share *KeyShare, cc *CeremonyContext) (bool, error) {
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

	// Marshalled through the tagged union, so the stored blob says which
	// curve it was generated on. A share read back without that tag is a
	// blob of JSON that happens to parse as either package's struct, and
	// unmarshalling it into the wrong one produces a party that cannot
	// sign -- discovered at signing time, on a key holding money.
	raw, err := MarshalShare(share)
	if err != nil {
		return false, fmt.Errorf("marshal key share: %w", err)
	}

	payload := map[string]interface{}{
		"party_id":    partyID,
		"ceremony_id": ceremonyID,
		"curve":       string(share.Curve),
		"save_data":   string(raw),
	}
	if cc != nil {
		ccRaw, err := json.Marshal(cc)
		if err != nil {
			return false, fmt.Errorf("marshal ceremony context: %w", err)
		}
		payload["ceremony_context"] = string(ccRaw)
	}

	kv := client.KVv2(cfg.mount)
	if _, err := kv.Put(ctx, cfg.keyPath, payload); err != nil {
		return false, fmt.Errorf("vault write %s: %w", cfg.keyPath, err)
	}
	return true, nil
}

// LoadKeyShare reads a previously-sealed secp256k1 key share back from
// Vault.
//
// Prefer LoadSealedShare, which handles both curves and returns the
// context needed to actually use the share. This exists because it is
// exported, and because a narrow "give me the ECDSA save-data" call is
// what the round-trip test wants.
//
// It used to unmarshal the stored blob straight into a
// LocalPartySaveData, and that was wrong in the worst available way.
// Since shares were tagged by curve, the stored JSON is
// {"curve":...,"ecdsa":{...}} -- a shape that unmarshals into
// LocalPartySaveData with no error and no matching fields, producing a
// struct whose every member is nil. A caller got back a key share that
// was not a key share and was told nothing.
//
// It was invisible because the only test that exercised it needed a real
// Vault, so it skipped everywhere and had never once run. That is the
// argument for vault_fake_test.go: a test that only runs where somebody
// installed a binary is a test that does not run.
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

	// Through UnmarshalShare, which reads the curve tag and refuses a blob
	// that does not carry one. Unmarshalling into the concrete type
	// directly is what produced the silent all-nil share described above.
	share, err := UnmarshalShare([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("sealed key share at %s: %w", cfg.keyPath, err)
	}
	if share.Curve != CurveSecp256k1 || share.ECDSA == nil {
		return nil, fmt.Errorf(
			"the share at %s is a %s share; LoadKeyShare returns secp256k1 save-data only. Use LoadSealedShare",
			cfg.keyPath, share.Curve)
	}
	return share.ECDSA, nil
}

func orDefaultVault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// LoadSealedShare reads back both the share and the context needed to use
// it.
//
// Separate from LoadKeyShare, which predates curve tagging and returns a
// bare ECDSA save-data. This is the one a restore path uses: it refuses a
// share whose curve tag is missing rather than guessing, for the same
// reason UnmarshalShare does -- a mis-tagged share unmarshals cleanly into
// the wrong package's struct and produces a party that cannot sign, found
// out at signing time on a key holding money.
func LoadSealedShare(ctx context.Context, getenv func(string) string, partyID int, ceremonyID string) (*KeyShare, *CeremonyContext, error) {
	cfg, configured := vaultShareConfigFromEnv(getenv, partyID, ceremonyID)
	if !configured {
		return nil, nil, fmt.Errorf("VAULT_ADDR not set; there is nothing sealed to restore from")
	}

	client, err := vault.NewClient(&vault.Config{Address: cfg.addr})
	if err != nil {
		return nil, nil, fmt.Errorf("vault client: %w", err)
	}
	if cfg.token != "" {
		client.SetToken(cfg.token)
	}

	secret, err := client.KVv2(cfg.mount).Get(ctx, cfg.keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("vault read %s: %w", cfg.keyPath, err)
	}
	if secret == nil || secret.Data == nil {
		return nil, nil, fmt.Errorf("no sealed key share at %s", cfg.keyPath)
	}

	raw, ok := secret.Data["save_data"].(string)
	if !ok || raw == "" {
		return nil, nil, fmt.Errorf("the sealed share at %s is malformed", cfg.keyPath)
	}
	share, err := UnmarshalShare([]byte(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("sealed share at %s: %w", cfg.keyPath, err)
	}

	var cc *CeremonyContext
	if ccRaw, ok := secret.Data["ceremony_context"].(string); ok && ccRaw != "" {
		var parsed CeremonyContext
		if err := json.Unmarshal([]byte(ccRaw), &parsed); err != nil {
			return nil, nil, fmt.Errorf("sealed ceremony context at %s is malformed: %w", cfg.keyPath, err)
		}
		cc = &parsed
	}
	return share, cc, nil
}
