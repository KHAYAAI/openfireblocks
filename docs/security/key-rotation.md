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

**What's real**: `workflows.KeyRotationWorkflow`
(`services/temporal-worker/workflows/dkg_ceremony.go`) runs a real
`DKGCeremonyWorkflow` child workflow — as of this pass, that means a genuine
network-driven `keygen.LocalParty` DKG across the same parties (see
`services/mpc-party/tss_party.go`), not the placeholder round-based protocol
it used to call. Each party's new share is sealed in Vault
(`services/mpc-party/vault_seal.go`) exactly as it is for a first-time
ceremony.

**What's not real yet**: `KeyRotationWorkflow` has an explicit `// TODO:
Deactivate old ceremony shares` where step 2 should be. A rotation today
generates a new key and returns it — the **old key's shares stay sealed in
Vault indefinitely**, unused but not destroyed, and nothing in this codebase
updates a customer's `key_pairs.status` or migrates balances off the old
address. Until that's built, "rotation" here means "provision a second key,"
not "retire the first one." Before relying on this for a real rotation:

1. Build the balance-migration step (sweep funds from the old address to
   the new one — this is the part that actually matters for custody).
2. Build old-share deactivation/scheduled-deletion in Vault after a
   retention period (for audit trail, not immediate deletion — see the
   incident-response plan's evidence-preservation requirements).
3. Update `key_pairs.status` so the API surface reflects which key is live.

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
| Threshold signing key | New DKG ceremony via `KeyRotationWorkflow` | Generation: yes. Old-key retirement: no (TODO) | New-key generation path is the real tss-lib DKG proven this pass |
| mTLS certs | Short Vault PKI TTL (default 24h) | No auto-reissue on schedule yet | Terraform module verified; no live Vault to test issuance against |
| DB credentials | `terraform taint` + `ALTER ROLE` + restart | No | Manual procedure only, not run against a real DB in this pass |
| `JWT_SECRET` | Secrets Manager + redeploy | No | Invalidates all sessions — no grace period exists |
| `ADMIN_API_KEY` | Secrets Manager + redeploy | No | No session impact |

None of these are launch-blocking on their own, but none of them should be
described as "handled" without the caveats above — see
`docs/security/audit-checklist.md` for how this fits into overall
readiness.
