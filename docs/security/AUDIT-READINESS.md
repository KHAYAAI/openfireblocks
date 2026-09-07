# External Audit Readiness Package

A scoping document for an external cryptographic/security audit of the
signing layer, plus a penetration test.

Purpose: make engaging a firm a short conversation instead of a discovery
project. It states what to audit, where the interesting code is, what has
already been verified and how, and — importantly — what is known to be
unverified, so an auditor spends their time on questions we cannot answer
ourselves rather than rediscovering ones we can.

**Status: not yet engaged.** No firm has been contacted or scheduled.
Selecting and contracting one is a procurement decision, not an engineering
task, and it is on the critical path to launch — see
`docs/security/what-claude-cannot-build.md`.

---

## 1. What we are asking to be audited

In priority order. Item 1 is the one that matters; if budget forces a
choice, buy item 1.

### Priority 1 — Threshold signing (MPC/TSS) correctness

The claim under audit: **no single party, and no fewer than `threshold+1`
parties, can produce a signature or reconstruct a private key.**

| What | Where |
|---|---|
| DKG ceremony (real `tss-lib` `keygen.LocalParty`) | `services/mpc-party/tss_party.go` |
| Threshold signing ceremony | `services/mpc-party/tss_signing.go` |
| Peer-to-peer protocol message relay | `services/mpc-party/tss_handlers.go` |
| Key share sealing at rest | `services/mpc-party/vault_seal.go` |
| Orchestration (never touches key material) | `services/temporal-worker/activities/real_tss.go` |
| Single-key signer (separate, non-threshold path) | `services/mpc-signer/signer.go` |

Specific questions we want answered:

1. **Committee subsetting.** A signing committee is a subset of the DKG
   parties. `StartSigning` subsets `tsscommon.SortedPartyIDs` preserving
   original order/index, because `LocalPartySaveData` is keyed by original
   position. We believe re-sorting would silently produce invalid or wrong
   results. Is the subsetting correct in all cases, including
   non-contiguous committees?
2. **Message relay integrity.** Protocol messages are relayed over HTTP
   between independent processes. Is there any way a malicious or
   compromised party can influence another party's output beyond what the
   protocol permits — replay, reordering, cross-ceremony injection?
   Ceremony IDs are the primary scoping mechanism; is that sufficient?
3. **Abort/failure handling.** What is the state of a ceremony that fails
   partway? Can a party be induced to reuse nonces or partial state across
   ceremonies?
4. **Key share lifecycle.** Shares are sealed to Vault KV v2 on ceremony
   completion and soft-deleted after a retention window on rotation
   (`services/temporal-worker/activities/key_rotation.go`). Is there a
   window where shares are recoverable when they should not be, or
   destroyed when they should not be?
5. **The two signing paths.** `mpc-signer` holds a single key and is
   *not* threshold-based; `mpc-party` is. Both can produce signatures.
   Is the separation clear enough that a caller cannot get a single-key
   signature where a threshold signature was intended?

### Priority 2 — Tenant isolation

The claim: **one customer cannot read or affect another customer's data,
keys, or transactions.**

- Postgres row-level security, forced, with a split `app` (RLS) /
  `app_admin` (BYPASSRLS) role model — `infrastructure/database/migrations/011_row_level_security.sql`
- Per-request tenant context via `set_config('app.current_customer_id', ...)`,
  transaction-scoped — `services/api-gateway/src/database/postgres.service.ts`,
  and the `withTenant` helper in each Go service's `db.go`
- The deliberate exceptions, which are where we would look first:
  `CustomerService` (must resolve identity *before* a tenant context
  exists), `AuditService`'s system-actor writes, and `temporal-worker`
  (orchestrates across tenants by design)

### Priority 3 — Authentication and authorization

- API-key authentication (SHA-256 hashed at rest) — `services/api-gateway/src/auth/`
- Dashboard identity: password + TOTP MFA — `services/api-gateway/src/identity/`
- Enterprise SSO via WorkOS AuthKit, including the account-linking rule
  (link an existing password account only on an IdP-verified email) —
  `services/api-gateway/src/identity/workos-sso.service.ts`
- Policy enforcement, which is fail-closed and gates every signing request
  — `services/policy-service`, and `CheckPolicy` in temporal-worker

### Priority 4 — Penetration test (separate engagement)

Standard external assessment of the deployed API surface. Note that at time
of writing **there is no deployed environment to test** — this must follow
the go-live runbook. Scope it against staging, not production.

