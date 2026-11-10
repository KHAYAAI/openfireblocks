package nitroattest

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"regexp"
	"strconv"
)

// OIDAttestationExtension is the X.509 extension that carries an attestation
// document in a party's self-signed TLS certificate.
//
// A placeholder arc: FORGE has no IANA Private Enterprise Number yet, and a
// 2.25 (UUID) arc cannot be expressed, because Go's ASN.1 and X.509 code
// limit each OID component to an int. Replace it once a number is assigned
// (registration is free); peers and the orchestrator must change together.
// A collision would be harmless: only certificates this service makes are
// read, and the document inside is verified whatever the OID says.
var OIDAttestationExtension = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 7001, 1}

// HasAttestation reports whether a certificate carries an attestation document.
func HasAttestation(cert *x509.Certificate) bool {
	_, ok := attestationExtension(cert)
	return ok
}

func attestationExtension(cert *x509.Certificate) ([]byte, bool) {
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(OIDAttestationExtension) {
			return ext.Value, true
		}
	}
	return nil, false
}

var partyNamePattern = regexp.MustCompile(`^party-(\d+)(\.|$)`)

// PartyFromName reads the party number from a name such as party-2.internal.
func PartyFromName(name string) (int, bool) {
	m := partyNamePattern.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// VerifyCertificate checks a party's self-signed TLS certificate: that it
// carries an attestation document which passes the policy, that the document
// binds this certificate's public key, and that the common name names the
// same party the document does. It returns the attested party number.
//
// The certificate's own validity period is checked too. Its signature is not
// worth checking (it is self-signed, so it proves nothing a valid document
// does not); the TLS handshake proves the peer holds the private key.
func (v *Verifier) VerifyCertificate(cert *x509.Certificate) (*Attested, error) {
	doc, ok := attestationExtension(cert)
	if !ok {
		return nil, refuse("the certificate carries no attestation document")
	}
	now := v.now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, refuse("the certificate is outside its validity period (%s to %s)", cert.NotBefore, cert.NotAfter)
	}
	att, err := v.Verify(doc, PurposePartyTLS)
	if err != nil {
		return nil, err
	}
	if len(att.PublicKey) == 0 || !bytes.Equal(att.PublicKey, cert.RawSubjectPublicKeyInfo) {
		return nil, refuse("the attestation document binds a different public key from the certificate's")
	}
	cnParty, ok := PartyFromName(cert.Subject.CommonName)
	if !ok {
		return nil, refuse("the certificate's common name %q does not name a party", cert.Subject.CommonName)
	}
	if cnParty != att.Claims.PartyID {
		return nil, refuse("the certificate says party %d but the enclave attested party %d", cnParty, att.Claims.PartyID)
	}
	return att, nil
}

// VerifyServerConnection is a tls.Config.VerifyConnection hook for a client
// calling a party that runs in an enclave. The server must present an
// attested certificate, and the party it attests must be the one the client
// dialled (the party-N in the server name).
//
// Use it with InsecureSkipVerify set: the Web PKI chain check is replaced by
// this one, never skipped. Go still runs VerifyConnection when
// InsecureSkipVerify is set.
func (v *Verifier) VerifyServerConnection(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return refuse("the server presented no certificate")
	}
	want, ok := PartyFromName(cs.ServerName)
	if !ok {
		return refuse("the server name %q does not name a party, so there is no party to check the enclave against", cs.ServerName)
	}
	att, err := v.VerifyCertificate(cs.PeerCertificates[0])
	if err != nil {
		return err
	}
	if att.Claims.PartyID != want {
		return refuse("dialled party %d but reached an enclave attesting party %d", want, att.Claims.PartyID)
	}
	return nil
}

// ClientTLSConfig returns a client configuration that accepts only a server
// whose attested certificate passes the policy. clientCert, if set, supplies
// the client's own certificate for mutual TLS.
func (v *Verifier) ClientTLSConfig(clientCert func(*tls.CertificateRequestInfo) (*tls.Certificate, error)) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Not a skipped check: VerifyServerConnection replaces the chain check.
		InsecureSkipVerify:   true, //nolint:gosec // replaced by attestation, see VerifyServerConnection
		VerifyConnection:     v.VerifyServerConnection,
		GetClientCertificate: clientCert,
	}
}

// Describe summarises an attested peer for logs.
func (a *Attested) Describe() string {
	return fmt.Sprintf("party %d, enclave image %x, module %s", a.Claims.PartyID, a.PCRs[0], a.ModuleID)
}
