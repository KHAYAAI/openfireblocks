// Command vault-pki-init requests a short-lived mTLS leaf certificate from
// Vault's PKI secrets engine and writes it to disk in the shape
// services/mpc-party/mtls.go, services/temporal-worker/activities/mtls.go,
// and services/policy-service/mtls.go all expect
// (MTLS_CERT_FILE=tls.crt, MTLS_KEY_FILE=tls.key, MTLS_CA_FILE=ca.crt).
// Runs in either of two shapes:
//   - once, as an init container, so the certificate exists before the main
//     container starts;
//   - with RENEW=true, as a long-lived sidecar that replaces the
//     certificate at two thirds of its life, forever. Without that second
//     shape the certificate's expiry silently becomes the pod's lifetime.
//     Consumers pick the new material up off disk without restarting; see
//     services/mpc-party/certreload.go.
//
// See
// infrastructure/helm/openfireblocks/templates/mpc-party.yaml and
// temporal-worker.yaml for how it's wired in.
//
// Two authentication modes:
//   - VAULT_TOKEN set: use it directly. This is the path exercised by
//     this package's own tests against a real `vault server -dev`
//     instance, and is also a legitimate production option for
//     environments that inject a Vault token by some other secure
//     mechanism (e.g. the Vault Agent Injector).
//   - VAULT_K8S_ROLE set (and VAULT_TOKEN unset): perform a real Vault
//     Kubernetes auth login using this pod's own ServiceAccount JWT (read
//     from the standard projected-token path), matching
//     infrastructure/terraform/modules/vault-pki's kubernetes auth
//     backend and role. This is the real production path but requires an
//     actual Kubernetes cluster + Vault kubernetes auth method configured
//     to exercise end to end -- not verifiable in a sandbox with no
//     cluster; see that Terraform module's own doc comment for what's
//     configured vs. what's never been applied against a real cluster.
package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	vaultAddr := os.Getenv("VAULT_ADDR")
	if vaultAddr == "" {
		log.Fatal("VAULT_ADDR is required")
	}
	commonName := os.Getenv("COMMON_NAME")
	if commonName == "" {
		log.Fatal("COMMON_NAME is required (the hostname this cert will be issued for, e.g. party-1.internal)")
	}
	outDir := getenv("CERT_OUT_DIR", "/etc/openfireblocks/mtls")
	pkiMount := getenv("VAULT_PKI_MOUNT", "pki")
	pkiRole := getenv("VAULT_PKI_ROLE", "internal-service")
	ttl := getenv("CERT_TTL", "24h")
	// Additional DNS SANs, comma-separated. Peers verify the name they
	// dialled, which is the platform's DNS name for this pod, not the
	// service identity in COMMON_NAME -- see issueCertificate.
	altNames := os.Getenv("ALT_NAMES")

	client := &http.Client{Timeout: 30 * time.Second}

	token := os.Getenv("VAULT_TOKEN")
	if token == "" {
		k8sRole := os.Getenv("VAULT_K8S_ROLE")
		if k8sRole == "" {
			log.Fatal("neither VAULT_TOKEN nor VAULT_K8S_ROLE is set -- no way to authenticate to Vault")
		}
		var err error
		token, err = kubernetesLogin(client, vaultAddr, k8sRole)
		if err != nil {
			log.Fatalf("kubernetes auth login failed: %v", err)
		}
		log.Printf("authenticated to Vault via kubernetes auth (role: %s)", k8sRole)
	}

	// RENEW=true turns this from a one-shot init container into a sidecar
	// that keeps the certificate fresh for as long as the pod lives.
	//
	// Without it, issuance is a single event at startup and the
	// certificate's expiry becomes the pod's lifetime: every party's
	// certificate expires at roughly the same moment, the whole committee
	// stops being able to talk at once, and the only recovery is a restart.
	// Short-lived certificates are the right design, but only if something
	// renews them.
	renew := os.Getenv("RENEW") == "true"

	issueOnce := func() (time.Time, error) {
		cert, err := issueCertificate(client, vaultAddr, token, pkiMount, pkiRole, commonName, altNames, ttl)
		if err != nil {
			return time.Time{}, fmt.Errorf("failed to issue certificate: %w", err)
		}
		if err := writeCertFiles(outDir, cert); err != nil {
			return time.Time{}, fmt.Errorf("failed to write certificate files: %w", err)
		}
		sanNote := ""
		if altNames != "" {
			sanNote = " (SANs: " + altNames + ")"
		}
		expiry, perr := certNotAfter(cert.CertPEM)
		if perr != nil {
			// Not fatal: the certificate is written and usable. Only the
			// renewal schedule needs the expiry, and the caller falls back
			// to a duration derived from the requested TTL.
			log.Printf("wrote certificate but could not parse its expiry: %v", perr)
		}
		log.Printf("issued and wrote mTLS certificate for %s%s to %s (serial: %s, ttl: %s)",
			commonName, sanNote, outDir, cert.SerialNumber, ttl)
		return expiry, nil
	}

	expiry, err := issueOnce()
	if err != nil {
		log.Fatal(err)
	}

	if !renew {
		return
	}

	// Renew at two thirds of the certificate's remaining life.
	//
	// Early enough that a failure leaves a third of the lifetime to retry
	// in -- renewal failures are the norm, not the exception, since Vault
	// restarts, token TTLs lapse and networks partition -- and late enough
	// that this is not hammering Vault. Retries below use a fraction of the
	// remaining time rather than a fixed backoff, so attempts get closer
	// together as expiry approaches.
	for {
		// Re-authenticate each cycle when using Kubernetes auth: the login
		// token has its own TTL (an hour by default) and is long dead by
		// the time a 24h certificate needs replacing. Reusing the startup
		// token here is why a naive renewal loop fails its first renewal
		// and not before -- a full day after anyone was watching.
		wait := renewalDelay(expiry, ttl)
		log.Printf("next renewal in %s (certificate valid until %s)",
			wait.Round(time.Second), expiry.Format(time.RFC3339))
		time.Sleep(wait)

		if os.Getenv("VAULT_TOKEN") == "" {
			newToken, lerr := kubernetesLogin(client, vaultAddr, os.Getenv("VAULT_K8S_ROLE"))
			if lerr != nil {
				log.Printf("re-authentication failed, will retry: %v", lerr)
				expiry = retrySoon(expiry)
				continue
			}
			token = newToken
		}

		newExpiry, ierr := issueOnce()
		if ierr != nil {
			log.Printf("renewal failed, will retry: %v", ierr)
			expiry = retrySoon(expiry)
			continue
		}
		expiry = newExpiry
	}
}