---

## 2. What has already been verified, and how

Offered so an auditor can skip re-deriving it — and to be explicit that
"verified" here means *executed against real systems*, not reviewed.

| Claim | How it was verified |
|---|---|
| DKG produces a real, usable threshold key | Real 2-of-3 DKG across three independent OS processes; signature recovers to the derived address (`crypto.SigToPub`) |
| The full customer path works | `POST /keys` → real Temporal workflow → real DKG → `key_pairs` activated with a real address, then signing with that key. ~24s. `services/api-gateway/src/keys/keys.provisioning.live.spec.ts` |
| mTLS on internal links | Real Vault-PKI-issued certs; valid cert accepted, absent cert rejected at the TLS layer. 4 of ~11 links covered |
| Automated cert issuance | `services/vault-pki-init` against a real Vault PKI mount, real handshake with the issued certs |
| Tenant isolation | Real Postgres: cross-tenant reads return zero rows; `app` confirmed to lack BYPASSRLS |
| Backup and restore | Real `pg_dump`/`pg_restore` + Vault KV export, restored into an isolated database, row counts and a canary secret verified |
| Database failover | A real `pg_basebackup` streaming standby promoted to writable primary in **252ms**, pre-failover data intact |
| Key rotation and balance migration | Real Vault soft-delete of old shares; real threshold-signed sweep transaction whose recovered sender matches the retiring address |
| Multi-chain address derivation | Bitcoin/Cosmos/Solana checked against each chain's specification computed independently in the tests |
| The whole path on real Kubernetes | Authenticated `POST /keys` → Temporal → real 2-of-3 DKG across three separate pods → all three sealed distinct shares in Vault → key activated (26s warm, 101s cold) → a 2-of-3 threshold signature recovers to the DKG-derived address. Reproducible: `infrastructure/kind/up.sh`, then `smoke-test.sh` |
| Tenant isolation enforced, not merely configured | On that cluster, with `app` demoted to non-superuser and owning the tables: tenant A sees 1 of 2 rows; a session with no tenant context sees 0 |

---

## 2b. Automated scan results (gosec), triaged

`gosec` runs in CI (advisory) and was run across all Go modules. 149
findings: 37 high, 28 medium, 84 low. Triaged rather than either fixed
wholesale or ignored — offered here so an auditor can skip the ones
already reasoned about and challenge the reasoning where it is wrong.

**Fixed as real:**

- **G115, `chains/solana.go` (6)** — unchecked `int`→`byte` conversions in
  the Solana message serializer. Account indices and the three header
  counts are single bytes, so a transaction with >255 accounts would have
  truncated into a well-formed message addressing the *wrong* accounts,
  and then been signed. Now refused, with the 255 boundary pinned by test.
  This was a genuine bug in newly written code and the single most
  valuable thing the scan found.
- **G101 (11)** — dev-credential DSN fallbacks. Now refused when
  `APP_ENV`/`ENVIRONMENT` is production.

**Assessed as not exploitable in context, with reasoning:**

- **G703/G704 (path traversal / SSRF, 12)** — the "tainted" inputs are
  operator-supplied environment variables (`MTLS_CERT_FILE`, `VAULT_ADDR`),
  not request data. An operator who can set the process environment does
  not need an SSRF to reach anything.
- **G104 (unhandled errors, 72)** — overwhelmingly deferred `Close()` and
  best-effort cleanup. Worth a pass for style; not a security finding.
- **G501/G401 (MD5, 6)** — MD5 is used for backup *checksums*
  (`services/backup/postgres_backup.go`), never for authentication or
  signatures. Integrity-against-corruption, not against an adversary. An
  auditor may reasonably argue for SHA-256 anyway; the cost is low.
- **G117 (struct field matches a secret pattern, 3)** — the `ApiKey` field
  on marketplace integration records. It holds the customer's third-party
  integration key, is never logged, and is only returned to the tenant
  that owns it.

**Deliberately left, and worth an auditor's opinion:**

- **G114 (HTTP servers without timeouts, 6)** — several services use
  `http.ListenAndServe` without read/write timeouts, which is a slowloris
  exposure. These are internal services behind mTLS rather than
  internet-facing, which lowers but does not eliminate it. A reasonable
  hardening item.
- **G118 (`context.Background()` in request handlers, 3)** — goroutines
  that outlive their request. Correctness/resource concern more than a
  security one.

---

## 3. What is NOT verified — audit here first

We would rather hand this over than have it found.

