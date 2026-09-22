package activities

import (
	"context"
	"fmt"
	"os"

	vault "github.com/hashicorp/vault/api"

	"forge-crypto/temporal-worker/workflows"
)

// ActivateKeyPair marks a key_pairs row active with its real
// DKG-produced address/public key. Runs on a.db, the same app_admin
// (BYPASSRLS) connection every other activity in this package uses (see
// main.go's DATABASE_URL doc comment) -- this worker orchestrates
// ceremonies for whatever customer Temporal dispatched, not one fixed
// tenant session.
func (a *Activities) ActivateKeyPair(ctx context.Context, req workflows.ActivateKeyPairRequest) error {
	if a.db == nil {
		return fmt.Errorf("no database connection configured; cannot activate key_pairs row %s", req.KeyID)
	}
	res, err := a.db.ExecContext(ctx,
		`UPDATE key_pairs SET status = 'active', address = $2, public_key = $3, updated_at = now() WHERE key_id = $1`,
		req.KeyID, req.Address, req.PublicKey,
	)
	if err != nil {
		return fmt.Errorf("failed to activate key_pairs row %s: %w", req.KeyID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("key_pairs row %s not found", req.KeyID)
	}
	return nil
}

// SetKeyPairStatus transitions key_pairs.status -- used by
// KeyRotationWorkflow to mark a retired key 'inactive', but generic over
// any of the CHECK-constrained values (pending_dkg, active, inactive,
// compromised).
func (a *Activities) SetKeyPairStatus(ctx context.Context, req workflows.SetKeyPairStatusRequest) error {
	if a.db == nil {
		return fmt.Errorf("no database connection configured; cannot set key_pairs %s status", req.KeyID)
	}
	res, err := a.db.ExecContext(ctx,
		`UPDATE key_pairs SET status = $2, updated_at = now() WHERE key_id = $1`,
		req.KeyID, req.Status,
	)
	if err != nil {
		return fmt.Errorf("failed to set key_pairs %s status to %q: %w", req.KeyID, req.Status, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("key_pairs row %s not found", req.KeyID)
	}
	return nil
}

// SetCeremonyStatus transitions dkg_ceremonies.status, used by
// ProvisionKeyWorkflow to keep the ceremony's own tracking row truthful
// (initiated -> in_progress -> completed/failed) independent of
// key_pairs.status. Runs on the same app_admin (BYPASSRLS) connection as
// every other activity in this package.
func (a *Activities) SetCeremonyStatus(ctx context.Context, req workflows.SetCeremonyStatusRequest) error {
	if a.db == nil {
		return fmt.Errorf("no database connection configured; cannot set dkg_ceremonies %s status", req.CeremonyID)
	}
	var errMsg interface{}
	if req.ErrorMessage != "" {
		errMsg = req.ErrorMessage
	}
	terminal := req.Status == "completed" || req.Status == "failed"
	res, err := a.db.ExecContext(ctx,
		`UPDATE dkg_ceremonies
		 SET status = $2,
		     error_message = $3,
		     completed_at = CASE WHEN $4 THEN now() ELSE completed_at END,
		     updated_at = now()
		 WHERE ceremony_id = $1::uuid`,
		req.CeremonyID, req.Status, errMsg, terminal,
	)
	if err != nil {
		return fmt.Errorf("failed to set dkg_ceremonies %s status to %q: %w", req.CeremonyID, req.Status, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("dkg_ceremonies row %s not found", req.CeremonyID)
	}
	return nil
}

// DeactivateOldKeyShares soft-deletes (Vault KV v2 "delete" -- recoverable
// via "undelete" until the mount's delete_version_after policy or an
// operator permanently destroys it) every party's sealed share for a
// retired ceremony. Same env-var convention and path format as
// services/mpc-party/vault_seal.go's vaultShareConfigFromEnv
// (VAULT_ADDR/VAULT_TOKEN/VAULT_KV_MOUNT/VAULT_KEY_SHARE_PATH), since
// this operates on exactly the paths that function writes to -- kept as a
// second, independent implementation rather than an import because
// mpc-party's is unexported and this runs in a different process/module
// with different lifecycle needs (this one never reads share content, so
// it doesn't need LoadKeyShare's decrypt-and-unmarshal path at all, only
// enough of the same path derivation to name what to delete).
//
// One party's deletion failing doesn't abort the rest -- each party's
// share is independent, so a transient failure on party 2 shouldn't leave
// party 1's and 3's shares undeleted too. See DeactivateSharesResult.Errors.
func (a *Activities) DeactivateOldKeyShares(ctx context.Context, req workflows.DeactivateSharesRequest) (*workflows.DeactivateSharesResult, error) {
	addr := os.Getenv("VAULT_ADDR")
	if addr == "" {
		// Matches vault_seal.go's own precedence: unset VAULT_ADDR means
		// sealing was never configured for this deployment, so there's
		// nothing in Vault to deactivate -- a valid, non-error state, not
		// a failure to report deactivatedCount=0 for.
		return &workflows.DeactivateSharesResult{}, nil
	}

	client, err := vault.NewClient(&vault.Config{Address: addr})
	if err != nil {
		return nil, fmt.Errorf("vault client: %w", err)
	}
	if token := os.Getenv("VAULT_TOKEN"); token != "" {
		client.SetToken(token)
	}

	mount := getenvDefault("VAULT_KV_MOUNT", "secret")
	basePath := getenvDefault("VAULT_KEY_SHARE_PATH", "openfireblocks/mpc-party")
	kv := client.KVv2(mount)

	result := &workflows.DeactivateSharesResult{}
	for _, partyID := range req.PartyIDs {
		path := fmt.Sprintf("%s/party-%d/%s", basePath, partyID, req.CeremonyID)
		if err := kv.Delete(ctx, path); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("party %d (%s): %v", partyID, path, err))
			continue
		}
		result.DeactivatedCount++
	}
	return result, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