// renewalDelay returns how long to wait before replacing a certificate that
// expires at notAfter, targeting two thirds of its remaining life.
//
// Falls back to the requested TTL when the expiry could not be parsed, and
// never returns a negative or absurdly small delay -- an already-expired
// certificate should be replaced now, but a tight loop against Vault helps
// nobody.
func renewalDelay(notAfter time.Time, ttl string) time.Duration {
	remaining := time.Until(notAfter)
	if notAfter.IsZero() {
		if d, err := time.ParseDuration(ttl); err == nil {
			remaining = d
		} else {
			remaining = time.Hour
		}
	}
	delay := remaining * 2 / 3
	if delay < minRenewalDelay {
		delay = minRenewalDelay
	}
	return delay
}

// retrySoon collapses the schedule after a failure so the next attempt
// comes sooner, without ever scheduling a busy loop.
func retrySoon(expiry time.Time) time.Time {
	remaining := time.Until(expiry)
	if remaining <= 0 {
		return time.Now().Add(minRenewalDelay * 3)
	}
	return time.Now().Add(remaining / 2)
}

// minRenewalDelay floors every wait, including retries after failure.
//
// Short enough for a test to drive several renewals with a small TTL,
// long enough that a persistently failing renewal cannot turn into a tight
// loop against Vault.
const minRenewalDelay = 5 * time.Second

// certNotAfter parses the expiry out of a PEM-encoded certificate.
func certNotAfter(certPEM string) (time.Time, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return time.Time{}, fmt.Errorf("no PEM block in certificate")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.NotAfter, nil
}

// vaultIssueResponse is the subset of Vault's PKI issue response this
// program uses. See:
// https://developer.hashicorp.com/vault/api-docs/secret/pki#generate-certificate-and-key
type vaultIssueResponse struct {
	Data struct {
		Certificate  string `json:"certificate"`
		PrivateKey   string `json:"private_key"`
		IssuingCA    string `json:"issuing_ca"`
		SerialNumber string `json:"serial_number"`
	} `json:"data"`
	Errors []string `json:"errors,omitempty"`
}