1. **Ethereum transactions have now been broadcast and mined — against a
   local dev node (geth --dev), not a public network.** A signed transfer
   and a full balance-migration sweep were both accepted, mined and
   confirmed, and the swept address ended at exactly 0 wei. This proves
   the encoding, broadcast and confirmation path against a real node; it
   proves nothing about mainnet economics, reorgs, mempool behaviour or
   public network conditions. **Bitcoin, Cosmos and Solana remain entirely
   unbroadcast** — their `BroadcastTransaction` returns "not implemented"
   outright.
2. **The cluster deployment is single-node, and Terraform is still
   unapplied.** The chart now *has* been applied: the whole customer path
   ran on real Kubernetes 1.29.14 with containerd 2.0.2 — see
   `docs/deployment/CLUSTER-DEPLOYMENT.md`, which also records the three
   bugs that surfaced only by doing it, including one that made **every**
   DKG ceremony fail under realistic pod CPU limits. But it ran on one
   kubelet: three `mpc-party` pods on a single node are not three
   independent failure domains, which is the entire security argument for
   threshold signing. Nothing exercised multi-node scheduling, pod
   anti-affinity, or node failure. Terraform still validates only and has
   never been applied. The Kubernetes-auth path in `vault-pki-init` (as
   opposed to the token path) has still never executed, and mTLS was
   disabled for that deployment.
3. **No production-scale load.** All timings are single-node local numbers
   and should not be read as capacity data.
4. **Multi-chain signing is unexercised against real networks.** The
   implementations follow each chain's spec and are tested against it, but
   no Bitcoin, Cosmos or Solana node has ever accepted one of these
   transactions. Cosmos supports only `SIGN_MODE_LEGACY_AMINO_JSON`, not
   `SIGN_MODE_DIRECT`.
5. **Regional failover is one component of four.** Only Postgres promotes;
   Vault, api-gateway and Temporal report "not implemented" with the reason.
6. **A provisioned threshold key cannot be used through the API.**
   `POST /sign` routes to `mpc-signer` — the separate single-key,
   non-threshold path. `ThresholdSigningWorkflow` is reachable only by
   starting it in Temporal directly. A customer can create a 2-of-3 key
   through the public API and has no public API with which to sign with it.
   A functional gap rather than a security one, but worth knowing when
   considering Priority 1 question 5: the two signing paths are not merely
   separate — only one of them is reachable from outside at all.
7. **immudb audit anchoring is unexercised.** The integration is real SDK
   code but has not run against a live immudb instance.
8. **HSM auto-unseal is unapplied.** The AWS KMS seal stanza and IAM are
   configured in Terraform; no Vault node has ever auto-unsealed via it.

---

## 4. Repository orientation for an auditor

```
services/
  mpc-party/          threshold DKG + signing (PRIORITY 1)
  mpc-signer/         single-key signer + multi-chain (separate path)
  temporal-worker/    orchestration; never handles key material
  api-gateway/        the only internet-facing service (NestJS/TypeScript)
  policy-service/     OPA/Rego policy evaluation, fail-closed
  backup/             backup, restore, DR failover
  vault-pki-init/     per-pod mTLS cert issuance (init container)
infrastructure/
  database/migrations/  schema, incl. 011 (RLS)
  terraform/            AWS + Vault (never applied)
  helm/openfireblocks/  the chosen deployment target (never applied)
docs/security/
  threat-model.md            STRIDE per trust boundary
  audit-checklist.md         the honest running status of every control
  key-rotation.md            every key/credential category
  what-claude-cannot-build.md what engineering cannot resolve
```

**Start with `docs/security/audit-checklist.md`.** It is maintained as an
honest record — including things that were found broken and fixed, and
things still broken — rather than a marketing document. If it says a
control is 🟡, the paragraph under it explains exactly what is missing.

Tests marked "live" require real dependencies and skip otherwise; they are
the ones that prove behaviour rather than shape. Run them with the relevant
service up (each file's doc comment gives the exact commands).

---

## 5. Suggested engagement shape

- **Cryptographic review of the signing layer** — a firm with actual
  MPC/threshold-signature experience, not general appsec. This is the
  engagement that matters.
- **Application penetration test** — after a staging environment exists.
- **Infrastructure review** — after Terraform has been applied at least
  once, since reviewing never-applied IaC has limited value.

Sequence them in that order. Item 1 can start immediately against the
repository; items 2 and 3 are blocked on the go-live runbook.
