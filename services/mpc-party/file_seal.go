package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Key shares sealed to an encrypted file, for deployments with no Vault.
//
// Before this, a party with VAULT_ADDR unset kept its share in memory only:
// a restart lost it, and with it the ability to sign for that key. A
// sovereign deployment may have no Vault, so there needs to be another way to
// keep a share across restarts without ever writing it in the clear.
//
//	SHARE_STORE_DIR       where sealed shares are written (mode 0700)
//	SHARE_STORE_KEY_FILE  a 32-byte key, as raw bytes or 64 hex characters
//
// AES-256-GCM. The additional authenticated data names the party and the
// ceremony, so a sealed file copied to another party's directory, or renamed
// to another ceremony, fails to open instead of being accepted as that
// party's share. Any change to the file fails authentication.
//
// What this does not do, plainly: a key file on the same disk as the sealed
// shares protects against a copied backup or a discarded disk, not against
// someone with a shell on the host. Put the key file on a separate secret
// mount, a tmpfs filled at boot, or unwrap it from an HSM/KMS. Vault remains
// the preferred store; this is the fallback when there is none.

const sealedFileVersion = 1

type sealedFile struct {
	Version int    `json:"v"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
}

type fileShareConfig struct {
	dir     string
	keyFile string
}

func fileShareConfigFromEnv(getenv func(string) string) (fileShareConfig, bool) {
	dir := strings.TrimSpace(getenv("SHARE_STORE_DIR"))
	if dir == "" {
		return fileShareConfig{}, false
	}
	return fileShareConfig{dir: dir, keyFile: strings.TrimSpace(getenv("SHARE_STORE_KEY_FILE"))}, true
}

// loadShareKey reads the sealing key and refuses one that is readable by
// anyone but its owner or that sits inside the store it protects.
func (c fileShareConfig) loadShareKey() ([]byte, error) {
	if c.keyFile == "" {
		return nil, errors.New("SHARE_STORE_DIR is set but SHARE_STORE_KEY_FILE is not: refusing to write shares without a key")
	}
	info, err := os.Stat(c.keyFile)
	if err != nil {
		return nil, fmt.Errorf("share store key file: %w", err)
	}
	if info.Mode().Perm()&0o007 != 0 {
		return nil, fmt.Errorf("share store key file %s is readable by other users (mode %o); it must not be world-accessible (0640 or stricter; Kubernetes adds group read to mounted Secrets when fsGroup is set)", c.keyFile, info.Mode().Perm())
	}
	if abs, e1 := filepath.Abs(c.keyFile); e1 == nil {
		if dir, e2 := filepath.Abs(c.dir); e2 == nil && strings.HasPrefix(abs, dir+string(os.PathSeparator)) {
			return nil, errors.New("the share store key file must not live inside the share store directory")
		}
	}
	raw, err := os.ReadFile(c.keyFile)
	if err != nil {
		return nil, fmt.Errorf("share store key file: %w", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 64 {
		if k, e := hex.DecodeString(trimmed); e == nil {
			return k, nil
		}
	}
	if len(raw) == 32 {
		return raw, nil
	}
	return nil, errors.New("share store key must be 32 bytes, as raw bytes or 64 hex characters")
}

func (c fileShareConfig) path(partyID int, ceremonyID string) (string, error) {
	// A ceremony id becomes a file name: nothing that could climb out of
	// the directory.
	if ceremonyID == "" || strings.ContainsAny(ceremonyID, "/\\\x00") || ceremonyID == "." || ceremonyID == ".." {
		return "", fmt.Errorf("ceremony id %q cannot be used as a file name", ceremonyID)
	}
	return filepath.Join(c.dir, fmt.Sprintf("party-%d", partyID), ceremonyID+".sealed"), nil
}

func shareAAD(partyID int, ceremonyID string) []byte {
	return []byte(fmt.Sprintf("openfireblocks-share-v%d|party-%d|%s", sealedFileVersion, partyID, ceremonyID))
}

// sealedPayload is what is encrypted: the same fields the Vault entry holds.
type sealedPayload struct {
	PartyID         int    `json:"party_id"`
	CeremonyID      string `json:"ceremony_id"`
	SaveData        string `json:"save_data"`
	CeremonyContext string `json:"ceremony_context,omitempty"`
}

func sealShareToFile(cfg fileShareConfig, partyID int, ceremonyID string, share *KeyShare, cc *CeremonyContext) error {
	key, err := cfg.loadShareKey()
	if err != nil {
		return err
	}
	path, err := cfg.path(partyID, ceremonyID)
	if err != nil {
		return err
	}
	raw, err := MarshalShare(share)
	if err != nil {
		return fmt.Errorf("marshal key share: %w", err)
	}
	p := sealedPayload{PartyID: partyID, CeremonyID: ceremonyID, SaveData: string(raw)}
	if cc != nil {
		b, err := json.Marshal(cc)
		if err != nil {
			return fmt.Errorf("marshal ceremony context: %w", err)
		}
		p.CeremonyContext = string(b)
	}
	plain, err := json.Marshal(p)
	if err != nil {
		return err
	}

	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	out, err := json.Marshal(sealedFile{
		Version: sealedFileVersion,
		Nonce:   hex.EncodeToString(nonce),
		Data:    hex.EncodeToString(gcm.Seal(nil, nonce, plain, shareAAD(partyID, ceremonyID))),
	})
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Temp file then rename, so a crash mid-write never leaves a truncated
	// share where a good one used to be.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sealing-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func loadShareFromFile(cfg fileShareConfig, partyID int, ceremonyID string) (*KeyShare, *CeremonyContext, error) {
	key, err := cfg.loadShareKey()
	if err != nil {
		return nil, nil, err
	}
	path, err := cfg.path(partyID, ceremonyID)
	if err != nil {
		return nil, nil, err
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("no sealed share for party %d ceremony %s: %w", partyID, ceremonyID, err)
	}
	var f sealedFile
	if err := json.Unmarshal(blob, &f); err != nil || f.Version != sealedFileVersion {
		return nil, nil, fmt.Errorf("the sealed share at %s is not a version %d sealed file", path, sealedFileVersion)
	}
	nonce, err1 := hex.DecodeString(f.Nonce)
	data, err2 := hex.DecodeString(f.Data)
	gcm, err3 := newGCM(key)
	if err1 != nil || err2 != nil || err3 != nil || len(nonce) != gcm.NonceSize() {
		return nil, nil, fmt.Errorf("the sealed share at %s is malformed", path)
	}
	plain, err := gcm.Open(nil, nonce, data, shareAAD(partyID, ceremonyID))
	if err != nil {
		return nil, nil, fmt.Errorf("the sealed share at %s does not open: wrong key, altered, or not this party's share for this ceremony", path)
	}
	var p sealedPayload
	if err := json.Unmarshal(plain, &p); err != nil || p.PartyID != partyID || p.CeremonyID != ceremonyID {
		return nil, nil, fmt.Errorf("the sealed share at %s is malformed", path)
	}
	share, err := UnmarshalShare([]byte(p.SaveData))
	if err != nil {
		return nil, nil, fmt.Errorf("sealed share at %s: %w", path, err)
	}
	var cc *CeremonyContext
	if p.CeremonyContext != "" {
		var parsed CeremonyContext
		if err := json.Unmarshal([]byte(p.CeremonyContext), &parsed); err != nil {
			return nil, nil, fmt.Errorf("sealed ceremony context at %s is malformed: %w", path, err)
		}
		cc = &parsed
	}
	return share, cc, nil
}

// retireShareFile destroys this party's sealed file. Overwritten before
// removal so a plain undelete of the directory entry recovers nothing; the
// ciphertext is useless without the key either way.
func retireShareFile(cfg fileShareConfig, partyID int, ceremonyID string) error {
	path, err := cfg.path(partyID, ceremonyID)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		_, _ = f.Write(make([]byte, info.Size()))
		_ = f.Sync()
		_ = f.Close()
	}
	return os.Remove(path)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
