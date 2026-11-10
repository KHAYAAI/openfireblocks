package enclave

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"openfireblocks.com/services/mpc-party/nitroattest"
)

// Identity is a party's TLS identity inside the enclave. The private key is
// generated in enclave memory and never leaves it. The certificate is
// self-signed and carries an attestation document whose public_key field is
// the certificate's public key, so a peer that verifies the document knows
// the key belongs to a measured enclave image, and the TLS handshake proves
// the peer on the other end holds it.
//
// The key and certificate are replaced every Refresh, well inside the
// verifier's maximum document age.
type Identity struct {
	PartyID  int
	attester Attester
	validity time.Duration
	refresh  time.Duration
	now      func() time.Time

	mu     sync.Mutex
	cert   *tls.Certificate
	issued time.Time
}

// IdentityOptions tune an Identity. Zero values take the defaults.
type IdentityOptions struct {
	// Validity of each certificate. Default 2 hours.
	Validity time.Duration
	// Refresh interval. Default 1 hour. Must be shorter than Validity.
	Refresh time.Duration
	// Now overrides the clock. Tests only.
	Now func() time.Time
}

// NewIdentity mints the first certificate. It fails if the attester does.
func NewIdentity(partyID int, a Attester, o IdentityOptions) (*Identity, error) {
	if partyID <= 0 {
		return nil, errors.New("enclave identity: party id must be positive")
	}
	if a == nil {
		return nil, errors.New("enclave identity: no attester")
	}
	id := &Identity{PartyID: partyID, attester: a, validity: o.Validity, refresh: o.Refresh, now: o.Now}
	if id.validity == 0 {
		id.validity = 2 * time.Hour
	}
	if id.refresh == 0 {
		id.refresh = time.Hour
	}
	if id.refresh >= id.validity {
		return nil, errors.New("enclave identity: refresh must be shorter than validity")
	}
	if id.now == nil {
		id.now = time.Now
	}
	if _, err := id.Certificate(); err != nil {
		return nil, err
	}
	return id, nil
}

// CommonName is the name the party's certificate carries.
func (id *Identity) CommonName() string { return fmt.Sprintf("party-%d.internal", id.PartyID) }

// Certificate returns the current certificate, minting a new one when the
// current one is due for refresh. If minting fails while the old certificate
// is still valid, the old one is returned with no error, so a transient NSM
// failure does not take the party down; once it has expired, the error is
// returned and handshakes fail.
func (id *Identity) Certificate() (*tls.Certificate, error) {
	id.mu.Lock()
	defer id.mu.Unlock()
	now := id.now()
	if id.cert != nil && now.Sub(id.issued) < id.refresh {
		return id.cert, nil
	}
	c, err := id.mint(now)
	if err != nil {
		if id.cert != nil && now.Before(id.cert.Leaf.NotAfter) {
			return id.cert, nil
		}
		return nil, err
	}
	id.cert, id.issued = c, now
	return c, nil
}

func (id *Identity) mint(now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	claims := nitroattest.Claims{Version: nitroattest.ClaimsVersion, Purpose: nitroattest.PurposePartyTLS, PartyID: id.PartyID}
	doc, err := id.attester.Attest(claims.Marshal(), nil, spki)
	if err != nil {
		return nil, fmt.Errorf("enclave identity: attestation failed: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:    serial,
		Subject:         pkix.Name{CommonName: id.CommonName()},
		DNSNames:        []string{id.CommonName(), fmt.Sprintf("party-%d", id.PartyID)},
		NotBefore:       now.Add(-time.Minute),
		NotAfter:        now.Add(id.validity),
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtKeyUsage:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		ExtraExtensions: []pkix.Extension{{Id: nitroattest.OIDAttestationExtension, Value: doc}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

// GetCertificate is a tls.Config.GetCertificate hook.
func (id *Identity) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return id.Certificate()
}

// GetClientCertificate is a tls.Config.GetClientCertificate hook.
func (id *Identity) GetClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return id.Certificate()
}
