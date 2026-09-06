package activities

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// Client side of mutual TLS to services/mpc-party -- see the matching
// doc comment in services/mpc-party/mtls.go for why this link specifically
// (the DKG ceremony transport) and how a real deployment issues these
// certs via Vault PKI. Same three-env-var opt-in convention as the server
// side, and same names, since both are configured from the same
// Terraform/Vault-issued cert bundle in practice.
const (
	envMTLSCertFile = "MTLS_CERT_FILE"
	envMTLSKeyFile  = "MTLS_KEY_FILE"
	envMTLSCAFile   = "MTLS_CA_FILE"
)

// clientTLSConfigFromEnv mirrors serverTLSConfigFromEnv in
// services/mpc-party/mtls.go: (nil, false, nil) when mTLS isn't
// configured (a valid, if less secure, local-dev configuration), an error
// only when it's partially configured or the files don't load.
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
