# Launch readiness

As of 2026-10-09, branch `claude/platform-explanation-h0st5y`. This is a
summary; `LAUNCH-CHECKLIST.md` is the evidence-based source and wins if they
disagree.

## Verdict

| Audience | Verdict |
|---|---|
| Fintech, supervised pilot on testnet or capped balances | **Ready to start.** A real key generation and signature pass on a real cluster (lite profile and AWS full profile), with public HTTPS. An external pentest has run against the AWS deployment and every finding is fixed on this branch. Solana devnet and Stripe runs are still to do |
| Banks, governments, uncapped mainnet funds | **Not ready. Bounded pilots only** |
| Production | **Not ready.** Blocked by external work (audit, SOC 2, isolated hosts, legal), not by known engineering gaps |

## What is built and how well it is proven

| Area | State | Proof |
|---|---|---|
| 2-of-3 threshold ECDSA and Ed25519 signing (tss-lib) | Built | Real keygen and signing across processes in CI and on a real AWS cluster; a key survives losing every party |
| Approvals, M-of-N, no self-approval, OIDC roles | Built | Real-Postgres CI job; OIDC job against an independent provider; quorum floor of 2 enforced (no self-service downgrade to a single signer) |
| Tenant isolation (Postgres RLS) | Built | CI tenant-isolation tests |
| Deposit sweeps, TRISA link, multi-custodian, tokenisation | Built | CI specs; TRISA cross-checked against TRISA's Go reference library; token contract on a dev chain |
| Bitcoin, Solana, Cosmos, EVM | Protocol-tested | Fixtures and mock nodes; never accepted by a real public network |
| Billing | Built | Stripe job runs only if `STRIPE_TEST_API_KEY` is set, otherwise it skips; no card has been charged |
| Helm chart, NetworkPolicy, backups | Installed and run on a real AWS cluster (full profile) and locally (lite profile) | Full profile verified on AWS with real generated secrets, public HTTPS via Caddy/Let's Encrypt; NetworkPolicy confirmed to isolate `mpc-signer` to `api-gateway`/`temporal-worker` only |
| Policy enforcement reads the whole transaction, not just its envelope | Built | `POST /sign` now decodes ERC-20 calldata and evaluates the whitelist/amount limit against the real recipient and amount, matching `POST /keys/:id/sign-transaction` -- see pentest remediation below |
| Vulnerability gate | **Currently failing** -- see CI state | New Go stdlib / `golang.org/x/net` advisories (GO-2026-66xx series) appeared across every Go module since the last green run and are not yet triaged or accepted |

## External pentest (Shannon, 2026-10-08) and remediation

A real AI-driven penetration test (Shannon, by Keygraph) ran against the live
AWS staging deployment (`staging.52.213.16.179.sslip.io`), authenticated as a
real tenant, in exploit mode. It found and proved 11 issues: 4 high, 5
medium, 2 low. All 11 are fixed on this branch (commit `83ce383`, CI-bug
follow-up in `33cff4c`):

| Finding | Severity | Fix |
|---|---|---|
| AUTH-01: MFA re-enroll/disable needed only a bearer JWT (second-factor takeover) | High | Current password required; current TOTP also required once MFA is already enabled |
| AUTHZ-01: webhook SSRF reached loopback, link-local/cloud-metadata, RFC1918, internal DNS | High | Dial-time IP check on every connection, redirects no longer auto-followed, raw transport errors no longer echoed to tenants |
| AUTHZ-02: a tenant admin could self-service approval quorum down to 1 | High | Hard floor of 2, unconditionally |
| MISC-01: `/sign` evaluated policy on the envelope, so ERC-20 calldata bypassed the destination whitelist and amount limit | High | `/sign` now decodes calldata and evaluates policy against the real recipient/amount (shared logic with `/keys/:id/sign-transaction`, extracted to `keys/transfer-intent.ts`) |
| AUTH-02: login lockout response distinguished existing from unknown accounts | Medium | Locked account now throws the same error as a wrong password, with equalized timing |
| AUTH-03: a captured JWT could not be revoked before its 1-hour TTL | Medium | Unique `jti` per token, `POST /v1/auth/logout`, Redis-backed denylist |
| AUTH-04: a captured dashboard session cookie could not be revoked before its 8-hour TTL | Medium | Sign-out now revokes the session server-side via the same denylist |
| MISC-02: velocity limiting silently disabled (fail-open) when Redis was unconfigured | Medium | Fails closed by default; explicit `RISK_ALLOW_DISABLED` opt-out (set for the local/kind dev profile only) |
| MISC-03: Swagger (`/docs`, `/docs-json`) exposed the full API unauthenticated | Medium | Only mounts outside `NODE_ENV=production`, or with explicit `ENABLE_SWAGGER=true` |
| AUTH-05: registration response distinguished existing from new accounts | Low | Same shape and timing regardless |
| MISC-04: AWS WAF Web ACLs were defined but never attached to anything | Low | Added the missing `aws_wafv2_web_acl_association` resources (conditional on a real ALB ARN); documented the equivalent gap for the current single-VM Caddy deployment, which has no ALB to attach to |

