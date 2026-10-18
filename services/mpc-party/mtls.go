package main

import (
	"crypto/tls"
	"net/http"
	"os"
)

// Mutual TLS for mpc-party's server side (temporal-worker driving
// ceremonies, and peer parties calling into /tss/keygen/* and /tss/sign/*)
// and client side (this party's own
// TSSPartyManager relaying tss-lib protocol messages out to peer parties --
// see tss_party.go). This is the actual key-generation-ceremony transport
// -- round data and, eventually, key shares cross it -- so it's the
// highest-value place in the platform for service-to-service mTLS. See
// infrastructure/terraform/modules/vault-pki for how a real deployment
// issues the cert/key pair read here; locally or in any environment
// without live Vault, any cert signed by caFile works identically, since
// TLS verification only checks the CA chain, not who issued the leaf
// certificate. A party's single cert serves both roles -- it's presenting
// the same identity whether it's answering a request or making one.
//
// Opt-in via three env vars so existing plain-HTTP deployments and tests
// are unaffected: unset any one of them and both the server and the
// outbound client fall back to plain HTTP.
const (
	envMTLSCertFile = "MTLS_CERT_FILE"
	envMTLSKeyFile  = "MTLS_KEY_FILE"
	envMTLSCAFile   = "MTLS_CA_FILE"
)

// sharedReloader is the process's single view of its mTLS material.
//
// One reloader, not one per call site: the server and the outbound client
// present the same identity, and two independently-polling loaders could
// briefly disagree about which generation of the certificate that is.
var sharedReloader *certReloader

// mtlsFilesFromEnv returns the three paths, or ok=false if mTLS simply
// isn't configured (not an error -- plain HTTP is a valid, if less secure,
// configuration this platform still needs to support for local dev).
func mtlsFilesFromEnv() (certFile, keyFile, caFile string, ok bool) {
	certFile, keyFile, caFile = os.Getenv(envMTLSCertFile), os.Getenv(envMTLSKeyFile), os.Getenv(envMTLSCAFile)
	if certFile == "" || keyFile == "" || caFile == "" {
		return "", "", "", false
	}
	return certFile, keyFile, caFile, true
}

// reloader lazily builds the shared reloader and starts its watch loop.
func reloader() (*certReloader, bool, error) {
	if sharedReloader != nil {
		return sharedReloader, true, nil
	}
	certFile, keyFile, caFile, ok := mtlsFilesFromEnv()
	if !ok {
		return nil, false, nil
	}
	r, err := newCertReloader(certFile, keyFile, caFile)
	if err != nil {
		return nil, false, err
	}
	sharedReloader = r
	go r.watch()
	return r, true, nil
}

// serverTLSConfigFromEnv returns (config, true, nil) if all three mTLS env
// vars are set and load correctly, or (nil, false, nil) if mTLS simply
// isn't configured.
//
// The returned config follows certificate rotation: see certreload.go for
// why loading the keypair exactly once turns the certificate's expiry into
// the pod's lifetime.
func serverTLSConfigFromEnv() (*tls.Config, bool, error) {
	r, ok, err := reloader()
	if err != nil || !ok {
		return nil, false, err
	}
	return r.serverConfig(), true, nil
}

// clientTLSConfigFromEnv mirrors serverTLSConfigFromEnv for this party's
// outbound connections to its peers (TSSPartyManager's relay client).
func clientTLSConfigFromEnv() (*tls.Config, bool, error) {
	r, ok, err := reloader()
	if err != nil || !ok {
		return nil, false, err
	}
	return r.clientConfig(), true, nil
}

// mtlsTransportFromEnv returns a transport that re-reads both the leaf and
// the CA from disk per connection -- see certReloader.clientTransport.
func mtlsTransportFromEnv() (*http.Transport, bool, error) {
	r, ok, err := reloader()
	if err != nil || !ok {
		return nil, false, err
	}
	return r.clientTransport(), true, nil
}
