// Command vault-pki-init requests a short-lived mTLS leaf certificate from
// Vault's PKI secrets engine and writes it to disk in the shape
// services/mpc-party/mtls.go, services/temporal-worker/activities/mtls.go,
// and services/policy-service/mtls.go all expect
// (MTLS_CERT_FILE=tls.crt, MTLS_KEY_FILE=tls.key, MTLS_CA_FILE=ca.crt).
// Meant to run once, as a Kubernetes init container, before the main
// container starts -- see
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
	"encoding/json"
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

	cert, err := issueCertificate(client, vaultAddr, token, pkiMount, pkiRole, commonName, ttl)
	if err != nil {
		log.Fatalf("failed to issue certificate: %v", err)
	}

	if err := writeCertFiles(outDir, cert); err != nil {
		log.Fatalf("failed to write certificate files: %v", err)
	}

	log.Printf("issued and wrote mTLS certificate for %s to %s (serial: %s, ttl: %s)", commonName, outDir, cert.SerialNumber, ttl)
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

func issueCertificate(client *http.Client, vaultAddr, token, mount, role, commonName, ttl string) (*issuedCert, error) {
	body, err := json.Marshal(map[string]string{
		"common_name": commonName,
		"ttl":         ttl,
	})
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