**What this shows:** a real, automated adversarial test ran against a real
deployment and found real issues, not hypothetical ones -- the ERC-20
calldata bypass in particular would have defeated the platform's core
custody controls. All 11 are fixed and covered by new or updated tests
(567 gateway tests, 12 webhooks Go tests, all passing; `tsc --noEmit` clean).
**What this does not show:** this is one AI-driven tool's exploit-mode pass,
not an independent human-led penetration test or a cryptographic audit, and
it has not been re-run against the fixed deployment yet to confirm the fixes
hold under the same tool. Full report: available on request (not committed --
contains exploitation detail against the then-current deployment).

## CI state

**Red**, independent of the pentest fixes above. `83ce383` (the fix commit
itself) failed CI on unrelated pre-existing issues (a flaky real-process
mpc-party timing test, and two bugs in this branch's own test setup --
`JwtAuthStrategy` breaking under a test helper's generic auto-mocker, and a
local e2e drill script not updated for AUTH-01's new required password).
Those were fixed in `33cff4c`. The next run then failed on something
unrelated to this session's work entirely: new Go stdlib /
`golang.org/x/net` security advisories (`GO-2026-6599` through
`GO-2026-6617`) now apply to every Go module in the repo and are not in
`docs/security/accepted-vulnerabilities.json`. This needs a decision (bump
the Go toolchain to pick up the fixes, due `v1.26.9`, or accept them with an
owner and expiry per the existing process) before CI is green again. Not
yet done.

## Blocked, and who unblocks it

| Item | Needs | Owner |
|---|---|---|
| Fix the new Go stdlib/x-net advisory gate | A decision: bump Go toolchain, or accept with owner+expiry | Next engineering action |
| Re-run the Shannon pentest against the fixed deployment | Redeploy this branch to the AWS staging VM, re-run Shannon | You (or delegate back) |
| Solana devnet run | An RPC endpoint (and a funded devnet key); paused on faucet rate limits | You |
| Stripe test run | A test key as repo secret `STRIPE_TEST_API_KEY` (CI then runs it) | You |
| Cryptographic audit (tss-lib layer, `PermissionedToken.sol`) | Auditor contract; brief and plan in `docs/assurance/`; outreach emails drafted in `docs/pilot/OUTREACH-EMAILS.md`, not yet sent | You; longest lead time |
| Independent human-led penetration test | Vendor, plus the deployed staging environment (exists) | You |
| SOC 2 Type II | CPA firm, 6-12 months of observed controls | You |
| MPC parties on isolated hosts, `terraform apply`, hardware HSM | Separate hosts or accounts, credentials, HSM | You |
| Live TRISA network | TRISA membership and certificates | You |
| Custody against a real custodian | Custodian account | You |
| Licensing, ToS, privacy policy, insurance, sanctions/KYC vendor | Legal and commercial work | You |

## AWS staging (2026-10-08) and local kind cluster runs (2026-10-06/07)

- **AWS, full profile, real secrets, public HTTPS:** an `m6i.2xlarge` EC2
  instance runs the full chart with freshly generated `ADMIN_API_KEY`/
  `JWT_SECRET` (never the published dev defaults), fronted by Caddy with a
  real Let's Encrypt certificate at `staging.52.213.16.179.sslip.io`. The
  smoke test (real 2-of-3 DKG, Vault share-sealing, policy gate, cross-tenant
  isolation, signature recovery) passed. `/admin*` and `/metrics*` correctly
  return 404 at the public edge. Evidence:
  `docs/evidence/2026-10-08-aws-full-profile-smoke-test.md`. This is the
  deployment the Shannon pentest above ran against.
- **Local kind cluster, lite profile (2026-10-07): PASSED, twice.** Real
  2-of-3 DKG across three pods, Vault-sealed shares, a threshold signature
  through the API, an over-limit denial, cross-tenant isolation, and
  signature recovery matching the key's address. Evidence:
  `docs/evidence/2026-10-07-kind-lite-smoke-test.md`.
- **What neither run shows:** billing with a real card, settlement on a
  public chain, the marketplace, redundant replicas, or MPC parties on
  separate hosts (both runs collocate all three parties on one machine/VM --
  real cryptographic ceremony, not real host isolation).

## Not proven anywhere

A real public chain, the TRISA link on the live network, the custody
executor against anything but a stand-in, Stripe with a real key, signing
parties on isolated hosts, and an independent human-led pentest or
cryptographic audit. The token contract is unaudited. (A real cluster with
public HTTPS, and a real external AI-driven pentest with its findings fixed,
are no longer on this list.)

## Critical path

1. Fix the Go stdlib/x-net advisory CI gate (new, blocking every merge).
2. Re-run Shannon against the fixed AWS deployment to confirm the 11
   findings are actually closed end-to-end, not just unit-tested.
3. Engage the audit firm now (longest lead time), then an independent
   pen-test vendor (the Shannon run does not substitute for this).
4. Pick the SOC 2 firm and start the 6-12 month observation window.
5. Run Solana devnet and Stripe test mode on the existing AWS deployment.
6. Stand up isolated-host MPC parties and a hardware HSM.
7. Close `LAUNCH-CHECKLIST.md` section 1, then re-assess.
