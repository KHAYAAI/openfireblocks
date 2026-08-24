# Pre-Production / Bank-Readiness Audit Checklist

Status legend: ✅ done · 🟡 partial · ⬜ not started

## Cryptography & key management
- 🟡 Vault-backed signing key (KV v2) — single shared key in place
- 🟡 Real MPC threshold signing (Binance TSS-Lib) — real DKG now proven
  over real network transport, not just in-process:
  `services/mpc-party`'s `/tss/keygen/*` endpoints drive an actual
  `keygen.LocalParty` per process (see `tss_party.go`), relaying real
  tss-lib protocol messages over HTTP (JSON + base64-encoded
  `WireBytes()`) between independent processes — genuinely different
  from, and a real advance over, the round-based `/round/*` endpoints
  documented as a non-cryptographic placeholder in `tss_wrapper.go`,
  which remain in place unchanged until `temporal-worker`'s
  `DKGRoundCoordinator` is rewired to drive the new protocol instead
  (not done in this pass). Live-verified with an actual integration
  test, `TestRealMultiPartyKeygenOverHTTP`: three independent
  `httptest.Server`s (real OS sockets, real HTTP round trips) ran a
  genuine DKG and converged on the identical derived Ethereum address —
  passes clean under `-race`. Investigating this also found that
  `services/mpc-signer` (the in-process implementation this session
  previously described as "proven") **did not compile as a whole module
  at all** — `main.go` imported `openfireblocks.com/services/mpc-signer/chains`
  while `go.mod` declares `module forge-crypto/mpc-signer`, a plain
  path mismatch nobody had run `go build ./...` against before. Fixed
  the import path; `go build -tags tss ./tss/...` and its test
  (`TestThresholdKeygenAndSign`) now genuinely pass, confirming the
  in-process claim was correct once the module actually builds — but
  the wider module (`main.go`, `chains/bitcoin.go`, `chains/cosmos.go`,
  `chains/solana.go`) still doesn't build: those files reference
  `btcutil`/`cosmos-sdk`/`base58` packages never added to `go.mod`, and
  `cosmos-sdk`'s current version needs a newer Go toolchain than
  installed in this sandbox. Out of scope for this pass (unrelated to
  threshold-signing correctness) and left as a disclosed, separate gap
  rather than silently worked around. Threshold **signing** over the new
  real network transport (as opposed to keygen) is real, valuable,
  separate work not attempted here — the single largest remaining item
  before this line can move to ✅.
- 🟡 HSM-backed Vault auto-unseal — this checklist previously listed it
  ⬜, which was wrong: it's already real. `modules/vault/user_data.sh`'s
  rendered `vault.hcl` has a `seal "awskms"` stanza (not Shamir), and
  `security.tf`'s `vault_primary`/`vault_secondary` IAM roles both grant
  `kms:Decrypt`/`kms:Encrypt`/`kms:GenerateDataKey*`/`kms:DescribeKey`
  scoped to their region's own KMS key ARN, matching the `kms_key_id`
  each Vault module instance is passed in `main.tf`. AWS KMS is the
  standard cloud-native equivalent of an HSM-backed seal (a dedicated
  physical HSM cluster is the CloudHSM-backed alternative, not used
  here). `terraform validate` passes and `terraform plan` resolves the
  graph, failing only at the AWS-credentials boundary like everything
  else in this tree. Still 🟡, not ✅: never applied against a real AWS
  account, so a real Vault node actually auto-unsealing via this path at
  boot has not been observed, only the Terraform/IAM/config wiring that
  should produce it.
- 🟡 Documented key ceremony + rotation procedure — see
  `docs/security/key-rotation.md`, written this pass, covering all five
  categories of key/credential this platform issues (threshold signing
  keys, mTLS certs, DB credentials, `JWT_SECRET`, `ADMIN_API_KEY`) with
  what's real vs. not for each. The threshold-key rotation workflow
  (`KeyRotationWorkflow`) genuinely generates a new key via the real DKG
  protocol now, but has an explicit `TODO` for retiring/deactivating the
  old key's shares and no balance-migration step — so it currently
  provisions a second key rather than completing a rotation. DB
  credential and cert rotation are documented manual procedures, not
  automated ones.
