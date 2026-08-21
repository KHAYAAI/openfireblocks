package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// Mutual TLS between mpc-party and whatever's driving the DKG ceremony
// (services/temporal-worker's DKGRoundCoordinator). This is the actual
// key-generation-ceremony transport -- round data and, eventually, key
// shares cross this link -- so it's the highest-value place in the
// platform for service-to-service mTLS, ahead of lower-stakes internal
// traffic. See infrastructure/terraform/modules/vault-pki for how a real
// deployment issues the cert/key pair this reads; locally or in any
// environment without live Vault, any cert signed by caFile works
// identically, since TLS verification only checks the CA chain, not who
// issued the leaf certificate.
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
