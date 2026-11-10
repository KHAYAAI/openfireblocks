package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	tsskeygen "github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	vault "github.com/hashicorp/vault/api"
	"openfireblocks.com/services/mpc-party/enclave"
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
	if _, enclaveSealed := sealerFor(partyID); enclaveSealed && !configured {
		// An enclave has no disk: a share not in Vault is gone at the next
		// restart, so a ceremony must fail rather than report success.
		return false, errors.New("enclave mode: VAULT_ADDR is not set, and an enclave cannot keep a share anywhere else")
	}
	if !configured {
		// No Vault: an encrypted file store, if one is configured (see
		// file_seal.go). Neither configured is still "not sealed, not an error".
		if fcfg, ok := fileShareConfigFromEnv(getenv); ok {
			if err := sealShareToFile(fcfg, partyID, ceremonyID, share, cc); err != nil {
				return false, fmt.Errorf("sealing the key share to the file store: %w", err)
			}
			return true, nil
		}
		return false, nil
	}

	client, err := newVaultClient(cfg.addr)
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
	if sealer, ok := sealerFor(partyID); ok {
		// Enclave mode: Vault receives only the KMS envelope. The share and
		// its context are inside it; the curve stays outside for operators.
		p := sealedPayload{PartyID: partyID, CeremonyID: ceremonyID, SaveData: string(raw)}
		if s, ok := payload["ceremony_context"].(string); ok {
			p.CeremonyContext = s
		}
		plain, err := json.Marshal(p)
		if err != nil {
			return false, err
		}
		env, err := sealer.Seal(ctx, plain, enclave.ShareContext(partyID, ceremonyID))
		if err != nil {
			clear(plain)
			return false, fmt.Errorf("sealing the key share with KMS: %w", err)
		}
		payload = map[string]interface{}{
			"party_id":         partyID,
			"ceremony_id":      ceremonyID,
			"curve":            string(share.Curve),
			"enclave_envelope": string(env),
		}
		if escrow, ok := escrowFor(partyID); ok {
			rec, err := escrow.Seal(plain, partyID, ceremonyID)
			if err != nil {
				clear(plain)
				return false, fmt.Errorf("sealing the key share to the recovery key: %w", err)
			}
			payload["recovery_envelope"] = string(rec)
		}
		clear(plain)
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

	client, err := newVaultClient(cfg.addr)
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

	if share, _, handled, err := openEnclaveEntry(ctx, partyID, ceremonyID, cfg.keyPath, secret.Data); handled {
		if err != nil {
			return nil, err
		}
		if share.Curve != CurveSecp256k1 || share.ECDSA == nil {
			return nil, fmt.Errorf("the share at %s is a %s share; LoadKeyShare returns secp256k1 save-data only", cfg.keyPath, share.Curve)
		}
		return share.ECDSA, nil
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
	return loadSealedShare(ctx, getenv, partyID, ceremonyID, false)
}

// LoadLegacyShareForMigration reads a share that was sealed before enclave
// mode, in the clear in Vault, so that an enclave can refresh it into an
// enclave-sealed share (see MigrateLegacyCeremony). Enclave mode only, and
// only for a Vault entry with no envelope: anything else is refused, so this
// cannot be used to read an enclave-sealed share any other way.
func LoadLegacyShareForMigration(ctx context.Context, getenv func(string) string, partyID int, ceremonyID string) (*KeyShare, *CeremonyContext, error) {
	if _, ok := sealerFor(partyID); !ok {
		return nil, nil, errors.New("migration into enclave sealing runs only in enclave mode")
	}
	return loadSealedShare(ctx, getenv, partyID, ceremonyID, true)
}

func loadSealedShare(ctx context.Context, getenv func(string) string, partyID int, ceremonyID string, legacy bool) (*KeyShare, *CeremonyContext, error) {
	cfg, configured := vaultShareConfigFromEnv(getenv, partyID, ceremonyID)
	if legacy && !configured {
		return nil, nil, errors.New("migration reads the legacy share from Vault, and VAULT_ADDR is not set")
	}
	if !configured {
		if fcfg, ok := fileShareConfigFromEnv(getenv); ok {
			return loadShareFromFile(fcfg, partyID, ceremonyID)
		}
		return nil, nil, fmt.Errorf("neither VAULT_ADDR nor SHARE_STORE_DIR is set; there is nothing sealed to restore from")
	}

	client, err := newVaultClient(cfg.addr)
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

	if legacy {
		if _, enveloped := secret.Data["enclave_envelope"]; enveloped {
			return nil, nil, fmt.Errorf("the share at %s is already enclave-sealed; there is nothing to migrate", cfg.keyPath)
		}
	} else if share, cc, handled, err := openEnclaveEntry(ctx, partyID, ceremonyID, cfg.keyPath, secret.Data); handled {
		return share, cc, err
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

// RetireSealedShare permanently destroys this party's sealed share for a
// ceremony: every version and the metadata, not a soft delete that
// `vault kv undelete` reverses. Returns (false, nil) if Vault isn't
// configured.
//
// Permanent on purpose. Retiring a share is what makes a key rotation or
// refresh worth doing: a share compromised before retirement must not be
// combinable with anything taken after it. A soft-deleted share is still
// on the disk, one command away.
//
// Each party retires only its own share, in its own Vault. The previous
// arrangement -- the orchestrator deleting every party's share with one
// Vault token -- only worked while all parties shared one Vault, and it
// meant one platform credential could reach every share.
func RetireSealedShare(ctx context.Context, getenv func(string) string, partyID int, ceremonyID string) (bool, error) {
	cfg, configured := vaultShareConfigFromEnv(getenv, partyID, ceremonyID)
	if !configured {
		if fcfg, ok := fileShareConfigFromEnv(getenv); ok {
			if err := retireShareFile(fcfg, partyID, ceremonyID); err != nil {
				return false, fmt.Errorf("destroying the sealed share file: %w", err)
			}
			return true, nil
		}
		return false, nil
	}
	client, err := newVaultClient(cfg.addr)
	if err != nil {
		return false, fmt.Errorf("vault client: %w", err)
	}
	if cfg.token != "" {
		client.SetToken(cfg.token)
	}
	if err := client.KVv2(cfg.mount).DeleteMetadata(ctx, cfg.keyPath); err != nil {
		return false, fmt.Errorf("vault destroy %s: %w", cfg.keyPath, err)
	}
	return true, nil
}

// newVaultClient builds a Vault client. In enclave mode it dials through the
// egress map and trusts the CA the host supplied.
func newVaultClient(addr string) (*vault.Client, error) {
	enclaveMu.RLock()
	ev := enclaveVault
	enclaveMu.RUnlock()
	if ev == nil || !ev.active {
		return vault.NewClient(&vault.Config{Address: addr})
	}
	cfg := vault.DefaultConfig()
	if cfg.Error != nil {
		return nil, cfg.Error
	}
	cfg.Address = addr
	if len(ev.caPEM) > 0 {
		if err := cfg.ConfigureTLS(&vault.TLSConfig{CACertBytes: ev.caPEM}); err != nil {
			return nil, err
		}
	}
	tr, ok := cfg.HttpClient.Transport.(*http.Transport)
	if !ok {
		return nil, errors.New("enclave mode: unexpected Vault transport")
	}
	tr.DialContext = ev.dial
	return vault.NewClient(cfg)
}

// openEnclaveEntry handles a Vault entry in enclave mode, or one written by
// an enclave. handled is false only for an ordinary entry read outside
// enclave mode.
func openEnclaveEntry(ctx context.Context, partyID int, ceremonyID, keyPath string, data map[string]interface{}) (*KeyShare, *CeremonyContext, bool, error) {
	envStr, hasEnvelope := data["enclave_envelope"].(string)
	sealer, enclaveMode := sealerFor(partyID)
	switch {
	case !hasEnvelope && !enclaveMode:
		return nil, nil, false, nil
	case !hasEnvelope:
		return nil, nil, true, fmt.Errorf("the share at %s was not sealed by an enclave; enclave mode refuses a share "+
			"that has sat in Vault in the clear. Move it in with a refresh ceremony instead", keyPath)
	case !enclaveMode:
		return nil, nil, true, fmt.Errorf("the share at %s was sealed by an enclave; only an enclave running the "+
			"image the KMS key policy names can open it", keyPath)
	}
	plain, err := sealer.Open(ctx, []byte(envStr), enclave.ShareContext(partyID, ceremonyID))
	if err != nil {
		return nil, nil, true, fmt.Errorf("opening the share at %s: %w", keyPath, err)
	}
	defer clear(plain)
	var p sealedPayload
	if err := json.Unmarshal(plain, &p); err != nil || p.PartyID != partyID || p.CeremonyID != ceremonyID {
		return nil, nil, true, fmt.Errorf("the enclave envelope at %s is malformed", keyPath)
	}
	share, cc, err := decodeSealedPayload(p, keyPath)
	return share, cc, true, err
}

// destroyEarlierShareVersions permanently destroys every version of a share's
// Vault entry except the current one, and returns how many it destroyed.
//
// Vault KV v2 keeps every version of an entry. After a migration the current
// version is the enclave envelope, and every earlier one is a share in the
// clear: unless they are destroyed, "migrated into the enclave" would describe
// the newest version only, and `vault kv get -version=1` would undo it.
func destroyEarlierShareVersions(ctx context.Context, getenv func(string) string, partyID int, ceremonyID string) (int, error) {
	cfg, configured := vaultShareConfigFromEnv(getenv, partyID, ceremonyID)
	if !configured {
		return 0, errors.New("VAULT_ADDR is not set")
	}
	client, err := newVaultClient(cfg.addr)
	if err != nil {
		return 0, err
	}
	if cfg.token != "" {
		client.SetToken(cfg.token)
	}
	kv := client.KVv2(cfg.mount)
	meta, err := kv.GetMetadata(ctx, cfg.keyPath)
	if err != nil {
		return 0, fmt.Errorf("reading the versions of %s: %w", cfg.keyPath, err)
	}
	var versions []int
	for k, v := range meta.Versions {
		n, err := strconv.Atoi(k)
		if err != nil || n >= meta.CurrentVersion || v.Destroyed {
			continue
		}
		versions = append(versions, n)
	}
	sort.Ints(versions)
	if len(versions) == 0 {
		return 0, nil
	}
	if err := kv.Destroy(ctx, cfg.keyPath, versions); err != nil {
		return 0, fmt.Errorf("destroying versions %v of %s: %w", versions, cfg.keyPath, err)
	}
	return len(versions), nil
}
