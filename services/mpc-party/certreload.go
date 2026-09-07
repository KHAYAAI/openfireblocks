package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"sync/atomic"
	"time"
)

// Reloads mTLS material from disk while the process is running.
//
// Certificates are issued at pod startup with a short TTL and something
// has to replace them before they expire. Loading the keypair once into a
// tls.Config -- which is what this service did -- means the expiry time of
// the certificate becomes the lifetime of the pod: every connection starts
// failing at the same moment across the whole committee, and the only
// recovery is a restart. That is a scheduled outage with a timer on it.
//
// So the files are the interface. Whatever rewrites them -- the renewal
// sidecar in this chart, cert-manager, a Vault agent, an operator running
// something else entirely -- this process notices and starts presenting
// the new certificate without dropping a connection or restarting.
//
// Only the leaf is genuinely short-lived; the CA is long-lived by design.
// Both are reloaded anyway, because a CA rotation that silently required a
// restart would be the same bug with a longer fuse.
type certReloader struct {
	certFile, keyFile, caFile string

	// Swapped wholesale on reload. Readers take the pointer once and use a
	// consistent snapshot, so a handshake in flight never sees a leaf from
	// one generation and a CA pool from another.
	current atomic.Pointer[certBundle]

	// Identifies the on-disk generation, so polling can skip the parse when
	// nothing has changed.
	lastStamp atomic.Pointer[string]
}

type certBundle struct {
	cert     tls.Certificate
	caPool   *x509.CertPool
	notAfter time.Time
}

// certReloadInterval is how often the files are checked.
//
// Cheap: a stat of three files, and a parse only when one of them actually
// changed. Frequent enough that a renewal is picked up long before the old
// certificate expires, given the sidecar renews at two thirds of the TTL.
const certReloadInterval = 30 * time.Second

func newCertReloader(certFile, keyFile, caFile string) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile, caFile: caFile}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// stamp identifies the current on-disk generation of the three files.
//
// Size and modification time rather than a content hash: this runs every
// 30 seconds forever, and the consequence of a missed change is a delayed
// reload, not a wrong one -- the next renewal writes different bytes at a
// different time.
func (r *certReloader) stamp() string {
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

func (r *certReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("failed to load mTLS cert/key: %w", err)
	}

	caPEM, err := os.ReadFile(r.caFile)
	if err != nil {
		return fmt.Errorf("failed to read mTLS CA file: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("mTLS CA file %s contained no usable certificates", r.caFile)
	}

	// tls.LoadX509KeyPair leaves Leaf nil. Parsing it here means the expiry
	// is available for logging and for the readiness signal below without
	// re-parsing on every handshake.
	var notAfter time.Time
	if len(cert.Certificate) > 0 {
		if leaf, perr := x509.ParseCertificate(cert.Certificate[0]); perr == nil {
			cert.Leaf = leaf
			notAfter = leaf.NotAfter
		}
	}

	r.current.Store(&certBundle{cert: cert, caPool: caPool, notAfter: notAfter})
	stamp := r.stamp()
	r.lastStamp.Store(&stamp)
	return nil
}

// watch polls for replacement material until ctx-less process exit.
//
// A failed reload is logged and the previous bundle is kept: a half-written
// certificate file is a transient state the writer will finish, and
// throwing away a working certificate because a new one was briefly
// unreadable would turn a harmless race into an outage.
func (r *certReloader) watch() {
	for range time.Tick(certReloadInterval) {
		last := r.lastStamp.Load()
		if last != nil && *last == r.stamp() {
			continue
		}
		prev := r.current.Load()
		if err := r.reload(); err != nil {
			log.Printf("mTLS material changed on disk but could not be loaded, keeping the previous certificate: %v", err)
			continue
		}
		cur := r.current.Load()
		if prev != nil && !prev.notAfter.Equal(cur.notAfter) {
			log.Printf("reloaded mTLS certificate, now valid until %s (in %s)",
				cur.notAfter.Format(time.RFC3339), time.Until(cur.notAfter).Round(time.Second))
		}
	}
}

// expiresAt reports when the certificate currently in use stops being
// valid. Zero if it could not be parsed.
func (r *certReloader) expiresAt() time.Time {
	b := r.current.Load()
	if b == nil {
		return time.Time{}
	}
	return b.notAfter
}

// serverConfig builds the listener's TLS config.
//
// GetConfigForClient runs per handshake and returns a config built from the
// current bundle, so both the presented leaf and the pool that verifies
// client certificates follow a rotation without a restart. The outer config
// still carries Certificates so that a client connecting before the first
// handshake-time callback -- and anything inspecting the config -- sees a
// usable certificate rather than none.
func (r *certReloader) serverConfig() *tls.Config {
	base := r.current.Load()
	return &tls.Config{
		Certificates: []tls.Certificate{base.cert},
		ClientCAs:    base.caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			b := r.current.Load()
			return &tls.Config{
				Certificates: []tls.Certificate{b.cert},
				ClientCAs:    b.caPool,
				ClientAuth:   tls.RequireAndVerifyClientCert,
				MinVersion:   tls.VersionTLS13,
			}, nil
		},
	}
}

// clientConfig builds the config for this party's outbound connections.
//
// GetClientCertificate is consulted per handshake, so the leaf this party
// presents to its peers follows a rotation. RootCAs is a snapshot: there is
// no client-side equivalent of GetConfigForClient, and the CA is the
// long-lived half -- a root rotation is a planned event that can afford a
// rolling restart, where a leaf expiring every day cannot.
func (r *certReloader) clientConfig() *tls.Config {
	base := r.current.Load()
	return &tls.Config{
		RootCAs:    base.caPool,
		MinVersion: tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			b := r.current.Load()
			return &b.cert, nil
		},
	}
}
