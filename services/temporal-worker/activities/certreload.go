package activities

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"net/http"
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

	current   atomic.Pointer[clientBundle]
	lastStamp atomic.Pointer[string]
}

// One consistent snapshot of both halves, swapped wholesale, so a
// handshake never pairs a leaf from one generation with a CA pool from
// another.
type clientBundle struct {
	cert   tls.Certificate
	caPool *x509.CertPool
}

const clientCertReloadInterval = 30 * time.Second

func newClientCertReloader(certFile, keyFile, caFile string) (*clientCertReloader, error) {
	r := &clientCertReloader{certFile: certFile, keyFile: keyFile, caFile: caFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	go r.watch()
	return r, nil
}

func (r *clientCertReloader) stamp() string {
	var s string
	for _, f := range []string{r.certFile, r.keyFile, r.caFile} {
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
	caPEM, err := os.ReadFile(r.caFile)
	if err != nil {
		return fmt.Errorf("failed to read mTLS CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("mTLS CA file %s contained no usable certificates", r.caFile)
	}
	r.current.Store(&clientBundle{cert: cert, caPool: pool})
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
// Prefer transport() where an http.Client is being built: this config's
// RootCAs is a snapshot, and see transport() for why that is not good
// enough.
func (r *clientCertReloader) config() *tls.Config {
	b := r.current.Load()
	return &tls.Config{
		RootCAs:    b.caPool,
		MinVersion: tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &r.current.Load().cert, nil
		},
	}
}

// transport builds an http.Transport that reads *both* halves of the mTLS
// material fresh for every new connection.
//
// GetClientCertificate covers the leaf, but there is no client-side
// equivalent for RootCAs: a config handed to http.Transport carries the CA
// pool it had when it was built, forever. The original reasoning was that
// the root is long-lived and a root rotation could afford a rolling
// restart. That was too convenient. When the CA did change underneath a
// running worker, every request failed with "certificate signed by unknown
// authority" while the correct CA sat on disk, already written by the
// renewal sidecar -- an error that points at the peer's certificate when
// the problem is entirely local.
//
// Dialling per connection costs nothing here (connections are pooled and
// long-lived) and removes the whole class of failure.
func (r *clientCertReloader) transport() *http.Transport {
	return &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			b := r.current.Load()
			cfg := &tls.Config{
				ServerName:   host,
				RootCAs:      b.caPool,
				Certificates: []tls.Certificate{b.cert},
				MinVersion:   tls.VersionTLS13,
			}
			return (&tls.Dialer{Config: cfg}).DialContext(ctx, network, addr)
		},
	}
}
