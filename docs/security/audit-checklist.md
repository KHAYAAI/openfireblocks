# Pre-Production / Bank-Readiness Audit Checklist

Status legend: ✅ done · 🟡 partial · ⬜ not started

## Cryptography & key management
- 🟡 Vault-backed signing key (KV v2) — single shared key in place
- 🟡 Real MPC threshold signing (Binance TSS-Lib): k-of-n DKG + signing proven
  and Ethereum-verified in-process (`services/mpc-signer/tss`); live multi-party
  transport + per-customer ceremony is the remaining Phase 2 work
- ⬜ HSM-backed Vault auto-unseal
- ⬜ Documented key ceremony + rotation procedure
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
  tenant's `customer_id` is rejected). Still 🟡, not ✅: only
  `services/api-gateway` threads the real per-request tenant context
  (`set_config('app.current_customer_id', ...)` in
  `postgres.service.ts`/`audit.service.ts`). The other six Go services
  that touch these tables (policy, settlement, billing, webhooks,
  marketplace, temporal-worker) connect as `app_admin` (BYPASSRLS) as an
  explicit interim measure so this change doesn't silently zero out their
  queries — each needs the same set_config() treatment before RLS
  actually protects those paths too (see the comment on `NewPostgresDB`/
  `dsn` in each service's `db.go`). Production RDS also has no `app`/
  `app_admin` role split yet — `infrastructure/terraform` provisions one
  master username only; provisioning the same two roles this migration
  assumes is still open.
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
  Still 🟡, not ✅: this is one proven link, not mesh-wide — the other
  ~10 internal HTTP links in this platform (api-gateway -> policy,
  api-gateway -> mpc-signer, settlement -> chain RPCs excluded, etc.)
  don't have the pattern applied yet, and no service mesh/sidecar
  (Istio, Linkerd, AWS App Mesh) is used — this is direct in-process TLS
  configuration instead, which is simpler to reason about here given
  there's no live Kubernetes/App-Mesh deployment target settled on yet
  (see the ECS-vs-Helm finding elsewhere in this checklist).
- ⬜ Multi-region / DR with RTO/RPO < 1h
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
  (`modules/ecs`). The Helm chart and the ECS Terraform are two
  parallel, never-reconciled deployment targets; only one should exist
  for real launch, and whichever it is, ECS additionally needs real task
  definitions written before it can run anything at all. Terraform's
  `helm` binary was not verifiable in this sandbox (get.helm.sh and the
  apt package are both network-blocked here) — the Helm template edits
  were checked for balanced `{{ }}` and valid base YAML, not rendered.

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
- ⬜ **Threshold signing correctness**: `services/mpc-party` now compiles,
  builds, vets clean, and its (pre-existing but never-before-runnable) test
  suite passes — but it is **not the cryptographic core** and must not be
  read as one. Its `TSSWrapper` is explicitly documented in-code as a
  simplified placeholder that exercises the HTTP round-relay protocol
  (fixed-count polling rounds) without performing valid threshold-ECDSA
  math: `ExecuteRound3to7` "combines" public keys with XOR, and
  `CombinePartialSignatures` sums partial signatures with plain big.Int
  addition. It previously imported bnb-chain/tss-lib's package types
  without ever constructing or driving a real `keygen.LocalParty` /
  `signing.LocalParty` — that dependency has now been removed from
  `go.mod` entirely (`go mod tidy` confirmed nothing in the package used
  it) so the import no longer misrepresents what the code does. The
  **actual** verified tss-lib integration — real `keygen.LocalParty`/
  `signing.LocalParty`, producing an Ethereum-verified signature — is
  `services/mpc-signer/tss/tss.go`, in-process only. Porting that
  message-driven protocol onto mpc-party's live multi-party HTTP transport
  is real, scoped work still ahead, tracked below and in
  `services/mpc-signer`'s own Phase 2 note above — not something "fixing
  compile errors" substitutes for.
- 🟡 `services/settlement` broadcasts against real go-ethereum/Bitcoin Core
  APIs now (previously fabricated transaction hashes and unconditionally
  reported "confirmed"); not yet tested against a live testnet from this
  environment

## Go/No-go

A mainnet deployment moving customer funds is **NO-GO** until every ⬜ in
Cryptography & key management and Application security is ✅, and an external
cryptographic audit + penetration test have passed.
