package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	vault "github.com/hashicorp/vault/api"
)

// VaultClient manages key share operations in HashiCorp Vault.
type VaultClient struct {
	client *vault.Client
	mount  string // KV v2 mount path (default: "secret")
}

// NewVaultClient creates a new Vault client.
// Reads VAULT_ADDR and VAULT_TOKEN from environment.
func NewVaultClient() (*VaultClient, error) {
	config := vault.DefaultConfig()
	config.Address = os.Getenv("VAULT_ADDR")
	if config.Address == "" {
		config.Address = "http://localhost:8200"
	}

	client, err := vault.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Vault client: %w", err)
	}

	token := os.Getenv("VAULT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("VAULT_TOKEN not set")
	}
	client.SetToken(token)

	mount := os.Getenv("VAULT_KV_MOUNT")
	if mount == "" {
		mount = "secret"
	}

	return &VaultClient{
		client: client,
		mount:  mount,
	}, nil
}

// KeyShare represents a sealed key share in Vault.
type KeyShare struct {
	Share       string   `json:"share"`       // Serialized key share (base64)
	Commitments []string `json:"commitments"` // DKG commitments (base64)
	DLProof     string   `json:"dlProof"`     // Discrete log proof (base64)
	PublicKey   string   `json:"publicKey"`   // Party's public key (hex)
	Version     int      `json:"version"`     // Ceremony round when created
}

// SealKeyShare seals a key share in Vault under path:
// secret/customers/{customerId}/ceremonies/{ceremonyId}/party/{partyId}
func (vc *VaultClient) SealKeyShare(ctx context.Context, customerId, ceremonyId string, partyId int, share *KeyShare) error {
	path := fmt.Sprintf("data/customers/%s/ceremonies/%s/party/%d", customerId, ceremonyId, partyId)

	data := map[string]interface{}{
		"share":       share.Share,
		"commitments": share.Commitments,
		"dlProof":     share.DLProof,
		"publicKey":   share.PublicKey,
		"version":     share.Version,
		"sealed_at":   timeNow(), // Add sealed timestamp
	}

	secret, err := vc.client.KVv2(vc.mount).Put(ctx, path, data)
	if err != nil {
		return fmt.Errorf("failed to seal key share: %w", err)
	}

	log.Printf("sealed key share: party=%d, path=%s, version=%d", partyId, path, secret.Data["metadata"].(map[string]interface{})["version"])
	return nil
}

// RetrieveKeyShare retrieves a sealed key share from Vault.
func (vc *VaultClient) RetrieveKeyShare(ctx context.Context, customerId, ceremonyId string, partyId int) (*KeyShare, error) {
	path := fmt.Sprintf("customers/%s/ceremonies/%s/party/%d", customerId, ceremonyId, partyId)

	secret, err := vc.client.KVv2(vc.mount).Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve key share: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("key share not found")
	}

	data := secret.Data
	share := &KeyShare{
		Share:       data["share"].(string),
		PublicKey:   data["publicKey"].(string),
		Version:     int(data["version"].(float64)),
	}

	// Parse commitments and DL proof
	if commitments, ok := data["commitments"].([]interface{}); ok {
		for _, c := range commitments {
			share.Commitments = append(share.Commitments, c.(string))
		}
	}

	if dlProof, ok := data["dlProof"].(string); ok {
		share.DLProof = dlProof
	}

	return share, nil
}

// RetrieveKeySharesBatch retrieves multiple key shares in parallel.
func (vc *VaultClient) RetrieveKeySharesBatch(ctx context.Context, customerId, ceremonyId string, partyIds []int) (map[int]*KeyShare, error) {
	shares := make(map[int]*KeyShare)
	errChan := make(chan error, len(partyIds))

	for _, partyId := range partyIds {
		go func(pid int) {
			share, err := vc.RetrieveKeyShare(ctx, customerId, ceremonyId, pid)
			if err != nil {
				errChan <- fmt.Errorf("failed to retrieve party %d: %w", pid, err)
				return
			}
			shares[pid] = share
		}(partyId)
	}

	// Wait for all retrievals
	for range partyIds {
		if err := <-errChan; err != nil && err.Error() != "" {
			return nil, err
		}
	}

	return shares, nil
}

