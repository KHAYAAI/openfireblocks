package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The slice of Vault's API this needs: seal status, init, unseal.
//
// Unauthenticated endpoints, all three -- there is no token to present
// before a cluster exists, which is precisely why they are unauthenticated
// and why the init endpoint can only ever succeed once.
type vaultClient struct {
	addr   string
	client *http.Client
}

func newVaultClient(addr string) *vaultClient {
	return &vaultClient{
		addr:   strings.TrimSuffix(addr, "/"),
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

type sealStatus struct {
	Initialized bool `json:"initialized"`
	Sealed      bool `json:"sealed"`
	// Progress toward the unseal threshold; useful in logs when a
	// multi-share configuration is only partly unsealed.
	Progress  int `json:"progress"`
	Threshold int `json:"t"`
}

func (v *vaultClient) SealStatus() (*sealStatus, error) {
	resp, err := v.client.Get(v.addr + "/v1/sys/seal-status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	// 503 is the documented response for a sealed Vault and carries a
	// perfectly good body. Treating it as an error would make the sidecar
	// refuse to act in exactly the state it exists to fix.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		return nil, fmt.Errorf("seal-status: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out sealStatus
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding seal-status: %w", err)
	}
	return &out, nil
}

type initResponse struct {
	Keys      []string `json:"keys"`
	KeysB64   []string `json:"keys_base64"`
	RootToken string   `json:"root_token"`
}

// Init initialises the cluster.
//
// One share and a threshold of one, which is not what a real deployment
// should do: production splits the key across several holders, or removes
// it entirely with KMS/HSM auto-unseal. A single share is what allows this
// sidecar to unseal unattended, and unattended is the requirement here --
// the alternative is a cluster that stays down after every restart until a
// human arrives with key material.
//
// Returns (nil, false, nil) if Vault reports it is already initialised,
// which happens routinely when several replicas race.
func (v *vaultClient) Init() (*unsealMaterial, bool, error) {
	payload, _ := json.Marshal(map[string]int{
		"secret_shares":    1,
		"secret_threshold": 1,
	})
	req, err := http.NewRequest(http.MethodPut, v.addr+"/v1/sys/init", bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false, err
	}

	if resp.StatusCode != http.StatusOK {
		// Vault answers 400 "Vault is already initialized" when it lost the
		// race. That is an expected outcome, not a failure.
		if strings.Contains(strings.ToLower(string(body)), "already initialized") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("init: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var out initResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false, fmt.Errorf("decoding init response: %w", err)
	}
	key := ""
	if len(out.KeysB64) > 0 {
		key = out.KeysB64[0]
	} else if len(out.Keys) > 0 {
		key = out.Keys[0]
	}
	if key == "" {
		return nil, false, fmt.Errorf("init returned no unseal key")
	}
	return &unsealMaterial{UnsealKey: key, RootToken: out.RootToken}, true, nil
}

// Unseal submits one key share.
func (v *vaultClient) Unseal(key string) (*sealStatus, error) {
	payload, _ := json.Marshal(map[string]string{"key": key})
	req, err := http.NewRequest(http.MethodPut, v.addr+"/v1/sys/unseal", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unseal: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out sealStatus
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding unseal response: %w", err)
	}
	return &out, nil
}