- ⬜ External cryptographic audit of the signing layer

## Application security
- ✅ API-key authentication on all tenant routes
- ✅ Admin endpoints behind a separate fail-closed admin token
- ✅ Strict request validation (reject unknown fields)
- ✅ Fail-closed policy enforcement before signing
- ✅ Per-tenant velocity limiting (Redis, tier-based hourly caps)
- ✅ API keys hashed at rest (SHA-256; plaintext shown once)
- 🟡 Rate limiting (per-IP throttler in-app; WAF/Kong at the edge still ⬜)
- ✅ Security headers (helmet)
- ⬜ External penetration test

## Tenant isolation & data
- ✅ All reads/writes scoped by `customer_id`
- 🟡 PostgreSQL RLS — now actually enforced (migration 011/012), not just
  written: the `app` role was a Postgres superuser, which always bypasses
  row security regardless of what policies exist — that's corrected
  (`ALTER ROLE app NOSUPERUSER`), every customer-scoped table has
  `ENABLE`+`FORCE ROW LEVEL SECURITY` and a `tenant_isolation` policy, and
  cross-tenant isolation was live-verified against real Postgres (a
  session with no `app.current_customer_id` set sees zero rows; a session
  scoped to tenant A cannot see tenant B's row; an insert spoofing another
  tenant's `customer_id` is rejected). `services/api-gateway` and, as of
  this pass, `services/policy` thread the real per-request tenant context
  (`set_config('app.current_customer_id', ...)`); `services/policy/db.go`
  now holds two pools at two privilege levels — `app_admin` used ONLY to
  resolve which customer a `key_id`/`policy_id` belongs to (unavoidably
  privileged, since RLS can't scope a query to a tenant that isn't known
  yet), and the RLS-scoped `app` pool via a `withTenant()` wrapper
  (mirroring `postgres.service.ts`'s) for every actual read/write.
  Live-verified the same way as the original RLS work: created a real
  customer/key/policy, confirmed the plain `app` role with no context set
  sees zero rows, confirmed a wrong tenant context also sees zero rows,
  confirmed the correct tenant context sees the row, and exercised the
  full HTTP surface (create/get/list/evaluate) end to end including the
  amount-limit policy actually blocking an over-limit evaluation.

  A later pass extended the same treatment to four more services:
  `settlement`, `billing`, `webhooks`, and `marketplace` all now hold the
  same two-pool (`admin`/`tenant`) + `withTenant()` structure. Where a
  method already received `customerID` directly (e.g. billing's
  `CreateSubscription`, `CreateInvoice`, `CreateUsageMetrics` — the
  `Subscription`/`Invoice`/`UsageMetrics` structs all carry `CustomerID`)
  it's used straight from the struct; where a method only has an entity
  ID (`settlement_id`, `subscription_id`, `webhook_id`, `integration_id`,
  a webhook `delivery_id`), a resolver on the admin pool looks up the
  owning `customer_id` first — including a join through
  `webhook_deliveries.webhook_id → webhooks.customer_id` and
  `integration_webhook_logs`'s equivalent, since neither table carries
  its own `customer_id` column (migration 011 scopes both indirectly).
  `billing`'s `plans` table is deliberately left on the admin pool
  un-scoped — it's platform-wide catalog data (no RLS policy applies to
  it at all), not a tenant-isolation gap. Live-verified: built, vetted,
  and gofmt-clean across all four; `settlement` specifically re-run
  through the same real-Postgres, real-HTTP methodology as `policy` --
  a real customer/key/signing-request/settlement created, a genuine
  `GET /v1/settlements/get` call resolving the tenant and returning the
  real row, and the same three RLS scenarios confirmed directly in SQL
  (no context → zero rows, correct context → the row). `billing`/
  `webhooks`/`marketplace` were not each independently re-verified live
  given the pattern is now proven twice over (`policy`, `settlement`) and
  the remaining three are mechanically identical — a reasonable but
  explicit trust extension, not a claim of individual verification.

  The fifth service originally listed, `temporal-worker`, turned out not
  to need this treatment at all: investigating it found its entire
  Postgres dependency is dead code. `Activities.db` (`*sql.DB`) and
  `Activities.roundStore` (`*db.CeremonyRoundStore`) are constructed at
  startup and never queried anywhere else in the codebase — grep for
  `a.db.` or `.roundStore.` outside their own definitions returns
  nothing. Worse, `CeremonyRoundStore` writes to a `ceremony_rounds`
  table that **no migration file creates** — every method on it would
  fail with "relation does not exist" against a real, freshly-migrated
  database if anything ever called it. Real round-state persistence for
  the actual (now-real, this-pass) DKG protocol lives in each
  `mpc-party` process's memory instead (see `tss_party.go`), not
  Postgres — so there's no live tenant-scoped query path here to
  RLS-scope. Fixing this properly means either wiring
  `CeremonyRoundStore` to a real migration and a real call site, or
  removing the dead scaffolding; RLS rollout isn't the applicable next
  step until one of those happens.

  Still 🟡, not ✅ overall: production RDS has no `app`/`app_admin`
  role split yet — `infrastructure/terraform` provisions one master
  username only; provisioning the same two roles this migration assumes
  is still open.
- 🟡 Dual audit trail (PostgreSQL + immudb) — downgraded from a prior ✅
  that was wrong: `AuditService.logEvent` wrote to `audit.events`, a table
  no migration had ever created, and silently swallowed the resulting
  error "so auditing never blocks signing" — meaning the PostgreSQL half
  of this had been a complete no-op for every signing request. Migration
  012 creates the table (RLS-protected, same as everything else here) and
  this is now live-verified working. The immudb half
  (`services/mpc-signer/audit.go`) is a real SDK integration, not a mock,
  but has not been verified against a live immudb instance in this
  sandbox.
- ⬜ Audit trail export + retention policy

## Infrastructure & operations
- ✅ Non-root, read-only-rootfs containers
- ✅ Resource limits, health probes, HPA, pod anti-affinity
- ✅ Prometheus metrics + SLO alert rules + Grafana dashboard
- ✅ CI builds + tests on every push
- 🟡 mTLS between services — implemented and live-verified for the DKG
  round-relay transport (`services/mpc-party` <-> `services/temporal-worker`'s
  `DKGRoundCoordinator`), chosen as the highest-value link since it's the
  actual key-generation-ceremony transport. Both sides are opt-in via
  three env vars (`MTLS_CERT_FILE`/`MTLS_KEY_FILE`/`MTLS_CA_FILE`) so
  existing plain-HTTP deployments and tests are unaffected when unset.
  `infrastructure/terraform/modules/vault-pki` configures Vault's PKI
  secrets engine to issue the real short-lived certs in production (off
  by default behind `vault_pki_enabled`, since it requires Vault to
  already be initialized/unsealed — a manual step Terraform can't
  perform itself); `terraform validate` passes and `terraform plan`
  resolves the graph, failing only at the AWS-credentials boundary like
  everything else in this tree. Live-verified with a real self-signed CA
  (functionally identical to what Vault PKI hands out — verification only
  checks the CA chain, not the issuer): a real mpc-party HTTPS server
  requiring client certs correctly accepted a request from the real
  `DKGRoundCoordinator` client code presenting a valid cert, and
  correctly rejected (confirmed in the server's own TLS handshake logs)
  a request with no client cert, a request with a cert signed by a
  different/untrusted CA, and a plain-HTTP request to the same port.
  A second link was added in a later pass: `services/temporal-worker`'s
  `CheckPolicy` activity (its shared `httpClient`, used for every HTTP
  call `Activities` makes) against `services/policy-service` — chosen
  because policy evaluation gates every signing request, comparable in
  stakes to the DKG transport. Same opt-in env-var convention, same
  `serverTLSConfigFromEnv`/`clientTLSConfigFromEnv` implementation copied
  into `policy-service/mtls.go`. `NewActivities` now fails loudly
  (`log.Fatalf`) on a genuine mTLS misconfiguration (cert files set but
  unusable) rather than silently falling back to plaintext, matching
  `NewDKGRoundCoordinator`'s existing standard. Live-verified the same
  three-scenario way: a real `policy-service` HTTPS server requiring
  client certs correctly accepted a request from the real `CheckPolicy`
  activity code (`approved=true` back from a genuine `/evaluate` call,
  not a stub), and correctly rejected — confirmed in the server's own
  TLS handshake logs — a request with no client cert and a request with a
  cert signed by a different/untrusted CA.
  Still 🟡, not ✅: two proven links now, not mesh-wide — the other ~9
  internal HTTP links in this platform (api-gateway -> policy,
  api-gateway -> mpc-signer, settlement -> chain RPCs excluded, etc.)
  don't have the pattern applied yet, and no service mesh/sidecar
  (Istio, Linkerd, AWS App Mesh) is used — this is direct in-process TLS
  configuration instead, which is simpler to reason about here given
  there's no live Kubernetes/App-Mesh deployment target settled on yet
  (see the ECS-vs-Helm finding elsewhere in this checklist).
- 🟡 Multi-region / DR with RTO/RPO < 1h — real infrastructure exists
  (`infrastructure/terraform`'s primary+secondary VPC/Vault cluster, and
  `modules/rds-replica`'s cross-region `aws_db_instance` with
  `replicate_source_db` for async PostgreSQL replication), and the
  backup/restore half of this is now real: `services/backup` had no
  `func main()` anywhere in it (nothing invoked it, no cron, no scheduled
  workflow); it now has a real entrypoint (`cmd/backup-server`), real
  `pg_dump -Fc`/`pg_restore` for Postgres, and a real recursive Vault KV
  export/import (honestly not a full Raft snapshot — Vault dev mode's
  in-memory storage doesn't support that API; see `vault_backup.go`'s doc
  comment for what a production raft-backed Vault should use instead).
  A real drill was run and is now a permanent, re-runnable test
  (`services/backup/integration_test.go`,
  `TestFullBackupAndRestoreDrill`): real backup, real restore into an
  isolated database, byte-identical data verified, a real Vault secret
  deleted and restored back with its exact value confirmed. Measured (not
  estimated): ~110-125ms for a full backup, well under a second for a
  full restore — against a dev-scale dataset (1 row, ~128KB), so these
  numbers say nothing about production-scale timing, but they are real
  measurements from a real drill for the first time, replacing
  `docs/PHASE3-BACKUP-RECOVERY-PROCEDURES.md`'s illustrative "RTO ≤ 4h /
  RPO ≤ 1h" / "~500GB-1TB" placeholders (that document corrected in place
  with a reality-check header + an update noting what's now real).
  Fixed two real bugs found only by actually running this:
  `BackupManager.ExecuteFullBackup` never called `storage.StoreBackup`,
  so its own `VerifyBackup` step unconditionally failed every real
  backup; and `RestoreFromPoint` handed each backend the combined
  manifest's ID instead of that backend's own component backup ID.
  Still 🟡, not ✅: incremental backups are full dumps repeated, not true
  WAL-based incrementals; no S3 storage is provisioned (local disk only);
  and — the part still genuinely unbuilt — cross-region **failover**
  (promoting the secondary region) remains honestly unimplemented.
  `DisasterRecoveryCoordinator.InitiateFailover` was exercised over real
  HTTP this pass too (`/dr/failover`) and correctly, immediately reports
  `status: "failed"` for the `postgres` component with rollback marked
  "not implemented" — exactly the honest behavior a prior pass built,
  now confirmed working end-to-end rather than only read as code.
- 🟡 Secrets via external-secrets / sealed-secrets (no plaintext in cluster)
  — `infrastructure/terraform/modules/secrets` now generates real random
  credentials (DB passwords for both the RLS-scoped `app` role and the
  `app_admin` bypass role, JWT signing key, admin API key) into AWS
  Secrets Manager via Terraform, with a task-execution-role IAM policy
  scoped to just that one secret ARN (replacing a prior `Resource: ["*"]`
  grant on the task role, which has been removed as unnecessary — this
  architecture resolves secrets via the execution role, not the app
  calling Secrets Manager itself). `terraform validate` passes and
  `terraform plan` resolves the whole dependency graph (rds → ecs →
  secrets); it fails only at the AWS-credentials boundary, same as
  everything else in this tree without a real account. The Helm chart's
  secret-consuming templates had real bugs found and fixed while wiring
  this up: `api-gateway`'s deployment never set `JWT_SECRET` at all (the
  pod would crash-loop in production — `jwt.strategy.ts` throws if it's
  unset, by design), and `policy-service`/`temporal-worker` had no
  `DATABASE_URL` wired whatsoever (policy would `log.Fatalf` on startup;
  temporal-worker would silently run with ceremony-round persistence
  disabled). All three now wire the correct secret keys. Still 🟡, not
  ✅: no `ExternalSecret`/`ClusterSecretStore` resource is rendered (it's
  documented as a template in `secret.yaml`'s comment, not applied, since
  it depends on cluster-specific IRSA/OIDC wiring this chart doesn't own)
  and — a materially bigger finding from this pass — **no ECS task
  definition or service exists anywhere in this Terraform tree for any
  application service**, only the cluster/IAM/autoscaling shell
  (`modules/ecs`).

  **Resolved in a later pass**: the Helm chart and the ECS Terraform were
  two parallel, never-reconciled deployment targets, and nobody had ever
  picked one. Kubernetes/Helm is now the chosen target — the further-along
  one, and the one this pass actually finished: all 13 deployable services
  in `services/` now have real Deployment+Service templates (`billing`,
  `ceremony-orchestrator`, `compliance`, `marketplace`, `mpc-party`,
  `policy` as `policy-api`, `settlement`, `webhooks` added this pass,
  joining the 4 already-templated `api-gateway`/`mpc-signer`/
  `policy-service`/`temporal-worker`); `mpc-party` deploys as one
  Deployment+Service **per party** (`party-1`...`party-N`, 1-indexed via a
  `{{ range until }}` loop), not N replicas of one Deployment, since
  `temporal-worker`'s `ExecuteRealDKG` and `mpc-party`'s own peer relay
  both hardcode `party-N` hostnames as the addressing convention — N
  interchangeable replicas would not be individually addressable the way
  the real protocol requires. `services/backup` has no `func main()`
  anywhere in it — it's library code with no entrypoint — so it isn't
  deployable as-is and has no template; noted rather than silently
  skipped or given a fabricated entrypoint.

  Also wired: `mpcParty.mtls`/`temporalWorker.mtls` (opt-in, mounts a
  `kubernetes.io/tls`-shaped secret and sets `MTLS_CERT_FILE`/
  `MTLS_KEY_FILE`/`MTLS_CA_FILE`, matching `services/mpc-party/mtls.go`)
  and `mpcParty.vault.addr` (opt-in `VAULT_ADDR`/`VAULT_TOKEN`, matching
  `vault_seal.go`).

  Unlike the prior pass, this one **was** verified against a real `helm`
  binary — Terraform's own bundled binary is still unreachable
  (get.helm.sh and the apt package are both network-blocked in this
  sandbox), but `go install helm.sh/helm/v3/cmd/helm@v3.16.2` isn't
  blocked, so a real binary was built and used: `helm lint` passes clean;
  `helm template` renders the full chart with default values into 29
  valid Kubernetes objects (14 Deployments, 13 Services, 1 HPA, 1
  ServiceAccount — Python's YAML parser confirmed every one is
  well-formed, not just that Helm didn't crash); the mTLS/Vault
  conditional branches were separately rendered with those values
  toggled on and also parse clean; and disabling every newly-added
  service via `--set` correctly collapses the output back down to just
  the original 4 services, confirming the `enabled` gates work in both
  directions. `helm template --debug` shows no `<no value>` (Helm's
  usual sign of a missing values reference) and no errors.

  Still 🟡, not ✅, and deliberately so: none of this has been applied to
  a real cluster (no live Kubernetes cluster in this environment, the
  same boundary Terraform hits at the AWS-credentials level), no
  `ExternalSecret`/`ClusterSecretStore` is rendered (still documented
  only, per `secret.yaml`'s comment — cluster-specific IRSA/OIDC wiring
  this chart doesn't own), and `infrastructure/terraform/modules/ecs`
  still exists with no task definitions — a decision to formally remove
  or keep it as a future alternate target is a separate call this pass
  didn't make, but it should not be stood up alongside Helm as a second
  "live" path.

## Compliance & billing
- 🟡 Usage metering (per-tenant signed/broadcast counts) — billing engine
  (Kill Bill) integration still to wire
- 🟡 SOC 2 Type II — access control policy, incident response plan, and
  vendor risk assessment drafted (`docs/security/`); audit/incident/
  compliance-metric evidence trail (`services/compliance`) is now
  PostgreSQL-backed rather than in-memory; still requires an actual
  observation period and independent audit, which no amount of code
  produces
- ⬜ ISO 27001
- 🟡 OFAC sanctions screening — no provider wired up (see vendor risk
  assessment), but `services/compliance` and `services/policy` now fail
  closed (explicit error) on an unscreened transaction rather than the
  previous behavior of silently reporting every address as clear
- 🟡 Regulatory reporting (SAR/CTR) —
  `services/compliance/regulatory_reporting.go` generates real CTR
  (FinCEN Form 104) and SAR (Form 111) drafts from real transaction data
  (`signing.transactions`, aggregated with `big.Int`, not floats), with
  correct 15-day (CTR) / 30-day (SAR) filing-deadline math, a structuring
  heuristic (repeated transactions in a window), SAR-narrative
  enforcement (refuses to draft one without a human-written description),
  and filed/overdue tracking (`regulatory_filings`, migration 013, RLS
  deny-all for the tenant-facing `app` role since SAR confidentiality is
  a legal requirement — a customer must never learn a SAR exists about
  them). Live-verified end-to-end: seeded three real transactions
  totaling 10.5 ETH, evaluated correctly against a $10,000 threshold both
  with and without a price rate (honestly reports "no price oracle
  configured" rather than fabricating a USD figure when none is given),
  generated a CTR draft, detected the same three transactions as a
  structuring candidate, generated a SAR requiring a narrative, and
  transitioned a filing through to `filed`. What this is *not*: nothing
  here files anything with FinCEN (there's no BSA E-Filing integration,
  by design — `MarkFiled` only records that a human already filed it
  out-of-band), there's no live price oracle anywhere in this codebase
  (USD amounts require the caller to supply a rate), and — per
  `docs/security/what-claude-cannot-build.md` §1 — the $10,000/15-day/
  30-day defaults are the standard US FinCEN rules, not a determination
  of this platform's actual obligations in whatever jurisdictions it
  operates, which still needs legal/compliance sign-off. POPIA/SARS (ZA)
  and any other non-US regime are not modeled at all.

## Fund-movement path
- 🟡 **Threshold signing correctness**: `services/mpc-party` now has TWO
  DKG code paths, and it matters which one anything relies on.
  `TSSWrapper` (`tss_wrapper.go`, endpoints `/round/*`) remains the
  non-cryptographic placeholder described previously — XOR "key
  combination," summed "partial signatures," not valid threshold-ECDSA
  math — unchanged in this pass. Alongside it, `TSSPartyManager`
  (`tss_party.go`, endpoints `/tss/keygen/*`) is a **real** tss-lib
  integration: it constructs and drives an actual `keygen.LocalParty`
  per process, exactly the way the already-verified
  `services/mpc-signer/tss/tss.go` does, but relayed over real HTTP
  between independent processes instead of in-process Go channels —
  the actual production transport shape. Live-verified with a real
  integration test (three independent `httptest.Server`s, real
  sockets) converging on an identical derived address, passing under
  `-race`. What's still ahead before this line is real end-to-end: (1)
  threshold **signing** over this same real transport (not attempted
  in this pass — DKG only), (2) rewiring `temporal-worker`'s
  `DKGRoundCoordinator` to actually drive `/tss/keygen/*` instead of
  the old `/round/*` placeholder (the old endpoints are what's wired
  into production orchestration today), (3) sealing the resulting key
  share in Vault rather than only deriving the public address, and (4)
  fixing `services/mpc-signer`'s own module-wide build (only its `tss`
  subpackage builds; `main.go`/`chains/*` don't — see the note above).
- 🟡 `services/settlement` broadcasts against real go-ethereum/Bitcoin Core
  APIs now (previously fabricated transaction hashes and unconditionally
  reported "confirmed"); not yet tested against a live testnet from this
  environment

## Go/No-go

A mainnet deployment moving customer funds is **NO-GO** until every ⬜ in
Cryptography & key management and Application security is ✅, and an external
cryptographic audit + penetration test have passed.
