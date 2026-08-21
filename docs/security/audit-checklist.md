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
