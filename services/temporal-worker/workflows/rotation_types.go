package workflows

import "time"

// Types for KeyRotationWorkflow and BalanceMigrationWorkflow -- see
// docs/security/key-rotation.md section 1 for what these close: a
// rotation used to generate a new key and stop there, leaving the old
// key's shares sealed in Vault forever and any balance still sitting at
// the old address.

// KeyRotationRequest is the workflow input for KeyRotationWorkflow: run a
// brand-new DKG ceremony, then retire the old one.
type KeyRotationRequest struct {
	NewCeremony DKGCeremonyRequest `json:"newCeremony"`

	// OldKeyID/NewKeyID are key_pairs.key_id values (see
	// infrastructure/database/migrations/001_initial_schema.sql) the
	// caller already created rows for -- this workflow does not create
	// key_pairs rows itself, only transitions their status. Either may
	// be left empty to skip that side's DB update (e.g. a caller that
	// manages key_pairs itself and only wants the Vault-side retirement).
	OldKeyID string `json:"oldKeyId,omitempty"`
	NewKeyID string `json:"newKeyId,omitempty"`

	// OldCeremonyID/OldPartyIDs identify whose Vault-sealed shares to
	// retire (see services/mpc-party/vault_seal.go's keyPath format).
	// Left empty/nil to skip Vault retirement entirely (e.g. a first-ever
	// key with no prior ceremony to retire).
	OldCeremonyID string `json:"oldCeremonyId,omitempty"`
	OldPartyIDs   []int  `json:"oldPartyIds,omitempty"`

	// ShareRetention is how long the old shares stay soft-deletable
	// (recoverable) in Vault before this workflow deletes them --  not
	// immediate, to preserve an audit/incident-response trail (see
	// docs/security/incident-response.md's evidence-preservation
	// requirements). Defaults to 720h (30 days) when zero.
	ShareRetention time.Duration `json:"shareRetention,omitempty"`
}

// KeyRotationResult is the workflow output.
type KeyRotationResult struct {
	NewCeremony          DKGCeremonyResult `json:"newCeremony"`
	OldSharesDeactivated bool              `json:"oldSharesDeactivated"`
	Status               string            `json:"status"` // completed | failed
	Error                string            `json:"error,omitempty"`
}

// ActivateKeyPairRequest is passed to the ActivateKeyPair activity: marks
// a key_pairs row active with its now-known address/public key, once its
// DKG ceremony has completed.
type ActivateKeyPairRequest struct {
	KeyID     string `json:"keyId"`
	Address   string `json:"address"`
	PublicKey string `json:"publicKey"`
}

// SetKeyPairStatusRequest is passed to the SetKeyPairStatus activity.
// Status must be one of key_pairs' CHECK-constrained values (pending_dkg,
// active, inactive, compromised).
type SetKeyPairStatusRequest struct {
	KeyID  string `json:"keyId"`
	Status string `json:"status"`
}

// DeactivateSharesRequest is passed to the DeactivateOldKeyShares
// activity: soft-delete (Vault KV v2 "delete", recoverable via
// "undelete" until the mount's own delete_version_after policy or an
// operator permanently destroys it) every PartyIDs member's sealed share
// for CeremonyID.
type DeactivateSharesRequest struct {
	CeremonyID string `json:"ceremonyId"`
	PartyIDs   []int  `json:"partyIds"`
}

// DeactivateSharesResult is returned by the DeactivateOldKeyShares
// activity.
type DeactivateSharesResult struct {
	DeactivatedCount int      `json:"deactivatedCount"`
	Errors           []string `json:"errors,omitempty"` // one party's failure doesn't abort the others
}

// BalanceMigrationRequest is the workflow input for
// BalanceMigrationWorkflow: sweep any balance remaining at a retiring
// threshold address to its replacement, signed with the OLD ceremony's
// threshold key -- the actual custody-relevant half of "rotation" that
// generating a new key alone does not accomplish.
type BalanceMigrationRequest struct {
	ChainID        string   `json:"chainId"`    // "ethereum" -- see BuildSweepTransaction, the only chain this builds a real tx for today
	EVMChainID     int64    `json:"evmChainId"` // numeric EIP-155 chain id, e.g. 11155111 for Sepolia
	OldCeremonyID  string   `json:"oldCeremonyId"`
	OldAddress     string   `json:"oldAddress"`
	NewAddress     string   `json:"newAddress"`
	PartyIDs       []int    `json:"partyIds"`       // signing committee: threshold+1 members of the OLD ceremony
	PartyEndpoints []string `json:"partyEndpoints"` // parallel to PartyIDs
}

// BalanceMigrationResult is the workflow output.
type BalanceMigrationResult struct {
	Status      string `json:"status"` // completed | skipped_zero_balance | signed_not_broadcast | failed
	SweptWei    string `json:"sweptWei,omitempty"`
	SignedTxHex string `json:"signedTxHex,omitempty"`
	TxHash      string `json:"txHash,omitempty"`
	Error       string `json:"error,omitempty"`
}

// BuildSweepTransactionRequest is passed to the BuildSweepTransaction
// activity.
type BuildSweepTransactionRequest struct {
	OldAddress string `json:"oldAddress"`
	NewAddress string `json:"newAddress"`
	EVMChainID int64  `json:"evmChainId"`
}

// BuildSweepTransactionResult is returned by the BuildSweepTransaction
// activity. All the *Wei fields are base-10 integer strings (wei doesn't
// fit in a float, and JSON numbers lose precision above 2^53) -- same
// convention as workflows.TransactionRequest.Value.
type BuildSweepTransactionResult struct {
	Skipped     bool   `json:"skipped"` // true when balance can't cover gas -- nothing worth sweeping
	BalanceWei  string `json:"balanceWei"`
	Nonce       uint64 `json:"nonce"`
	GasLimit    uint64 `json:"gasLimit"`
	GasPriceWei string `json:"gasPriceWei"`
	ValueWei    string `json:"valueWei"` // balance - (gasLimit * gasPrice) -- what actually gets swept
	TxHashHex   string `json:"txHashHex"`
}

// AssembleSweepTransactionRequest is passed to the AssembleSweepTransaction
// activity. Carries the same plain fields BuildSweepTransaction returned
// (not a serialized unsigned tx) so this activity deterministically
// rebuilds the IDENTICAL transaction object before applying the
// signature -- signing over one transaction and assembling a different
// one would silently produce a signature that doesn't match the
// broadcast tx.
type AssembleSweepTransactionRequest struct {
	NewAddress   string `json:"newAddress"`
	Nonce        uint64 `json:"nonce"`
	GasLimit     uint64 `json:"gasLimit"`
	GasPriceWei  string `json:"gasPriceWei"`
	ValueWei     string `json:"valueWei"`
	EVMChainID   int64  `json:"evmChainId"`
	Signature    string `json:"signature"`    // hex, 65 bytes [R||S||V], from ExecuteRealSigning
	ExpectedFrom string `json:"expectedFrom"` // the old threshold address; the recovered sender is checked against this before returning
}

// AssembleSweepTransactionResult is returned by the
// AssembleSweepTransaction activity.
type AssembleSweepTransactionResult struct {
	SignedTxHex string `json:"signedTxHex"`
	TxHash      string `json:"txHash"`
}
