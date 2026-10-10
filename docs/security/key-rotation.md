# Key & Credential Rotation

This document covers every category of key/secret/certificate this platform
issues and what rotating each one actually does today — verified against the
real code, not written as an aspirational plan. Where a category has no real
rotation mechanism yet, that's stated plainly rather than described as if it
worked.

## 1. Threshold signing keys (DKG-generated, per key_pair)

**What rotation means here**: generating a brand-new k-of-n threshold key
(new DKG ceremony, new address) and retiring the old one — not re-deriving
the same key. A threshold-ECDSA key can't be "rotated in place" the way a
symmetric secret can; the address changes.

**What's real, as of a later pass**: `workflows.KeyRotationWorkflow`
(`services/temporal-worker/workflows/dkg_ceremony.go`) now does all three of
the things the prior paragraph listed as missing, not just the first:

1. Runs a real `DKGCeremonyWorkflow` child workflow — a genuine
   network-driven `keygen.LocalParty` DKG across the same parties (see
   `services/mpc-party/tss_party.go`). Each party's new share is sealed in
   Vault (`services/mpc-party/vault_seal.go`) exactly as for a first-time
   ceremony.
2. Immediately flips `key_pairs.status` via two new activities
   (`ActivateKeyPair`/`SetKeyPairStatus`,
   `services/temporal-worker/activities/key_rotation.go`) — the new row
   becomes `active` with its real address/public key, the old row (if any)
   becomes `inactive`. Verified against a real Postgres instance, not just
   read as code: `TestKeyPairStatusTransitions`
   (`activities/key_rotation_db_test.go`) seeds real `customers`/`key_pairs`
   rows, runs both activities, and confirms the transition with an
   independent `SELECT` afterward — including that acting on a nonexistent
   `key_id` genuinely returns an error rather than silently succeeding.
3. Retires the old ceremony's Vault-sealed shares — but not immediately.
   `KeyRotationWorkflow` calls `workflow.Sleep` for a configurable
   `ShareRetention` (default 720h / 30 days) — a durable Temporal timer,
   the same mechanism `MonitorTransaction`'s polling and every other timer
   in this codebase already relies on — then calls a new
   `DeactivateOldKeyShares` activity, which soft-deletes (Vault KV v2
   "delete": recoverable via "undelete" until an operator or the mount's
   own `delete_version_after` policy destroys it for real) every party's
   share at the path `vault_seal.go` wrote it to. One party's deletion
   failing doesn't abort the others (`DeactivateSharesResult.Errors`).
   Verified against a real Vault dev server: `TestLiveDeactivateOldKeyShares`
   (`activities/key_rotation_live_test.go`, build tag `live`) seeds real
   fake shares at the real path format, runs the activity, and confirms
   with an independent follow-up read that they're genuinely unreadable
   afterward — not just that the call returned no error.

**Balance migration — the part that actually matters for custody** — is a
separate new workflow, `workflows.BalanceMigrationWorkflow`
(`services/temporal-worker/workflows/balance_migration.go`), not folded
into `KeyRotationWorkflow` itself (a rotation may have nothing to migrate,
e.g. a key rotated before ever receiving funds — see its `Skipped` result).
It queries the retiring address's real balance and gas price
(`BuildSweepTransaction`), builds a real `go-ethereum` `LegacyTx` moving
everything above gas cost to the replacement address, gets it signed by a
**real threshold signature from the OLD ceremony's key** via the exact same
`ExecuteRealSigning` path `ThresholdSigningWorkflow` uses (the old key
material never left Vault/mpc-party to make a "simpler" signing path
possible), assembles the final signed transaction
(`AssembleSweepTransaction`), and — this is not optional, it's the point —
verifies the recovered sender matches the old address before returning
anything; a mismatch is refused, not handed back as if valid.

Verified end-to-end against real, separately-running `mpc-party` processes
(`TestLiveBalanceMigrationSweep`, `activities/balance_migration_live_test.go`,
build tag `live`, same convention as the pre-existing
`TestLiveRealDKGAndSigning`): a real DKG ceremony derived a threshold
address, a real 2-of-3 threshold signature was produced over the sweep
transaction's hash, and the assembled, RLP-encoded signed transaction was
independently confirmed (via `crypto.SigToPub`, not just the activity's own
internal check) to recover to that exact address. Measured: the full
DKG-to-signed-sweep-transaction pipeline took ~46 seconds in this sandbox
(dominated by real tss-lib DKG, not the transaction-assembly code, which is
pure and effectively instant — see `TestAssembleSweepTransaction_ValidSignature`,
which covers the same composition with a plain key standing in for a
threshold signature, no live processes required).

**What's still not real**: broadcasting the assembled sweep transaction
against an actual funded address on a live chain. No funded testnet address
or reachable Ethereum RPC endpoint exists in this sandbox, so
`BuildSweepTransaction`'s `ethclient.BalanceAt`/`SuggestGasPrice`/
`PendingNonceAt` calls and `BalanceMigrationWorkflow`'s final
`BroadcastTransaction` step (reusing the existing activity
`TransactionSettlementWorkflow` already uses) are real code, wired
correctly, but not exercised against a live network — the cryptographic
composition is proven, an actual on-chain transfer isn't. If broadcast
fails, the workflow reports `status: "signed_not_broadcast"` with the real
signed transaction hex included, so an operator can rebroadcast it by hand
rather than losing the work.

