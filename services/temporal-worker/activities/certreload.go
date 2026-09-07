package activities

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"sync/atomic"
	"time"
)

// Reloads this worker's client certificate from disk while it runs.
//
// The certificate is issued at pod startup with a short TTL and a renewal
// sidecar replaces the files before it expires (see
// infrastructure/helm/openfireblocks/templates/temporal-worker.yaml).
// Loading the keypair exactly once, which is what this did, means the
// worker keeps presenting the original certificate until it expires and
// then cannot talk to any party -- the renewal happens on disk and is
// ignored. Short-lived certificates only help if the process holding them
// notices.
//
// This is the client-side counterpart of services/mpc-party/certreload.go.
// It is a copy rather than a shared package because these are separate Go
// modules; keeping the two in sync matters more than the duplication costs.
type clientCertReloader struct {
	certFile, keyFile, caFile string

	current   atomic.Pointer[tls.Certificate]
	caPool    *x509.CertPool
	lastStamp atomic.Pointer[string]
}

const clientCertReloadInterval = 30 * time.Second

func newClientCertReloader(certFile, keyFile, caFile string) (*clientCertReloader, error) {
	r := &clientCertReloader{certFile: certFile, keyFile: keyFile, caFile: caFile}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read mTLS CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("mTLS CA file %s contained no usable certificates", caFile)
	}
	r.caPool = pool

	if err := r.reload(); err != nil {
		return nil, err
	}
	go r.watch()
	return r, nil
}

func (r *clientCertReloader) stamp() string {
	var s string
	for _, f := range []string{r.certFile, r.keyFile} {
		fi, err := os.Stat(f)
		if err != nil {
			s += f + ":err;"
			continue
		}
		s += fmt.Sprintf("%s:%d:%d;", f, fi.Size(), fi.ModTime().UnixNano())
	}
	return s
}

func (r *clientCertReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("failed to load mTLS client cert/key: %w", err)
	}
	r.current.Store(&cert)
	stamp := r.stamp()
	r.lastStamp.Store(&stamp)
	return nil
}

// watch keeps the previous certificate on a failed reload: a half-written
// file is a state the writer will finish, and discarding a working
// certificate over it would turn a race into an outage.
func (r *clientCertReloader) watch() {
	for range time.Tick(clientCertReloadInterval) {
		last := r.lastStamp.Load()
		if last != nil && *last == r.stamp() {
			continue
		}
		if err := r.reload(); err != nil {
			log.Printf("mTLS material changed on disk but could not be loaded, keeping the previous certificate: %v", err)
			continue
		}
		log.Printf("reloaded mTLS client certificate from %s", r.certFile)
	}
}

// config returns a TLS config whose leaf follows rotation.
//
// RootCAs is a snapshot: the CA is the long-lived half, and a root rotation
// is a planned event that can afford a rolling restart, where a leaf
// expiring every day cannot.
func (r *clientCertReloader) config() *tls.Config {
	return &tls.Config{
		RootCAs:    r.caPool,
		MinVersion: tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return r.current.Load(), nil
		},
	}
}
