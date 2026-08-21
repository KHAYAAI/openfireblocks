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
- ⬜ mTLS between services (service mesh)
- ⬜ Multi-region / DR with RTO/RPO < 1h
- ⬜ Secrets via external-secrets / sealed-secrets (no plaintext in cluster)

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
- ⬜ Regulatory reporting (SAR/CTR; POPIA/SARS for ZA)

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
