package main

import (
	"context"
	"testing"
)

// TestSealAndRetrieveKeyShare tests sealing and retrieving a key share from Vault.
// NOTE: Requires running Vault server with:
// vault server -dev
// export VAULT_ADDR=http://localhost:8200
// export VAULT_TOKEN=<dev token>
func TestSealAndRetrieveKeyShare(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Vault integration test")
	}

	vc, err := NewVaultClient()
	if err != nil {
		t.Fatalf("failed to create Vault client: %v", err)
	}

	ctx := context.Background()

	// Test data
	customerId := "test-customer"
	ceremonyId := "test-ceremony"
	partyId := 0

	keyShare := &KeyShare{
		Share:       "base64encodedinvalidhere",
		Commitments: []string{"commitment1", "commitment2", "commitment3"},
		DLProof:     "dlproofbase64here",
		PublicKey:   "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		Version:     1,
	}

	// Seal key share
	err = vc.SealKeyShare(ctx, customerId, ceremonyId, partyId, keyShare)
	if err != nil {
		t.Fatalf("failed to seal key share: %v", err)
	}

	// Retrieve key share
	retrieved, err := vc.RetrieveKeyShare(ctx, customerId, ceremonyId, partyId)
	if err != nil {
		t.Fatalf("failed to retrieve key share: %v", err)
	}

	// Verify key share
	if retrieved.Share != keyShare.Share {
		t.Errorf("share mismatch: expected %s, got %s", keyShare.Share, retrieved.Share)
	}

	if retrieved.PublicKey != keyShare.PublicKey {
		t.Errorf("public key mismatch: expected %s, got %s", keyShare.PublicKey, retrieved.PublicKey)
	}

	if len(retrieved.Commitments) != len(keyShare.Commitments) {
		t.Errorf("commitments count mismatch: expected %d, got %d", len(keyShare.Commitments), len(retrieved.Commitments))
	}
}

// TestRetrieveKeySharesBatch tests batch retrieval of key shares.
func TestRetrieveKeySharesBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Vault integration test")
	}

	vc, err := NewVaultClient()
	if err != nil {
		t.Fatalf("failed to create Vault client: %v", err)
	}

	ctx := context.Background()

	customerId := "test-customer"
	ceremonyId := "test-ceremony-batch"
	partyIds := []int{0, 1, 2}

	// Seal multiple key shares
	for i, partyId := range partyIds {
		keyShare := &KeyShare{
			Share:       "share" + string(rune(i)),
			PublicKey:   "0x" + string(rune(i)),
			Commitments: []string{},
			Version:     1,
		}

		err := vc.SealKeyShare(ctx, customerId, ceremonyId, partyId, keyShare)
		if err != nil {
			t.Fatalf("failed to seal key share for party %d: %v", partyId, err)
		}
	}

	// Retrieve all in batch
	shares, err := vc.RetrieveKeySharesBatch(ctx, customerId, ceremonyId, partyIds)
	if err != nil {
		t.Fatalf("failed to retrieve key shares batch: %v", err)
	}

	if len(shares) != len(partyIds) {
		t.Errorf("share count mismatch: expected %d, got %d", len(partyIds), len(shares))
	}

	for _, partyId := range partyIds {
		if _, ok := shares[partyId]; !ok {
			t.Errorf("missing key share for party %d", partyId)
		}
	}
}

// TestDeleteKeyShare tests deleting a key share.
func TestDeleteKeyShare(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Vault integration test")
	}

	vc, err := NewVaultClient()
	if err != nil {
		t.Fatalf("failed to create Vault client: %v", err)
	}

	ctx := context.Background()

	customerId := "test-customer"
	ceremonyId := "test-ceremony-delete"
	partyId := 0

	// Seal a key share
	keyShare := &KeyShare{
		Share:       "sharetodelete",
		PublicKey:   "0xdelete",
		Commitments: []string{},
		Version:     1,
	}

	err = vc.SealKeyShare(ctx, customerId, ceremonyId, partyId, keyShare)
	if err != nil {
		t.Fatalf("failed to seal key share: %v", err)
	}

	// Delete it
	err = vc.DeleteKeyShare(ctx, customerId, ceremonyId, partyId)
	if err != nil {
		t.Fatalf("failed to delete key share: %v", err)
	}

	// Verify it's deleted (should return not found)
	_, err = vc.RetrieveKeyShare(ctx, customerId, ceremonyId, partyId)
	if err == nil {
		t.Errorf("key share should be deleted but was retrieved")
	}
}
