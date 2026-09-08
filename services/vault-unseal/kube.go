package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Minimal in-cluster Kubernetes client: read and create one Secret.
//
// The unseal material has to live somewhere every Vault replica can reach,
// and that rules out the pods' own volumes -- each replica has its own
// PersistentVolumeClaim, so a key written on vault-0 is invisible to
// vault-1. A Secret is the one piece of shared, durable, access-controlled
// state a Kubernetes cluster always has.
//
// Written against the API directly rather than client-go: this needs two
// verbs on one resource, and client-go would add a large dependency tree to
// a component whose entire purpose is to still work when everything else is
// broken.
type kubeClient struct {
	host      string
	namespace string
	token     string
	client    *http.Client
}

const (
	saTokenPath     = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	saCAPath        = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	saNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

func newKubeClient() (*kubeClient, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in a cluster: KUBERNETES_SERVICE_HOST/PORT unset")
	}

	token, err := os.ReadFile(saTokenPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read the service account token: %w", err)
	}
	caPEM, err := os.ReadFile(saCAPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read the cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("cluster CA at %s contained no usable certificates", saCAPath)
	}

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		ns, err := os.ReadFile(saNamespacePath)
		if err != nil {
			return nil, fmt.Errorf("failed to determine the namespace: %w", err)
		}
		namespace = strings.TrimSpace(string(ns))
	}

	return &kubeClient{
		host:      fmt.Sprintf("https://%s:%s", host, port),
		namespace: namespace,
		token:     strings.TrimSpace(string(token)),
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			}},
		},
	}, nil
}

func (k *kubeClient) do(method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequest(method, k.host+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := k.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, out, err
}

// unsealMaterial is what the cluster needs to come back after a restart.
type unsealMaterial struct {
	UnsealKey string `json:"unseal_key"`
	RootToken string `json:"root_token"`
}

const (
	secretKeyUnseal = "unseal-key"
	secretKeyRoot   = "root-token"
)

// GetUnsealMaterial reads the shared Secret. Returns (nil, nil) when it does
// not exist yet, which is the normal state before the cluster is
// initialised and is not an error.
func (k *kubeClient) GetUnsealMaterial(name string) (*unsealMaterial, error) {
	code, body, err := k.do(http.MethodGet,
		fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", k.namespace, name), nil)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, nil
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("reading secret %s: %s: %s", name, http.StatusText(code), strings.TrimSpace(string(body)))
	}

	var out struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding secret %s: %w", name, err)
	}
	key, err := base64.StdEncoding.DecodeString(out.Data[secretKeyUnseal])
	if err != nil {
		return nil, fmt.Errorf("secret %s has an undecodable unseal key: %w", name, err)
	}
	root, err := base64.StdEncoding.DecodeString(out.Data[secretKeyRoot])
	if err != nil {
		return nil, fmt.Errorf("secret %s has an undecodable root token: %w", name, err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("secret %s exists but carries no unseal key", name)
	}
	return &unsealMaterial{UnsealKey: string(key), RootToken: string(root)}, nil
}

// CreateUnsealMaterial stores the material, and reports whether *this*
// caller created it.
//
// The distinction matters: every replica runs this sidecar and they all
// start at once, so more than one can observe an uninitialised Vault and
// try to initialise it. Only one POST to Vault's init endpoint can succeed,
// but the loser must not overwrite the winner's key -- doing so would leave
// a Secret that unseals nothing and a cluster nobody can open. A 409 from
// the API server is the arbiter, and losing is a normal outcome rather than
// an error.
func (k *kubeClient) CreateUnsealMaterial(name string, m *unsealMaterial) (created bool, err error) {
	payload, err := json.Marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": k.namespace,
		},
		"type": "Opaque",
		"data": map[string]string{
			secretKeyUnseal: base64.StdEncoding.EncodeToString([]byte(m.UnsealKey)),
			secretKeyRoot:   base64.StdEncoding.EncodeToString([]byte(m.RootToken)),
		},
	})
	if err != nil {
		return false, err
	}

	code, body, err := k.do(http.MethodPost,
		fmt.Sprintf("/api/v1/namespaces/%s/secrets", k.namespace), payload)
	if err != nil {
		return false, err
	}
	switch code {
	case http.StatusOK, http.StatusCreated:
		return true, nil
	case http.StatusConflict:
		return false, nil
	default:
		return false, fmt.Errorf("creating secret %s: %s: %s", name, http.StatusText(code), strings.TrimSpace(string(body)))
	}
}