**A related gap found while building this, out of this section's original
scope but worth disclosing**: `services/api-gateway/src/keys/keys.service.ts`'s
`createKey` has its own `// TODO: Trigger DKG ceremony workflow` — the
customer-facing "create a key" API path creates a `pending_dkg` `key_pairs`
row and then **never actually starts a Temporal workflow**, DKG or
otherwise. Every DKG/rotation/signing workflow this document and
`docs/security/audit-checklist.md` describe as real has been exercised by
directly constructing its request struct and executing it (via Temporal's
test environment or the live `-tags live` processes) — none of it is
reachable yet from the actual customer-facing API without a human or a
script doing that by hand. Wiring a real Temporal client into the NestJS
gateway is a materially different, larger piece of work than this section's
scope (rotation of an *existing* key) and was not attempted here.

## 2. mTLS certificates (service-to-service)

**What's real**: `infrastructure/terraform/modules/vault-pki` configures a
Vault PKI secrets engine with `max_lease_ttl` (default `24h` —
see that module's `variables.tf`) — every certificate issued through it is
short-lived by construction. A service that requests a fresh cert on every
restart never runs on a certificate older than that TTL; there is no
"rotation" step to schedule because the certs expire and get reissued as a
natural consequence of the TTL.

**What's not real yet**: nothing in this codebase actually calls `vault
write pki/.../issue/internal-service` on a schedule or on startup — the
Terraform module issues one root CA cert, but no service init container or
sidecar requests its own leaf cert automatically yet (see
`infrastructure/helm/openfireblocks/templates/secret.yaml`'s doc comment on
`mpcParty.mtls.certSecret`/`temporalWorker.mtls.certSecret`, which currently
expect a human or an External Secrets Operator integration to populate
them). Until that automation exists, "rotation" means manually re-running
the `vault write pki/.../issue/...` command and updating the Kubernetes
Secret before the TTL expires — a real, if manual, procedure, not
theoretical, but not the automated cadence the short TTL is designed to
enable.

## 3. Database credentials (`app` / `app_admin` roles)

**What's real**: `infrastructure/terraform/modules/secrets` generates both
roles' passwords via Terraform's `random_password` resource and stores them
in AWS Secrets Manager. Because they're `random_password` resources (not
data sources), running `terraform apply` again does **not** regenerate
them — Terraform treats an already-created random value as stable state,
by design, so an ordinary `apply` is not a rotation mechanism.

**Actual rotation procedure**:
1. `terraform taint module.app_secrets_primary.random_password.app` (or
   `.app_admin`) to force regeneration on the next apply.
2. `terraform apply` — this generates a new password and writes a new
   version to Secrets Manager, but does **not** by itself change the
   Postgres role's actual password.
3. `ALTER ROLE app WITH PASSWORD '<new value>'` (or `app_admin`) run
   against the live database — a manual step, or one to script, this
   repository doesn't automate yet.
4. Restart every service holding the old DSN in a live connection pool (a
   `sql.DB` doesn't re-read its DSN — it needs a process restart, or a
   connection-pool-level credential-refresh mechanism this codebase
   doesn't have).

There is no automated credential rotation (no Secrets Manager rotation
Lambda, no scheduled Terraform run) — this is a fully manual, multi-step
procedure today.

## 4. `JWT_SECRET` (api-gateway session signing)

**What's real**: sourced from Secrets Manager via the same
`modules/secrets` Terraform module, wired into the Helm chart as a required
env var (`jwt.strategy.ts` throws at startup if it's unset — see
`services/api-gateway`).

**Rotation impact**: rotating this secret invalidates **every currently
active session** simultaneously — every issued JWT fails signature
verification against the new secret the moment it's deployed. There is no
dual-secret grace-period support (accepting tokens signed by either the old
or new secret during a rollover window) in `jwt.strategy.ts` today. Treat a
`JWT_SECRET` rotation as a forced logout of every customer session, and
schedule it accordingly (a maintenance window, or explicit customer
communication) rather than as a silent background operation.

## 5. `ADMIN_API_KEY` (customer provisioning)

**What's real**: a single static token, sourced the same way as
`JWT_SECRET`. Rotating it is a straightforward Secrets Manager +
redeploy operation with no session-invalidation side effect, since it
authenticates admin-only provisioning calls, not customer sessions.

**What's not real yet**: this is explicitly documented elsewhere in this
repo (`values.yaml`) as "replace with Keycloak in prod" — a single shared
static token for all administrative access is not a real access-control
model, and rotating it doesn't change that.

## Summary table

| Category | Rotation mechanism | Automated? | Verified |
|---|---|---|---|
| Threshold signing key | New DKG ceremony + `key_pairs.status` flip + retention-scheduled Vault share deletion via `KeyRotationWorkflow`; balance sweep via `BalanceMigrationWorkflow` | Generation, status flip, Vault retirement, and sweep-tx construction/signing: yes. Sweep broadcast to a live funded chain: no (untestable without one) | New-key generation, both DB transitions, real Vault soft-delete, and a real threshold-signed sweep transaction (recovery-verified) are all live-verified this pass; see this section for what's not: broadcast, and api-gateway never actually starting any DKG workflow |
| mTLS certs | Short Vault PKI TTL (default 24h) | No auto-reissue on schedule yet | Terraform module verified; no live Vault to test issuance against |
| DB credentials | `terraform taint` + `ALTER ROLE` + restart | No | Manual procedure only, not run against a real DB in this pass |
| `JWT_SECRET` | Secrets Manager + redeploy | No | Invalidates all sessions — no grace period exists |
| `ADMIN_API_KEY` | Secrets Manager + redeploy | No | No session impact |

None of these are launch-blocking on their own, but none of them should be
described as "handled" without the caveats above — see
`docs/security/audit-checklist.md` for how this fits into overall
readiness.
