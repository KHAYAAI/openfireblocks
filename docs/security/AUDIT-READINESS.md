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

1. **Committee subsetting.** This was previously listed as an open
   question, on the belief that `LocalPartySaveData` was keyed by original
   position and that re-sorting a committee would produce invalid results.
   **That belief was wrong and the code was broken**: tss-lib subsets the
   save data itself and indexes it by committee position, so carrying
   original DKG indices into a smaller committee panicked
   (`PrepareForSigning: len(ks) <= i`) for every committee except `{1, 2}`.
   It went unnoticed because the gateway always chose the first `threshold`
   parties. Both are now fixed and `TestSigningWithEveryCommittee` covers
   all three committees of a 2-of-3 key.

   What we would still like checked: that the renumbering is correct for
   larger and sparser committees (5-of-9, say), and that a committee
   assembled in a different order by different parties cannot produce a
   valid-looking signature for the wrong key rather than failing.
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
  — `services/policy-service`, and `CheckPolicy` in temporal-worker.
  Note that `ThresholdSigningWorkflow` itself performs no policy check:
  the gate lives in the API layer (`KeysService.enforcePolicy`, shared by
  both signing routes), so anything that can start that workflow in
  Temporal directly bypasses policy entirely. Whether that boundary is in
  the right place is worth an opinion.

### Priority 4 — Penetration test (separate engagement)

Standard external assessment of the deployed API surface. There is now a
reproducible full-stack deployment to point one at — `infrastructure/kind/up.sh`
stands up the whole platform on Kubernetes in one command — but it is a
throwaway local cluster with dev-grade dependencies, not a staging
environment: Vault runs in dev mode, credentials are well-known strings,
and nothing is exposed beyond a port-forward. A real engagement still needs
a real staging environment per the go-live runbook. Scope it against
staging, not production.

---

## 2. What has already been verified, and how

Offered so an auditor can skip re-deriving it — and to be explicit that
"verified" here means *executed against real systems*, not reviewed.

| Claim | How it was verified |
|---|---|
| DKG produces a real, usable threshold key | Real 2-of-3 DKG across three independent OS processes; signature recovers to the derived address (`crypto.SigToPub`) |
| The full customer path works | `POST /keys` → real Temporal workflow → real DKG → `key_pairs` activated with a real address, then signing with that key. ~24s. `services/api-gateway/src/keys/keys.provisioning.live.spec.ts` |
| mTLS on internal links | Real Vault-PKI-issued certs; valid cert accepted, absent cert rejected at the TLS layer. Now also running in-cluster for the party↔party and worker↔party links, which are the ones carrying protocol messages |
| Postgres has a verified warm standby | `infrastructure/kind/postgres-failover-drill.sh`: real streaming replication (walsender in `streaming` state, ~170ms round trip for a canary row), `pg_promote()` out of recovery in ~480ms, the promoted node accepts writes -- which a standby refuses -- and the pre-promotion data survived |
| Vault survives losing a node | Three-replica Raft cluster, one per worker. The node hosting the Raft leader was drained: quorum held, leadership moved, and a party pod scheduled after the drain obtained a fresh certificate through Kubernetes auth |
| A signing path with no cryptography behind it | Removed. `mpc-party` served `/round/*` and `/sign` backed by a stand-in documented as "not cryptographically secure"; no workflow used them, but they were reachable by anything that could reach a party. Deleted, with a test pinning them as 404 |
| Automated cert issuance | `services/vault-pki-init` against a real Vault PKI mount, real handshake with the issued certs; both the token path and (now) the Kubernetes-auth path |
| Tenant isolation | Real Postgres: cross-tenant reads return zero rows; `app` confirmed to lack BYPASSRLS |
| Backup and restore | Real `pg_dump`/`pg_restore` + Vault KV export, restored into an isolated database, row counts and a canary secret verified |
| Database failover | A real `pg_basebackup` streaming standby promoted to writable primary in **252ms**, pre-failover data intact |
| Key rotation and balance migration | Real Vault soft-delete of old shares; real threshold-signed sweep transaction whose recovered sender matches the retiring address |
| Multi-chain address derivation | Bitcoin/Cosmos/Solana checked against each chain's specification computed independently in the tests |
| The whole path on real Kubernetes | Authenticated `POST /keys` → Temporal → real 2-of-3 DKG across three pods **on three separate nodes, over mTLS** → all three sealed distinct shares in Vault → key activated → a 2-of-3 threshold signature recovers to the DKG-derived address. Reproducible: `infrastructure/kind/up.sh`, then `smoke-test.sh` |
| A 2-of-3 key survives losing a party | `infrastructure/kind/node-failure-drill.sh`: a worker node hosting a party the system had just chosen is cordoned and drained; the key still signs (975ms) with the remaining two, and the signature recovers to the DKG-derived address |
| Any committee can sign, not just the first two | `TestSigningWithEveryCommittee`: one DKG, then signing with {1,2}, {1,3} and {2,3}, each recovering to the same address |
| Certificates rotate without a restart | Real Vault PKI with a 20s TTL: three distinct serials, each replaced at two thirds of its life; plus a live TLS handshake showing the server presenting the new serial to a new connection without restarting |
| A threshold-signed transaction is actually spendable | `infrastructure/kind/chain-test.sh`: bytes from `POST /keys/:keyId/transactions` handed to a real geth node, which accepted them, computed the same hash the service predicted, mined them successfully, moved the value, and attributes the transaction to the DKG-derived address |
| Policy governs what is actually signed | `POST /keys/:keyId/transactions` on that cluster: the returned raw transaction was parsed back independently with `ethers`, and its sender is the DKG-derived address **and** its own `unsignedHash` is byte-identical to the digest the ceremony signed |
| Per-pod mTLS via Vault Kubernetes auth | `vault-pki-init` authenticating with its pod's service-account token against a real Vault kubernetes auth backend, issuing a leaf with the service identity as CN and the in-cluster DNS name as a SAN, and the parties then completing a DKG over those certificates |
| Parties are actually spread | Enforced `requiredDuringScheduling` anti-affinity; the three party pods land on three distinct worker nodes, and the chart refuses to schedule them otherwise rather than silently co-locating key shares |
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

