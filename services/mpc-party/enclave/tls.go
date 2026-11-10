package enclave

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"

	"openfireblocks.com/services/mpc-party/nitroattest"
)

// ServerOptions configure the party's TLS server inside the enclave.
type ServerOptions struct {
	Identity *Identity
	Verifier *nitroattest.Verifier
	// OrchestratorCAs verifies callers that are not parties (the
	// orchestrator), which present a certificate from the platform PKI.
	// Optional: nil means only attested parties may connect.
	OrchestratorCAs *x509.CertPool
	// OrchestratorNames are the common names a PKI-issued client certificate
	// may carry. Default temporal-worker.internal.
	OrchestratorNames []string
}

// ServerTLSConfig requires a client certificate on every connection and
// accepts exactly two kinds:
//
//   - an attested party certificate that passes the policy (the peers);
//   - a certificate from the platform PKI whose common name is an
//     orchestrator name (the orchestrator, which never sees a share).
//
// A PKI-issued certificate naming a party is refused: in enclave mode a
// party is only ever an enclave, so a party-N certificate from the PKI is a
// party running outside one, or a stolen certificate.
func ServerTLSConfig(o ServerOptions) (*tls.Config, error) {
	if o.Identity == nil || o.Verifier == nil {
		return nil, errors.New("enclave TLS: identity and verifier are required")
	}
	names := o.OrchestratorNames
	if len(names) == 0 {
		names = []string{"temporal-worker.internal"}
	}
	return &tls.Config{
		MinVersion:     tls.VersionTLS13,
		GetCertificate: o.Identity.GetCertificate,
		ClientAuth:     tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			return verifyClient(raw, o.Verifier, o.OrchestratorCAs, names)
		},
	}, nil
}

func verifyClient(raw [][]byte, v *nitroattest.Verifier, cas *x509.CertPool, names []string) error {
	if len(raw) == 0 {
		return errors.New("enclave TLS: no client certificate")
	}
	leaf, err := x509.ParseCertificate(raw[0])
	if err != nil {
		return fmt.Errorf("enclave TLS: client certificate: %w", err)
	}
	if nitroattest.HasAttestation(leaf) {
		_, err := v.VerifyCertificate(leaf)
		return err
	}
	if cas == nil {
		return errors.New("enclave TLS: the client is not an attested party, and no orchestrator CA is configured")
	}
	if _, isParty := nitroattest.PartyFromName(leaf.Subject.CommonName); isParty {
		return fmt.Errorf("enclave TLS: refusing a PKI certificate for %q: in enclave mode a party must present an attested certificate", leaf.Subject.CommonName)
	}
	if !slices.Contains(names, leaf.Subject.CommonName) {
		return fmt.Errorf("enclave TLS: %q is not an orchestrator name", leaf.Subject.CommonName)
	}
	inter := x509.NewCertPool()
	for _, r := range raw[1:] {
		c, err := x509.ParseCertificate(r)
		if err != nil {
			return fmt.Errorf("enclave TLS: client chain: %w", err)
		}
		inter.AddCert(c)
	}
	_, err = leaf.Verify(x509.VerifyOptions{Roots: cas, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	if err != nil {
		return fmt.Errorf("enclave TLS: orchestrator certificate: %w", err)
	}
	return nil
}

// PeerClientTLSConfig is for the party's calls to other parties: it
// presents this enclave's attested certificate and accepts only a server
// that is the attested party it dialled.
func PeerClientTLSConfig(id *Identity, v *nitroattest.Verifier) *tls.Config {
	return v.ClientTLSConfig(id.GetClientCertificate)
}