type issuedCert struct {
	CertPEM      string
	KeyPEM       string
	CAPEM        string
	SerialNumber string
}

// issueCertificate requests a leaf from Vault's PKI engine.
//
// altNames is a comma-separated list of additional DNS SANs, and is not
// cosmetic: the common name identifies the *service* (party-1.internal),
// but peers reach it at whatever DNS name the platform gives it
// (party-1.openfireblocks.svc.cluster.local). TLS verifies the name the
// client dialled, so without SANs covering that name every mutually
// authenticated connection fails hostname verification -- and the failure
// looks like a certificate problem rather than a naming one.
func issueCertificate(client *http.Client, vaultAddr, token, mount, role, commonName, altNames, ttl string) (*issuedCert, error) {
	payload := map[string]string{
		"common_name": commonName,
		"ttl":         ttl,
	}
	if altNames != "" {
		payload["alt_names"] = altNames
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/%s/issue/%s", vaultAddr, mount, role)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var parsed vaultIssueResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse response (status %d): %s", resp.StatusCode, string(raw))
	}
	if resp.StatusCode != http.StatusOK {
		if len(parsed.Errors) > 0 {
			return nil, fmt.Errorf("vault returned %d: %v", resp.StatusCode, parsed.Errors)
		}
		return nil, fmt.Errorf("vault returned %d: %s", resp.StatusCode, string(raw))
	}

	if parsed.Data.Certificate == "" || parsed.Data.PrivateKey == "" {
		return nil, fmt.Errorf("vault response missing certificate or private_key: %s", string(raw))
	}

	return &issuedCert{
		CertPEM:      parsed.Data.Certificate,
		KeyPEM:       parsed.Data.PrivateKey,
		CAPEM:        parsed.Data.IssuingCA,
		SerialNumber: parsed.Data.SerialNumber,
	}, nil
}

func writeCertFiles(outDir string, cert *issuedCert) error {
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	// 0600: only this pod's own containers (sharing the volume) need to
	// read these -- the private key especially should never be
	// group/world-readable.
	if err := os.WriteFile(filepath.Join(outDir, "tls.crt"), []byte(cert.CertPEM), 0o600); err != nil {
		return fmt.Errorf("failed to write tls.crt: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "tls.key"), []byte(cert.KeyPEM), 0o600); err != nil {
		return fmt.Errorf("failed to write tls.key: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "ca.crt"), []byte(cert.CAPEM), 0o600); err != nil {
		return fmt.Errorf("failed to write ca.crt: %w", err)
	}
	return nil
}

type k8sLoginResponse struct {
	Auth struct {
		ClientToken string `json:"client_token"`
	} `json:"auth"`
	Errors []string `json:"errors,omitempty"`
}

// kubernetesLogin performs a real Vault Kubernetes auth login using this
// pod's own projected ServiceAccount token. See
// https://developer.hashicorp.com/vault/docs/auth/kubernetes -- requires
// a real cluster with Vault's kubernetes auth method configured against
// it (infrastructure/terraform/modules/vault-pki's vault_auth_backend
// "kubernetes" + vault_kubernetes_auth_backend_role), not verifiable
// without one.
func kubernetesLogin(client *http.Client, vaultAddr, role string) (string, error) {
	jwtPath := getenv("KUBERNETES_TOKEN_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/token")
	jwt, err := os.ReadFile(jwtPath)
	if err != nil {
		return "", fmt.Errorf("failed to read service account token from %s: %w", jwtPath, err)
	}

	body, err := json.Marshal(map[string]string{
		"role": role,
		"jwt":  string(jwt),
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal login request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/auth/kubernetes/login", vaultAddr)
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read login response: %w", err)
	}

	var parsed k8sLoginResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse login response (status %d): %s", resp.StatusCode, string(raw))
	}
	if resp.StatusCode != http.StatusOK || parsed.Auth.ClientToken == "" {
		if len(parsed.Errors) > 0 {
			return "", fmt.Errorf("vault login returned %d: %v", resp.StatusCode, parsed.Errors)
		}
		return "", fmt.Errorf("vault login returned %d with no client_token: %s", resp.StatusCode, string(raw))
	}
	return parsed.Auth.ClientToken, nil
}
