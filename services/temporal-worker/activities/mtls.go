package activities

import (
	"crypto/tls"
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

// sharedReloader is the process's single view of its mTLS material, so the
// worker cannot end up with two loaders disagreeing about which generation
// of its certificate is current.
var sharedReloader *clientCertReloader

// clientTLSConfigFromEnv mirrors serverTLSConfigFromEnv in
// services/mpc-party/mtls.go: (nil, false, nil) when mTLS isn't
// configured (a valid, if less secure, local-dev configuration), an error
// only when it's partially configured or the files don't load.
//
// The returned config follows certificate rotation -- see certreload.go for
// why loading the keypair exactly once means the worker stops being able to
// reach any party the moment its startup certificate expires.
func clientTLSConfigFromEnv() (*tls.Config, bool, error) {
	certFile, keyFile, caFile := os.Getenv(envMTLSCertFile), os.Getenv(envMTLSKeyFile), os.Getenv(envMTLSCAFile)
	if certFile == "" || keyFile == "" || caFile == "" {
		return nil, false, nil
	}

	if sharedReloader == nil {
		r, err := newClientCertReloader(certFile, keyFile, caFile)
		if err != nil {
			return nil, false, err
		}
		sharedReloader = r
	}
	return sharedReloader.config(), true, nil
}
