# Audit scope addendum: what was added after the readiness package

`docs/security/AUDIT-READINESS.md` is the brief for the cryptographic review and the penetration
test. It was written before the work below, so a firm quoting from it alone would under-scope.
This lists what to add, with sizes (non-blank, non-comment lines, excluding tests) so a quote
can be sized, and the specific questions each area raises. Audit against a pinned commit.

## 1. In scope for the cryptographic / protocol review

| Area | Lines | Where | The question for the reviewer |
|---|---|---|---|
| Ceremony liveness fix | ~15 changed | `services/mpc-party/tss_party.go`, `tss_signing.go`, `tss_resharing.go` | A party was published to the message handler before tss-lib's `Start()`, so a message that arrived in between was stored, acknowledged and never re-examined, and a round that needed it hung. Found by CI, fixed by publishing after `Start()`. **Is there any remaining window in which a message can be acknowledged but not processed?** Includes resharing's two-party start. |
| Threshold rule | ~40 | `services/mpc-party/tss_party.go`, `services/api-gateway/src/keys/create-key.validation.ts` | A key's threshold must be a strict majority (`t+1 >= floor(n/2)+1`) and at least 2 parties, enforced in both the gateway and the party. Is that the right rule for this protocol, and is it enforceable only there? |
| Authenticated node messages, replay protection, sealed shares | in the readiness package's priority 1 | `services/mpc-party` | Unchanged since the brief; listed so it is not dropped. |
| File-based share sealing (the Vault fallback) | ~230 | `services/mpc-party/file_seal.go`, `docs/deployment/SHARE-STORE.md` | AES-256-GCM with the party and ceremony bound as AAD, key from a file. Key handling, nonce management, and what a world-readable or group-readable key file allows. |
| Off-site backup encryption | ~600 | `services/backup/offsite.go`, `offsite_s3.go` | Client-side AES-256-GCM in 1 MiB chunks, each chunk's position and a last-chunk flag bound into its authentication; truncation, reordering and chunk-swap detection. Key derivation and rotation. |
| TRISA envelope cryptography | ~120 | `services/api-gateway/src/travel-rule/trisa/trisa-crypto.ts` | AES-256-GCM payload, HMAC-SHA256 over the ciphertext, key and secret sealed with RSA-OAEP-SHA512. Checked against TRISA's own Go implementation in CI (`trisa-interop.spec.ts`) in both directions. Review of the composition (HMAC checked before decrypt, algorithm pinning, nonce use). |
| **PermissionedToken.sol** | ~140 Solidity | `contracts/PermissionedToken.sol` | **Not independently audited and holds the powers of a regulated issuer** (mint, burn, forced transfer, freeze, pause). Needs a smart-contract auditor, not only a cryptographer. Specific questions: the forced-transfer and burn powers, the two-step ownership, `removeHolder` and stranded balances, any reentrancy or accounting error, and whether the cap can be bypassed. |

## 2. In scope for the application penetration test

See [PENTEST-SCOPE.md](PENTEST-SCOPE.md). The surface that did not exist when the readiness
package was written:

| Area | Lines | Where | What to attack |
|---|---|---|---|
| TRISA node | ~660 | `services/api-gateway/src/travel-rule/trisa` | A public mutual-TLS gRPC port. Malformed and oversized envelopes, certificate validation, the inbound customer-matching by beneficiary address, duplicate delivery, counterparty trust bypass. |
| Custodian connectors | ~370 | `services/api-gateway/src/custody` | Server-side request forgery: the gateway fetches URLs an administrator registered. The URL is https-only (localhost excepted), redirects are refused, the credential is the name of a `CUSTODY_TOKEN_*` variable only. Try to read other secrets, reach internal addresses, or make a response forge a balance. |
| Safety controls | ~290 | `services/api-gateway/src/controls` | Freeze and address whitelist bypass on every signing path, including after approval. |
| Deposit sweeps | ~310 | `services/api-gateway/src/sweeps` | Redirecting a standing sweep; the scheduler's cross-tenant read. |
| Tokenisation | ~415 | `services/api-gateway/src/tokenisation` | Registering a contract that is not the audited one; getting an administrative act signed without the quorum; calldata the allowlist should refuse (`token-calls.ts`). |
| OIDC sign-in | ~950 (identity) | `services/api-gateway/src/identity` | Authorization-code + PKCE, state and nonce, ID-token verification, UserInfo fallback with subject match. Tested against one independent provider only. |
| EVM nonce serialisation | ~35 | `services/api-gateway/src/transfers/transfers.service.ts` | A per-(chain, key address) Postgres advisory lock around nonce read to broadcast. Deadlock, starvation, and behaviour when a replica dies holding it. |

## 3. Known issues the reviewer should not have to rediscover

Found and fixed during recent work, listed so a reviewer can judge whether the class is
closed or only the instance:

* A column too narrow for a Solana transaction id (80 vs 88 characters) caused a 500 *after* a
  transfer had been broadcast. Fixed (migration 030). The class: any assumption about an
  external identifier's length.
* ethers caches identical RPC reads for 250 ms; two transfers prepared at once from one key
  shared a nonce, and the second failed with a signature that cannot be redone (it is
  replayed by idempotency key). Fixed by disabling the cache and serialising per key.
* A certified OIDC provider omits email from the ID token; the first login failed. Fixed
  with a UserInfo fallback that requires the subject to match.
* The test for tenant isolation passed vacuously for a period because a shell expansion
  made the "tenant" connection the BYPASSRLS admin role. The isolation itself was correct.
  The class: tests that cannot fail.

## 4. Explicitly out of scope, and why

* `tss-lib` itself (a dependency; `TSS-LIB-ADVISORY-REVIEW.md` is our reading of its advisories).
* The Travel Rule *network*: interoperability with live TRISA members is untested and is a
  certification matter, not a code review.
* Legal characterisation of any token as a security.
* Third-party SaaS (WorkOS, Stripe, Didit) beyond how we call them.

## 5. What to hand over

`scripts/assurance-pack.sh` assembles it: the pinned commit, the dependency lists (npm and Go),
the migration list, the route inventory, the CI workflow definitions, the evidence-collector
output if available, and checksums. The review is a statement about that commit.