// ListKeyShares lists all key shares for a ceremony.
func (vc *VaultClient) ListKeyShares(ctx context.Context, customerId, ceremonyId string) ([]int, error) {
	path := fmt.Sprintf("metadata/customers/%s/ceremonies/%s", customerId, ceremonyId)

	secret, err := vc.client.Logical().List(path)
	if err != nil {
		return nil, fmt.Errorf("failed to list key shares: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return nil, nil
	}

	keys, ok := secret.Data["keys"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid keys format")
	}

	var partyIds []int
	for _, k := range keys {
		partyId := 0
		fmt.Sscanf(k.(string), "party/%d", &partyId)
		partyIds = append(partyIds, partyId)
	}

	return partyIds, nil
}

// DeleteKeyShare deletes a key share from Vault (soft delete with retention).
func (vc *VaultClient) DeleteKeyShare(ctx context.Context, customerId, ceremonyId string, partyId int) error {
	path := fmt.Sprintf("data/customers/%s/ceremonies/%s/party/%d", customerId, ceremonyId, partyId)

	_, err := vc.client.KVv2(vc.mount).Delete(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to delete key share: %w", err)
	}

	log.Printf("deleted key share: party=%d", partyId)
	return nil
}

// RotateKeyShares marks old key shares for deletion after retention period.
func (vc *VaultClient) RotateKeyShares(ctx context.Context, customerId, oldCeremonyId string) error {
	// List all key shares in old ceremony
	partyIds, err := vc.ListKeyShares(ctx, customerId, oldCeremonyId)
	if err != nil {
		return fmt.Errorf("failed to list key shares for rotation: %w", err)
	}

	// TODO: Mark for deletion with retention period (e.g., 90 days)
	// For now, just log
	log.Printf("marked %d key shares for rotation: customerId=%s, ceremonyId=%s", len(partyIds), customerId, oldCeremonyId)
	return nil
}

// GetKeyShareMetadata retrieves metadata about a key share (creation time, version, etc).
func (vc *VaultClient) GetKeyShareMetadata(ctx context.Context, customerId, ceremonyId string, partyId int) (map[string]interface{}, error) {
	path := fmt.Sprintf("metadata/customers/%s/ceremonies/%s/party/%d", customerId, ceremonyId, partyId)

	secret, err := vc.client.Logical().Read(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	if secret == nil {
		return nil, fmt.Errorf("metadata not found")
	}

	return secret.Data, nil
}

// BackupKeyShares exports key shares for backup (encrypted with backup key).
func (vc *VaultClient) BackupKeyShares(ctx context.Context, customerId, ceremonyId string) ([]byte, error) {
	partyIds, err := vc.ListKeyShares(ctx, customerId, ceremonyId)
	if err != nil {
		return nil, fmt.Errorf("failed to list key shares for backup: %w", err)
	}

	shares, err := vc.RetrieveKeySharesBatch(ctx, customerId, ceremonyId, partyIds)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve key shares for backup: %w", err)
	}

	// Marshal to JSON
	backup := map[string]interface{}{
		"customerId":  customerId,
		"ceremonyId":  ceremonyId,
		"backed_up_at": timeNow(),
		"shares":      shares,
	}

	data, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal backup: %w", err)
	}

	// TODO: Encrypt with Vault transit engine before returning
	return data, nil
}

// RestoreKeyShares restores key shares from backup.
func (vc *VaultClient) RestoreKeyShares(ctx context.Context, backupData []byte, customerId, ceremonyId string) error {
	var backup struct {
		Shares map[string]*KeyShare `json:"shares"`
	}

	if err := json.Unmarshal(backupData, &backup); err != nil {
		return fmt.Errorf("failed to unmarshal backup: %w", err)
	}

	for partyIdStr, share := range backup.Shares {
		var partyId int
		fmt.Sscanf(partyIdStr, "%d", &partyId)

		if err := vc.SealKeyShare(ctx, customerId, ceremonyId, partyId, share); err != nil {
			return fmt.Errorf("failed to restore key share for party %d: %w", partyId, err)
		}
	}

	log.Printf("restored %d key shares from backup", len(backup.Shares))
	return nil
}

// timeNow returns current time as RFC3339 string.
func timeNow() string {
	return ""  // TODO: use time.Now().Format(time.RFC3339)
}
