package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// Mutual TLS for mpc-signer's server side. Same env-var convention and
// implementation as services/policy-service/mtls.go,
// services/mpc-party/mtls.go and
// services/temporal-worker/activities/mtls.go -- deliberately duplicated
// rather than shared, because these are separate Go modules and a shared
// module for ~40 lines of stdlib TLS setup would couple their release
// cycles for no real benefit; the doc comments cross-reference instead.
//
// This is the link that carries an actual transaction to be signed
// (api-gateway -> mpc-signer, and temporal-worker's SignTransaction
// activity -> mpc-signer), which makes it the highest-value link in the
// platform to authenticate: anything that can reach this endpoint
// unauthenticated can ask for a signature.
//
// Opt-in via three env vars so existing plain-HTTP deployments and tests
// are unaffected: unset any one of them and the server falls back to
// ListenAndServe (main.go decides which to call).
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
