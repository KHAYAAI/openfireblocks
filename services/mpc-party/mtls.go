package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// Mutual TLS for mpc-party's server side (temporal-worker's
// DKGRoundCoordinator calling the legacy round-relay endpoints, and any
// peer party calling into /tss/keygen/*) and client side (this party's own
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

// serverTLSConfigFromEnv returns (config, true, nil) if all three mTLS env
// vars are set and load correctly, or (nil, false, nil) if mTLS simply
// isn't configured (not an error -- plain HTTP is a valid, if less secure,
// configuration this platform still needs to support for local dev).
func serverTLSConfigFromEnv() (*tls.Config, bool, error) {
	certFile, keyFile, caFile := os.Getenv(envMTLSCertFile), os.Getenv(envMTLSKeyFile), os.Getenv(envMTLSCAFile)
	if certFile == "" || keyFile == "" || caFile == "" {
		return nil, false, nil
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, false, fmt.Errorf("failed to load mTLS server cert/key: %w", err)
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read mTLS CA file: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, false, fmt.Errorf("mTLS CA file %s contained no usable certificates", caFile)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}, true, nil
}

// clientTLSConfigFromEnv mirrors serverTLSConfigFromEnv for this party's
// outbound connections to its peers (TSSPartyManager's relay client).
func clientTLSConfigFromEnv() (*tls.Config, bool, error) {
	certFile, keyFile, caFile := os.Getenv(envMTLSCertFile), os.Getenv(envMTLSKeyFile), os.Getenv(envMTLSCAFile)
	if certFile == "" || keyFile == "" || caFile == "" {
		return nil, false, nil
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, false, fmt.Errorf("failed to load mTLS client cert/key: %w", err)
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read mTLS CA file: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, false, fmt.Errorf("mTLS CA file %s contained no usable certificates", caFile)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}, true, nil
}