1. **Ethereum transactions are broadcast and mined against development
   nodes only.** A threshold-signed transfer built by the gateway is
   accepted and mined by a real geth node running in the cluster, moves
   value, and is attributed by the node to the DKG-derived address
   (`infrastructure/kind/chain-test.sh`). That proves the encoding,
   broadcast and confirmation path. It proves nothing about mainnet
   economics, reorgs, mempool behaviour or public network conditions --
   `geth --dev` is single-signer instant-seal with no consensus.
   **Bitcoin, Cosmos and Solana remain entirely unbroadcast**: their
   `BroadcastTransaction` returns "not implemented" outright.
2. **The cluster deployment is four nodes on one host, and Terraform is
   still unapplied.** The chart has been applied and the whole customer
   path runs on real Kubernetes 1.29.14 with containerd 2.0.2, with the
   three MPC parties on three separate worker nodes under enforced
   anti-affinity, communicating over mTLS with certificates each pod
   obtains from Vault PKI at startup via Kubernetes auth. See
   `docs/deployment/CLUSTER-DEPLOYMENT.md`, which records the eight bugs
   that surfaced only by doing this — including one that made **every** DKG
   ceremony fail under realistic pod CPU limits, and one where enabling
   mTLS made every party pod permanently unrunnable.

   What that is not: the four nodes are containers on a single machine, so
   the parties do not have independent failure domains in any sense that
   survives that machine dying. No node was drained or killed to confirm a
   2-of-3 committee keeps signing through it. Certificate *renewal* is
   untested — issuance works, nothing rotates a 24h cert. Terraform still
   validates only and has never been applied.
3. **No production-scale load.** All timings are single-node local numbers
   and should not be read as capacity data.
4. **Multi-chain signing is unexercised against real networks.** The
   implementations follow each chain's spec and are tested against it, but
   no Bitcoin, Cosmos or Solana node has ever accepted one of these
   transactions. Cosmos supports only `SIGN_MODE_LEGACY_AMINO_JSON`, not
   `SIGN_MODE_DIRECT`.
5. **Regional failover is one component of four.** Only Postgres promotes;
   Vault, api-gateway and Temporal report "not implemented" with the reason.
6. **Two signing routes with different guarantees, and the weaker one is
   now opt-in.** `POST /keys/:keyId/transactions` takes transaction
   fields, builds and hashes the transaction itself, and signs that hash —
   so what policy evaluated and what got signed are provably the same
   transaction, and it refuses to return anything whose signature does not
   recover to the key's own address. That is the strong route.

   `POST /keys/:keyId/sign` still takes a caller-supplied digest. It is
   gated fail-closed by the same policy service, but a digest is opaque:
   **a caller who declares one transaction and submits the digest of
   another gets a policy decision about the wrong transaction.** It is now
   off unless a tenant is granted it explicitly (migration 017), so a new
   tenant can only reach the strong route and enabling the weak one is a
   decision on the record. Worth an auditor's opinion on whether it should
   exist at all.

   Neither route constrains calldata semantics: policy evaluates
   `to`/`value`/`chainId`, so a transfer to a whitelisted address carrying
   a call to something else is within policy as written. Constraining that
   means decoding calldata against an ABI allowlist.
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
