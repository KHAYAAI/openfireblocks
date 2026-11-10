package activities

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge-crypto/temporal-worker/internal/nitroattest"
)

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func issue(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn}, DNSNames: []string{cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c, key, der
}

// With an attestation policy, a party-N host must attest; a PKI certificate
// for party-N is refused, while other hosts still verify against the PKI.
func TestAttestedTransportRoutesByHostName(t *testing.T) {
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	writePEM(t, filepath.Join(dir, "ca.pem"), "CERTIFICATE", caDER)
	_, ck, cder := issue(t, ca, caKey, "temporal-worker.internal")
	writePEM(t, filepath.Join(dir, "c.pem"), "CERTIFICATE", cder)
	kder, _ := x509.MarshalECPrivateKey(ck)
	writePEM(t, filepath.Join(dir, "k.pem"), "EC PRIVATE KEY", kder)
	r, err := newClientCertReloader(filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem"), filepath.Join(dir, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}

	servers := map[string]string{}
	for _, name := range []string{"party-1.internal", "policy.internal"} {
		_, sk, sder := issue(t, ca, caKey, name)
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{sder}, PrivateKey: sk}}}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		servers[name] = srv.Listener.Addr().String()
	}
	old := attestedDial
	attestedDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		return (&net.Dialer{}).DialContext(ctx, network, servers[host])
	}
	t.Cleanup(func() { attestedDial = old })

	policy := nitroattest.Policy{ImagePCR0: []string{strings.Repeat("ab", 48)}}
	client := &http.Client{Transport: r.attestedTransport(&nitroattest.Verifier{Policy: policy}), Timeout: 5 * time.Second}

	resp, err := client.Get("https://policy.internal:443/")
	if err != nil {
		t.Fatalf("a non-party host with a PKI certificate was refused: %v", err)
	}
	resp.Body.Close()

	if _, err := client.Get("https://party-1.internal:443/"); err == nil || !strings.Contains(err.Error(), "no attestation document") {
		t.Fatalf("a party with only a PKI certificate was accepted: %v", err)
	}
}

func TestAttestationPolicyRequiresMTLS(t *testing.T) {
	sharedReloader = nil
	t.Setenv(envMTLSCertFile, "")
	t.Setenv(envPartyAttestationPolicy, "/nonexistent")
	if _, _, err := mtlsTransportFromEnv(); err == nil {
		t.Fatal("attestation policy accepted without mTLS")
	}
}
