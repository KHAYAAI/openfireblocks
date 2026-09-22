package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A tiny CA, so these tests exercise the real certificate machinery rather
// than a mock of it: real ASN.1, real chain verification, real handshakes.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	parsed, _ := x509.ParseCertificate(der)
	return &testCA{
		cert: parsed,
		key:  key,
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// issue returns PEM cert/key for a leaf with the given serial, so a test can
// tell one generation of a certificate from the next.
func (ca *testCA) issue(t *testing.T, cn string, serial int64, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func writeMaterial(t *testing.T, dir string, certPEM, keyPEM, caPEM []byte) (cert, key, ca string) {
	t.Helper()
	cert = filepath.Join(dir, "tls.crt")
	key = filepath.Join(dir, "tls.key")
	ca = filepath.Join(dir, "ca.crt")
	for path, data := range map[string][]byte{cert: certPEM, key: keyPEM, ca: caPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return cert, key, ca
}

// The bug this guards against: loading the keypair once means the
// certificate's expiry becomes the process's lifetime. A renewer rewriting
// the files has to be enough, with no restart and no dropped listener.
func TestCertReloaderPicksUpRotatedMaterial(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()

	firstCert, firstKey := ca.issue(t, "party-1.internal", 1001, time.Now().Add(time.Hour))
	certFile, keyFile, caFile := writeMaterial(t, dir, firstCert, firstKey, ca.pem)

	r, err := newCertReloader(certFile, keyFile, caFile)
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}

	got := r.current.Load().cert.Leaf.SerialNumber.Int64()
	if got != 1001 {
		t.Fatalf("serving serial %d before rotation, want 1001", got)
	}

	// What a renewal sidecar does: same paths, new bytes.
	secondCert, secondKey := ca.issue(t, "party-1.internal", 2002, time.Now().Add(4*time.Hour))
	writeMaterial(t, dir, secondCert, secondKey, ca.pem)

	// reload() is what the watch loop calls; calling it directly keeps the
	// test off a 30-second timer.
	if err := r.reload(); err != nil {
		t.Fatalf("reload after rotation: %v", err)
	}

	got = r.current.Load().cert.Leaf.SerialNumber.Int64()
	if got != 2002 {
		t.Fatalf("still serving serial %d after rotation, want 2002 -- the process would keep presenting a certificate until it expired", got)
	}
}

// A reload that fails must not discard a working certificate: a
// half-written file is a transient state the writer will finish, and
// dropping the current material over it converts a harmless race into an
// outage.
func TestCertReloaderKeepsWorkingCertWhenNewMaterialIsUnreadable(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()

	goodCert, goodKey := ca.issue(t, "party-1.internal", 3003, time.Now().Add(time.Hour))
	certFile, keyFile, caFile := writeMaterial(t, dir, goodCert, goodKey, ca.pem)

	r, err := newCertReloader(certFile, keyFile, caFile)
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}

	if err := os.WriteFile(certFile, []byte("-----BEGIN CERTIFICATE-----\ntruncated"), 0o600); err != nil {
		t.Fatalf("write partial cert: %v", err)
	}
	if err := r.reload(); err == nil {
		t.Fatal("reload accepted a truncated certificate")
	}

	if got := r.current.Load().cert.Leaf.SerialNumber.Int64(); got != 3003 {
		t.Fatalf("serving serial %d after a failed reload, want the previous 3003", got)
	}
}

// The property that actually matters: a live mTLS server presents the new
// certificate to a *new connection* after rotation, without being
// restarted. Everything above is bookkeeping; this is the behaviour.
func TestServerPresentsRotatedCertificateWithoutRestart(t *testing.T) {
	ca := newTestCA(t)
	dir := t.TempDir()

	firstCert, firstKey := ca.issue(t, "localhost", 4004, time.Now().Add(time.Hour))
	certFile, keyFile, caFile := writeMaterial(t, dir, firstCert, firstKey, ca.pem)

	r, err := newCertReloader(certFile, keyFile, caFile)
	if err != nil {
		t.Fatalf("newCertReloader: %v", err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	srv.TLS = r.serverConfig()
	srv.StartTLS()
	defer srv.Close()

	// A client that trusts the CA and presents its own certificate, since
	// the server requires one.
	clientOf := func() *http.Client {
		clientCert, clientKey := ca.issue(t, "client.internal", 9009, time.Now().Add(time.Hour))
		pair, err := tls.X509KeyPair(clientCert, clientKey)
		if err != nil {
			t.Fatalf("client keypair: %v", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(ca.pem)
		return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{pair},
			RootCAs:      pool,
			ServerName:   "localhost",
			MinVersion:   tls.VersionTLS13,
		}}}
	}

	serialSeen := func() int64 {
		c := clientOf()
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.TLS.PeerCertificates[0].SerialNumber.Int64()
	}

	if got := serialSeen(); got != 4004 {
		t.Fatalf("server presented serial %d, want 4004", got)
	}

	rotatedCert, rotatedKey := ca.issue(t, "localhost", 5005, time.Now().Add(4*time.Hour))
	writeMaterial(t, dir, rotatedCert, rotatedKey, ca.pem)
	if err := r.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := serialSeen(); got != 5005 {
		t.Fatalf("server still presenting serial %d after rotation, want 5005 -- rotation requires a restart, which is the bug", got)
	}
}
