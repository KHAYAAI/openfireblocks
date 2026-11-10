package enclave

import (
	"crypto/hpke"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Escrow is the break-glass path for enclave-sealed shares.
//
// A share sealed only to KMS is lost with the KMS key: deleted, its region
// gone, the AWS account closed, or AWS itself unavailable to the customer.
// Escrow also encrypts each party's share to an offline recovery key held by
// that party's recovery officers, outside AWS, so the procedure in
// docs/deployment/KEY-RECOVERY.md still works without the vendor and
// without the cloud.
//
// The public keys are baked into the image (ENCLAVE_RECOVERY_PUBKEYS), so
// the host cannot substitute one of its own. Each party has its own recovery
// key, and they must be held by different people: whoever holds threshold
// many recovery private keys can reconstruct the key, exactly as whoever
// held threshold many Vault unseal shares could before enclave mode.
//
// The ciphersuite is HPKE (RFC 9180) with the hybrid ML-KEM-768 + X25519
// KEM, HKDF-SHA256 and AES-256-GCM, so a recovery envelope recorded today
// is not opened by a future quantum computer breaking X25519 alone. The HPKE
// info string binds the party and ceremony.
type Escrow struct {
	pub hpke.PublicKey
}

const escrowVersion = 1

var (
	escrowKEM  = hpke.MLKEM768X25519()
	escrowKDF  = hpke.HKDFSHA256()
	escrowAEAD = hpke.AES256GCM()
)

// RecoveryEnvelope is what is stored beside the KMS envelope.
type RecoveryEnvelope struct {
	Version    int    `json:"v"`
	KEM        uint16 `json:"kem"`
	Ciphertext []byte `json:"ciphertext"`
}

func escrowInfo(partyID int, ceremonyID string) []byte {
	return []byte(fmt.Sprintf("forge-recovery-v%d|party-%d|%d:%s", escrowVersion, partyID, len(ceremonyID), ceremonyID))
}

// NewEscrow parses a recovery public key (hex, as RecoveryKeygen prints it).
func NewEscrow(pubHex string) (*Escrow, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(pubHex))
	if err != nil {
		return nil, errors.New("recovery public key is not hex")
	}
	pub, err := escrowKEM.NewPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("recovery public key: %w", err)
	}
	return &Escrow{pub: pub}, nil
}

// Seal encrypts plaintext to the recovery key, for one party and ceremony.
func (e *Escrow) Seal(plaintext []byte, partyID int, ceremonyID string) ([]byte, error) {
	ct, err := hpke.Seal(e.pub, escrowKDF, escrowAEAD, escrowInfo(partyID, ceremonyID), plaintext)
	if err != nil {
		return nil, err
	}
	return json.Marshal(RecoveryEnvelope{Version: escrowVersion, KEM: escrowKEM.ID(), Ciphertext: ct})
}

// RecoveryKeygen makes a recovery key pair. The private key is for an
// offline machine; the public key goes into the image build.
func RecoveryKeygen() (privHex, pubHex string, err error) {
	k, err := escrowKEM.GenerateKey()
	if err != nil {
		return "", "", err
	}
	priv, err := k.Bytes()
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(priv), hex.EncodeToString(k.PublicKey().Bytes()), nil
}

// OpenRecovery decrypts a recovery envelope with the offline private key. It
// runs on the recovery officers' air-gapped machine, never in a party.
func OpenRecovery(privHex string, envelope []byte, partyID int, ceremonyID string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(privHex))
	if err != nil {
		return nil, errors.New("recovery private key is not hex")
	}
	priv, err := escrowKEM.NewPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("recovery private key: %w", err)
	}
	var env RecoveryEnvelope
	if err := json.Unmarshal(envelope, &env); err != nil || env.Version != escrowVersion || env.KEM != escrowKEM.ID() {
		return nil, fmt.Errorf("not a version %d recovery envelope", escrowVersion)
	}
	plain, err := hpke.Open(priv, escrowKDF, escrowAEAD, escrowInfo(partyID, ceremonyID), env.Ciphertext)
	if err != nil {
		return nil, errors.New("the recovery envelope does not open: wrong key, altered, or another party's or ceremony's")
	}
	return plain, nil
}
