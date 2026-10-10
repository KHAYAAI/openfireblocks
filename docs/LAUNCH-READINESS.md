# Launch readiness

As of 2026-10-10, branch `claude/platform-explanation-h0st5y`. This is a
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
| Bitcoin, Solana, Cosmos, EVM | Protocol-tested | EVM and Bitcoin were driven end to end against a private multi-node EVM network and Bitcoin Core in a controlled mode. Solana and Cosmos used fixtures and mock nodes. **No chain has been accepted by a public network yet.** Sales material must say "private test network", not "real network" |
| Billing | Built | Stripe job runs only if `STRIPE_TEST_API_KEY` is set, otherwise it skips; no card has been charged |
| Helm chart, NetworkPolicy, backups | Installed and run on a real AWS cluster (full profile) and locally (lite profile) | Full profile verified on AWS with real generated secrets, public HTTPS via Caddy/Let's Encrypt; NetworkPolicy confirmed to isolate `mpc-signer` to `api-gateway`/`temporal-worker` only |
| Policy enforcement reads the whole transaction, not just its envelope | Built | `POST /sign` now decodes ERC-20 calldata and evaluates the whitelist/amount limit against the real recipient and amount, matching `POST /keys/:id/sign-transaction` -- see pentest remediation below |
| Vulnerability gate | Passing | Go toolchain `go1.26.9` and `golang.org/x/net v0.60.0` clear the GO-2026-66xx advisories; CI green on `5ab7a4f` and `56e6662` |

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

## External pentest, round 2 (Shannon, 2026-10-10)

Run against the redeployed, fixed staging (see `docs/evidence/2026-10-10-aws-redeploy-fixed-branch.md`).
Result: 4 findings (1 critical, 2 medium, 1 low), and none of the 11 round-1 findings reproduced. The
critical one was real and new: `POST /sign` did not honour the organisation freeze, address whitelist or
approval gate. All four are fixed in code and unit-tested; they are not yet re-verified on the live
deployment. Detail: `docs/security/PENTEST-ROUND-2-2026-10-10.md`. A round 3 after redeploy, and an
independent human-led test, remain open.

## CI state

**Green on `5ab7a4f` (run 441) and `56e6662` (run 443), 2026-10-09.** How it got there, in order:

1. `83ce383` (the pentest-fix commit) failed on a flaky real-process
   mpc-party timing test plus two bugs in this branch's own test setup
   (`JwtAuthStrategy` breaking under a test helper's generic auto-mocker, a
   local e2e drill script not updated for AUTH-01's new required password).
   Fixed in `33cff4c`.
2. The next run failed on something unrelated to this session's work
   entirely: new Go stdlib / `golang.org/x/net` security advisories
   (`GO-2026-6599` through `GO-2026-6617`) appeared across every Go module
   and were not in `docs/security/accepted-vulnerabilities.json`. Fixed in
   `79604e4` by bumping the Go toolchain to `go1.26.9` and
   `golang.org/x/net` to `v0.60.0` everywhere each is actually used (not
   blanket-added; confirmed per-module from CI's own "in stdlib" vs "in
   golang.org/x/net" output). Caught and reverted a side effect along the
   way: the dependency bump auto-raised the public `sdks/go` SDK's minimum
   Go version from 1.21 to 1.26, which is a real compatibility break and
   got its own decision (reverted; that module's advisories were pure
   stdlib and needed no `x/net` bump at all).
3. A stray committed build binary in `services/api-gateway/test/trisa-interop/`
   got rebuilt as a side effect of step 2's rebuild; removed and gitignored
   in `bc29787` (the test already builds its own fresh copy at run time and
   never read the committed one).
4. That rebuild also surfaced a real break the advisory fix caused: 4
   Dockerfiles (`backup`, `mpc-party`, `mpc-signer`, `temporal-worker`)
   still built `FROM golang:1.25-bookworm`, older than those 4 modules'
   `go.mod` now requires, with no network access inside the build to
   fetch a newer one. Fixed in `5ab7a4f` by bumping those 4 images to
   `golang:1.26-bookworm`, matching the other 9 Dockerfiles' existing
   convention of tracking their own module's `go` directive.

Confirmed green: the push runs for `5ab7a4f` and `56e6662` both completed
with conclusion `success`, including the mpc-party ceremony tests. The
earlier mpc-party timeouts (three different tests across three runs, in code
untouched this session) did not recur on either run, which supports runner
resource flakiness rather than a code defect. Keep watching; if one recurs,
re-run the job three times and bisect only if it fails identically each time.

## Blocked, and who unblocks it

| Item | Needs | Owner |
|---|---|---|
| Watch for mpc-party ceremony timeouts recurring | Passed on the last two runs; re-run and bisect only if one recurs | Engineering, low priority |
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

## Commercial and assurance outreach (started 2026-10-09)

Sent 14 emails on 2026-10-09 from TKM@myforgepay.com: 9 to government and regulator
innovation offices (SARB Fintech Unit, Mauritius FSC and Bank of Mauritius, Nigeria SEC, Bank of
Ghana, Rwanda CMA, Seychelles FSA, Bank of Namibia, Bank of Tanzania), 3 to fintechs (Peach
Payments, Ozow, Chipper Cash), 1 to Trail of Bits (cryptographic audit screening) and 1 to the Bank
of England Digital Securities Sandbox. Follow-up drafts sit in Gmail for 2026-10-16. Form-based
outreach (VALR, OVEX, Luno, Yellow Card, Cross River Bank, plus pentest and SOC 2 firms) is
prepared but not yet submitted. No replies yet. Details and tracker: `docs/pilot/OUTREACH-BATCH-1.md`,
`docs/pilot/FORM-TEXTS-BATCH-2.md`, `docs/pilot/NAMED-CONTACTS.md`.

Wording used with institutions, and what is true behind it:

| Statement made | State |
|---|---|
| An AI-driven penetration test is complete and all findings are fixed | True. Fixes are unit-tested but the test has not been re-run against the fixed deployment |
| An independent human-led penetration test is being arranged | Outreach only. No vendor is engaged |
| An independent cryptographic review is being arranged | One screening email sent (Trail of Bits). No firm is engaged |
| A SOC 2 programme is planned to begin within 1-2 months | A plan, not a commitment. No auditor or platform is chosen |
| Cleared for testnet pilots, not for material funds | True. Pilot priced at R450,000 for six months |

Competitive note: Absa launched a bank-led digital-asset custody service (with Ripple) on
2026-10-02, so South African banks are competitors before they are buyers.

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

1. Done: CI green on `5ab7a4f` and `56e6662`.
2. Re-run Shannon against the fixed AWS deployment to confirm the 11
   findings are actually closed end-to-end, not just unit-tested.
3. Follow up the audit, pen-test and SOC 2 outreach on 2026-10-16 and engage a firm in each
   category (the Shannon run does not substitute for the human test).
4. Pick the SOC 2 firm and start the 6-12 month observation window.
5. Run Solana devnet and Stripe test mode on the existing AWS deployment.
6. Stand up isolated-host MPC parties and a hardware HSM.
7. Close `LAUNCH-CHECKLIST.md` section 1, then re-assess.
