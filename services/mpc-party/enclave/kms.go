package enclave

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/edgebitio/nitro-enclaves-sdk-go/crypto/cms"

	"openfireblocks.com/services/mpc-party/nitroattest"
)

// KMSAPI is the part of the AWS KMS client the sealer uses.
type KMSAPI interface {
	GenerateDataKey(ctx context.Context, in *kms.GenerateDataKeyInput, opts ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error)
	Decrypt(ctx context.Context, in *kms.DecryptInput, opts ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// Sealer encrypts key shares with a data key that AWS KMS releases only to
// an attested enclave.
//
// Seal asks KMS for a new data key with a Recipient: an attestation document
// whose public_key is an RSA key generated inside this enclave. KMS checks
// the document against the key policy (kms:RecipientAttestation:ImageSha384
// and friends), and returns the data key only as CiphertextForRecipient,
// encrypted to that RSA key, so the host relaying the response cannot read
// it. The share is encrypted with AES-256-GCM under the data key, and what
// is stored is the KMS-encrypted data key and the ciphertext.
//
// Open sends the encrypted data key back to KMS's Decrypt with a fresh
// Recipient. A different image, or the same image in debug mode, gets a
// refusal from KMS: the key policy is what enforces this, not this code.
//
// The encryption context (party, ceremony) is bound both into the KMS
// ciphertext and into the GCM additional data, so a share cannot be opened
// as another party's or another ceremony's.
type Sealer struct {
	KMS      KMSAPI
	KeyID    string
	Attester Attester
	PartyID  int

	once   sync.Once
	rsaKey *rsa.PrivateKey
	keyErr error
}

// envelopeVersion is the stored format.
const envelopeVersion = 1

// Envelope is what is stored for a sealed share.
type Envelope struct {
	Version      int    `json:"v"`
	KeyID        string `json:"key_id"`
	EncryptedKey []byte `json:"encrypted_key"`
	Nonce        []byte `json:"nonce"`
	Ciphertext   []byte `json:"ciphertext"`
}

func (s *Sealer) recipientKey() (*rsa.PrivateKey, error) {
	s.once.Do(func() {
		// RSA-2048 is what KMS's RSAES_OAEP_SHA_256 recipient mode takes. The
		// key exists only in enclave memory and only for this process.
		s.rsaKey, s.keyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	return s.rsaKey, s.keyErr
}

func (s *Sealer) recipient() (*kmstypes.RecipientInfo, *rsa.PrivateKey, error) {
	if s.Attester == nil {
		return nil, nil, errors.New("enclave sealer: no attester")
	}
	key, err := s.recipientKey()
	if err != nil {
		return nil, nil, err
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	claims := nitroattest.Claims{Version: nitroattest.ClaimsVersion, Purpose: nitroattest.PurposeKMS, PartyID: s.PartyID}
	doc, err := s.Attester.Attest(claims.Marshal(), nil, pub)
	if err != nil {
		return nil, nil, fmt.Errorf("enclave sealer: attestation for KMS failed: %w", err)
	}
	return &kmstypes.RecipientInfo{
		AttestationDocument:    doc,
		KeyEncryptionAlgorithm: kmstypes.KeyEncryptionMechanismRsaesOaepSha256,
	}, key, nil
}

// cmsMu serialises the CMS parser: crypto/cms (taken from mozilla-services/
// pkcs7) updates a package-level indentation counter on every decode, so
// two concurrent decodes are a data race. One party can seal two ceremonies
// at once, and the race detector found it doing so.
var cmsMu sync.Mutex

// openRecipientBlob decrypts KMS's CiphertextForRecipient (CMS enveloped
// data) with the enclave's RSA key.
func openRecipientBlob(key *rsa.PrivateKey, blob []byte) (out []byte, err error) {
	if len(blob) == 0 {
		return nil, errors.New("enclave sealer: KMS returned no CiphertextForRecipient; " +
			"refusing to continue (a response without it means the Recipient was ignored)")
	}
	// The CMS parser indexes into attacker-influenced lengths; turn a panic
	// into an error.
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("enclave sealer: malformed CiphertextForRecipient (%v)", r)
		}
	}()
	cmsMu.Lock()
	dk, err := cms.DecryptEnvelopedKey(key, blob)
	cmsMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("enclave sealer: opening CiphertextForRecipient: %w", err)
	}
	if len(dk) != 32 {
		return nil, fmt.Errorf("enclave sealer: data key is %d bytes, expected 32", len(dk))
	}
	return dk, nil
}

// Seal encrypts plaintext under a new KMS data key.
func (s *Sealer) Seal(ctx context.Context, plaintext []byte, ec map[string]string) ([]byte, error) {
	rcpt, key, err := s.recipient()
	if err != nil {
		return nil, err
	}
	out, err := s.KMS.GenerateDataKey(ctx, &kms.GenerateDataKeyInput{
		KeyId:             &s.KeyID,
		KeySpec:           kmstypes.DataKeySpecAes256,
		EncryptionContext: ec,
		Recipient:         rcpt,
	})
	if err != nil {
		return nil, fmt.Errorf("enclave sealer: KMS GenerateDataKey: %w", err)
	}
	if len(out.Plaintext) != 0 {
		return nil, errors.New("enclave sealer: KMS returned the data key in plaintext despite the Recipient; refusing to use it")
	}
	if len(out.CiphertextBlob) == 0 {
		return nil, errors.New("enclave sealer: KMS returned no encrypted data key")
	}
	dk, err := openRecipientBlob(key, out.CiphertextForRecipient)
	if err != nil {
		return nil, err
	}
	defer clear(dk)
	gcm, err := gcmFor(dk)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	env := Envelope{
		Version:      envelopeVersion,
		KeyID:        s.KeyID,
		EncryptedKey: out.CiphertextBlob,
		Nonce:        nonce,
		Ciphertext:   gcm.Seal(nil, nonce, plaintext, contextAAD(ec)),
	}
	return json.Marshal(env)
}

// Open decrypts an envelope made by Seal with the same encryption context.
func (s *Sealer) Open(ctx context.Context, sealed []byte, ec map[string]string) ([]byte, error) {
	var env Envelope
	if err := json.Unmarshal(sealed, &env); err != nil || env.Version != envelopeVersion {
		return nil, fmt.Errorf("enclave sealer: not a version %d envelope", envelopeVersion)
	}
	if len(env.EncryptedKey) == 0 || len(env.Ciphertext) == 0 {
		return nil, errors.New("enclave sealer: envelope is incomplete")
	}
	rcpt, key, err := s.recipient()
	if err != nil {
		return nil, err
	}
	keyID := env.KeyID
	if keyID == "" {
		keyID = s.KeyID
	}
	out, err := s.KMS.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob:    env.EncryptedKey,
		KeyId:             &keyID,
		EncryptionContext: ec,
		Recipient:         rcpt,
	})
	if err != nil {
		return nil, fmt.Errorf("enclave sealer: KMS Decrypt: %w", err)
	}
	if len(out.Plaintext) != 0 {
		return nil, errors.New("enclave sealer: KMS returned the data key in plaintext despite the Recipient; refusing to use it")
	}
	dk, err := openRecipientBlob(key, out.CiphertextForRecipient)
	if err != nil {
		return nil, err
	}
	defer clear(dk)
	gcm, err := gcmFor(dk)
	if err != nil {
		return nil, err
	}
	if len(env.Nonce) != gcm.NonceSize() {
		return nil, errors.New("enclave sealer: envelope nonce is malformed")
	}
	plain, err := gcm.Open(nil, env.Nonce, env.Ciphertext, contextAAD(ec))
	if err != nil {
		return nil, errors.New("enclave sealer: envelope does not open: altered, or sealed under another context")
	}
	return plain, nil
}

func gcmFor(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// contextAAD is a canonical encoding of the encryption context.
func contextAAD(ec map[string]string) []byte {
	keys := make([]string, 0, len(ec))
	for k := range ec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("forge-enclave-envelope-v1")
	for _, k := range keys {
		fmt.Fprintf(&b, "|%d:%s=%d:%s", len(k), k, len(ec[k]), ec[k])
	}
	return []byte(b.String())
}

// ShareContext is the encryption context for one party's share of one ceremony.
func ShareContext(partyID int, ceremonyID string) map[string]string {
	return map[string]string{
		"forge:purpose":  "mpc-key-share",
		"forge:party":    fmt.Sprintf("party-%d", partyID),
		"forge:ceremony": ceremonyID,
	}
}
