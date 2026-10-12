# Launch readiness

As of 2026-10-11, branch `claude/platform-explanation-h0st5y`. This is a
summary; `LAUNCH-CHECKLIST.md` is the evidence-based source and wins if they
disagree.

## Verdict

| Audience | Verdict |
|---|---|
| Fintech, supervised pilot on testnet or capped balances | **Ready to start.** A real key generation and signature pass on a real cluster (lite profile and AWS full profile), with public HTTPS. An external pentest has run against the AWS deployment and every finding is fixed on this branch (the last fixes are not yet redeployed to staging). Threshold-signed transactions have been accepted by public test networks on four chains (see below); a Stripe run is still to do |
| Banks, governments, uncapped mainnet funds | **Not ready. Bounded pilots only** |
| Production | **Not ready.** Blocked by external work (audit, SOC 2, isolated hosts, legal), not by known engineering gaps |

## What is built and how well it is proven

| Area | State | Proof |
|---|---|---|
| 2-of-3 threshold ECDSA and Ed25519 signing (tss-lib) | Built | Real keygen and signing across processes in CI and on a real AWS cluster; a key survives losing every party |
| Approvals, M-of-N, no self-approval, OIDC roles | Built | Real-Postgres CI job; OIDC job against an independent provider; quorum floor of 2 enforced (no self-service downgrade to a single signer) |
| Tenant isolation (Postgres RLS) | Built | CI tenant-isolation tests |
| Deposit sweeps, TRISA link, multi-custodian, tokenisation | Built | CI specs; TRISA cross-checked against TRISA's Go reference library; token contract on a dev chain |
| Bitcoin, Solana, Cosmos, EVM | **EVM (Sepolia), Solana (devnet), Cosmos (Osmosis testnet) and Bitcoin (testnet4): accepted by public networks.** | Each has a block/slot containing a transaction signed by a 2-of-3 threshold key, 2026-10-11 (`docs/evidence/2026-10-11-sepolia.md`, `-solana-devnet.md`, `-cosmos-osmosis-testnet.md`). Bitcoin testnet4: a threshold-signed spend was mined in block 156069 (`docs/evidence/2026-10-11-bitcoin-testnet4.md`). **ERC-20 stablecoins are proven on a public chain too**: Circle's Sepolia USDC was registered, verified on-chain by the platform and moved from a threshold key (`docs/evidence/2026-10-11-erc20-usdc-sepolia.md`); a euro stablecoin (EURC) too, and OUSD (Open Standard) was registered and verified read-only against its real Ethereum and Base contracts (`-ousd-ethereum-verified.md`; no transfer). EURC followed after EUR ceilings were added to the policy (`-erc20-eurc-sepolia.md`; receipt and Transfer event confirmed, exact recipient-balance check not, see the doc) |
| Billing | Built | Stripe job runs only if `STRIPE_TEST_API_KEY` is set, otherwise it skips; no card has been charged |
| Helm chart, NetworkPolicy, backups | Installed and run on a real AWS cluster (full profile) and locally (lite profile) | Full profile verified on AWS with real generated secrets, public HTTPS via Caddy/Let's Encrypt; NetworkPolicy confirmed to isolate `mpc-signer` to `api-gateway`/`temporal-worker` only |
| Policy enforcement reads the whole transaction, not just its envelope | Built | `POST /sign` now decodes ERC-20 calldata and evaluates the whitelist/amount limit against the real recipient and amount, matching `POST /keys/:id/sign-transaction` -- see pentest remediation below |
| Vulnerability gate | Passing | Go toolchain `go1.26.9` and `golang.org/x/net v0.60.0` clear the GO-2026-66xx advisories; CI green on `5ab7a4f` and `56e6662` |

## Status update, 2026-10-11

**Verdict unchanged:** supervised testnet pilots are ready to start; banks, governments and uncapped mainnet funds
are not; production is blocked by external work, not known engineering gaps.

What changed today, with the evidence for each (all in `docs/evidence/`):

| Area | Result |
|---|---|
| Ethereum Sepolia | A 2-of-3 threshold-signed transfer was accepted and mined (`2026-10-11-sepolia.md`) |
| Solana devnet | Accepted and confirmed (`2026-10-11-solana-devnet.md`) |
| Cosmos (Osmosis testnet) | Accepted and confirmed after the per-chain fee price was set (`2026-10-11-cosmos-osmosis-testnet.md`) |
| Bitcoin testnet4 | Mined in block 156069 (txid `280f69b1…3c1a`); the platform ran its own Bitcoin Core node (`2026-10-11-bitcoin-testnet4.md`) |
| USDC (Sepolia) | Registered, verified on-chain and moved from a threshold key (`2026-10-11-erc20-usdc-sepolia.md`) |
| EURC (Sepolia) | Same path, after EUR ceilings were added to the policy; receipt and contract Transfer event confirmed, the exact recipient-balance check was not (shared burn address, lagging RPC) (`2026-10-11-erc20-eurc-sepolia.md`) |
| OUSD (Ethereum and Base mainnets) | Registered and verified against the live contracts, **read-only; no transfer** (`2026-10-11-ousd-ethereum-verified.md`) |
| Rand tokens (ZARP/ZARC) | Supported in code with rand-denominated ceilings and tests; no public-chain run yet |
| AI agents through MCP | `services/mcp-server`: an agent pays within limits a person set (tokens, recipients, per-payment and 24-hour limits in rand, expiry, revocation); policy, Travel Rule and the 2-of-3 signature still apply; the platform reads nonce and fees and broadcasts; retries never pay twice. **20 of 20 end to end on a local EVM dev chain** (`2026-10-11-mcp-agent-end-to-end.md`), not on a public network. Recorded for four segments (`docs/showcase/v2/`) |

Wording to use externally: "accepted by public test networks" for Bitcoin, Ethereum, Solana, Cosmos and test
stablecoins; "registered and verified on-chain" for OUSD; for AI agents, "AI agents can pay through MCP within
limits a person sets, proven end to end on a local development chain". Do **not** say "source under NDA" (the repository is public; say "the source code is open for review"), mainnet, production-ready,
independently audited, that OUSD / ZARP / ZARC transfers are proven, or that agent payments run on a public network.

Known limits and open items: EUR ceilings (900,000 global, 90,000 pro, 9,000 free) were chosen by engineering and
need the founder's sign-off; the Stripe path has never charged a real card; the staging cluster resets its
Helm settings on every `up.sh` (trust-proxy hops, EVM/Solana/Cosmos RPC, Cosmos gas price) and they must be
reapplied; Solana SPL tokens are not supported (native SOL only); the MCP server and the new agent routes have not been
through a pentest round, agent limits are denominated in rand only, and the recordings' agent sentences are scripted
(no language model in the loop); the independent human pentest, cryptographic
audit, SOC 2 Type II, isolated hosts with HSM, live TRISA, a real custodian, legal/licensing and a capped mainnet
run all remain.

## Compliance and governance update, 2026-10-11 (evening)

**Verdict unchanged.** Today's work moves the SOC 2 / ISO 27001 *preparation* forward. It does not
change what the platform is cleared for.

| Area | Result | Evidence |
|---|---|---|
| Access review in the product | **Built.** Controls > Access review (admins and auditors) lists every person, role, second factor, last sign-in, every AI agent and its limits, and the API key's age, and flags problems by severity. Sign-off needs a one-time code or a fresh SSO session; the server stores its own snapshot with a SHA-256, and the record cannot be changed or deleted (database trigger, row-level security) | 13 tests (8 unit, 5 against real Postgres) in CI; checked in a browser; 160 routes, all guarded (`docs/assurance/ROUTE-INVENTORY.md`) |
| ISMS documentation (ISO 27001:2022) | **Drafted, not approved.** Scope, policy, risk register (24 risks), Statement of Applicability (all 93 controls), objectives, audit/review/corrective-action templates, 17 policies, record templates, tabletop and DR test scripts | `docs/compliance/isms/` |
| Privacy (POPIA, PAIA, GDPR) | **Drafted for counsel.** RoPA, privacy notice, PAIA manual, DPA, DPIA (Travel Rule and agent payments), request procedure, Information Officer registration, cross-border clauses, outreach opt-out | `docs/compliance/isms/privacy/` |
| Incident response plan | **Updated.** Freeze-first containment, 2-of-3 (it said 4-of-7), token revocation; POPIA s22 and GDPR 72-hour notification steps; contacts section (names to fill) | `docs/security/incident-response-plan.md` |
| Account checks | **Built.** GitHub controls check (run against this repo); AWS baseline check (tested against a stub, **not yet run on the real account**); weekly evidence workflow | `scripts/compliance/`, `.github/workflows/compliance-evidence.yml`, `docs/evidence/2026-10-11-compliance-checks.md` |

**Findings from those checks, and where they stand (2026-10-12):**

| Finding | Status | Detail |
|---|---|---|
| 39 GitHub Actions not pinned; 2 workflows without token permissions | **Closed** | All pinned to the commit their tag or branch pointed to (no behaviour change); least-privilege permissions per job. Check re-run: PASS |
| No CODEOWNERS | **Closed** | `.github/CODEOWNERS` added |
| Repository public while material said "source under NDA" | **Closed** (decision: stay public) | All NDA claims removed from website copy, templates, form texts and the 16 Oct follow-ups; full secret scan clean. One Gmail draft (Trail of Bits follow-up) still says the repository will be shared after an NDA: edit before sending |
| Default branch has **no branch protection** | **Open: owner action** | The session's GitHub access refuses repository-settings writes. In GitHub: rename the default branch to `main`, add a ruleset (PR required, CI required, no force push or deletion). **Note:** `deploy.yaml` and `test-and-build.yml` trigger on `main` and will start running after the rename; `test-and-build.yml` is a legacy pipeline that will fail (it builds `./cmd/main.go`, which does not exist): retire or disable it at the same time |
| **Dependabot alerts disabled**; secret scanning unknown | **Open: owner action** | Settings > Code security: Dependabot alerts, secret scanning, push protection |
| **POPIA s69** on outreach | **Held** | Follow-ups due 2026-10-16 stay unsent until counsel confirms |
| Off-site backups never run against a real bucket | **Open: blocked** | Needs AWS access (connector re-authorisation or credentials) |
| AWS baseline never run on the real account | **Open: blocked** | Same: re-authorise the AWS connector, or set `AWS_AUDIT_ROLE_ARN` |

**Compliance timeline (from the Sprinto estimate, unchanged):** SOC 2 Type I about 2–3 months
after signing a platform and approving the policies; ISO 27001 about 3–5 months; SOC 2 Type II
about 6–8 months (needs a 3+ month observation window). The clock has not started: no platform
is signed and no policy is approved.

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
deployment. Detail: `docs/security/PENTEST-ROUND-2-2026-10-10.md`. Round 3 (same day, after the fixes were deployed) found only 2 low
issues (a loose rate limit on the API-key sign-in, and different MFA-verify error messages that revealed
which emails have MFA); both are fixed in code and unit-tested, not yet redeployed. Still open: a round 4
to confirm, and an independent human-led test.

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
| Redeploy `8735cd4` (round-3 fixes) to staging, optional round 4 | Rebuild with `up.sh`, reapply the Helm settings that `up.sh` resets, re-run Shannon | You (or delegate back) |
| Solana devnet run | **Done 2026-10-11** (`docs/evidence/2026-10-11-solana-devnet.md`) | Done |
| Stripe test run | A test key as repo secret `STRIPE_TEST_API_KEY` (CI then runs it) | You |
| Cryptographic audit (tss-lib layer, `PermissionedToken.sol`) | Auditor contract; brief and plan in `docs/assurance/`; outreach emails drafted in `docs/pilot/OUTREACH-EMAILS.md`, not yet sent | You; longest lead time |
| Independent human-led penetration test | Vendor, plus the deployed staging environment (exists) | You |
| SOC 2 Type II | CPA firm, 6-12 months of observed controls | You |
| MPC parties on isolated hosts, `terraform apply`, hardware HSM | Separate hosts or accounts, credentials, HSM | You |
| Live TRISA network | TRISA membership and certificates | You |
| Custody against a real custodian | Custodian account | You |
| Licensing, ToS, privacy policy, insurance, sanctions/KYC vendor | Legal and commercial work; privacy notice, PAIA manual and DPA are drafted in `docs/compliance/isms/privacy/` for counsel | You |
| Protect the default branch, enable Dependabot alerts and secret scanning | Repository admin settings (CA-003, CA-004) | You |
| POPIA s69 position on outreach before the 2026-10-16 follow-ups | Counsel | You |
| Sign Sprinto (or similar); approve policies; name ISMS owner, Information Officer, incident lead | Founder decisions and signatures | You |
| Register the Information Officer; publish privacy notice and PAIA manual | Information Regulator portal; counsel review | You |
| AWS baseline on the real account | Re-authorise the AWS connector, or set `AWS_AUDIT_ROLE_ARN` (SecurityAudit role) | You |

## Commercial and assurance outreach (started 2026-10-09)

Sent 14 emails on 2026-10-09 from TKM@myforgepay.com: 9 to government and regulator
innovation offices (SARB Fintech Unit, Mauritius FSC and Bank of Mauritius, Nigeria SEC, Bank of
Ghana, Rwanda CMA, Seychelles FSA, Bank of Namibia, Bank of Tanzania), 3 to fintechs (Peach
Payments, Ozow, Chipper Cash), 1 to Trail of Bits (cryptographic audit screening) and 1 to the Bank
of England Digital Securities Sandbox. Follow-up drafts sit in Gmail for 2026-10-16. **Hold them until counsel confirms the POPIA s69 position** (see the compliance update above). Form-based
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

Any mainnet transfer, the TRISA link on the live network, the custody
executor against anything but a stand-in, Stripe with a real key, signing
parties on isolated hosts, off-site backups to a real bucket, AI-agent payments
on a public network, the AWS baseline on the real account, and an independent
human-led pentest or cryptographic audit. The token contract is unaudited.
(A real cluster with public HTTPS, a real external AI-driven pentest with its
findings fixed, and transfers accepted by public **test** networks on four chains
are no longer on this list.)

## Critical path

1. Done: CI green on `5ab7a4f` and `56e6662`.
2. Re-run Shannon against the fixed AWS deployment to confirm the 11
   findings are actually closed end-to-end, not just unit-tested.
3. Follow up the audit, pen-test and SOC 2 outreach on 2026-10-16 and engage a firm in each
   category (the Shannon run does not substitute for the human test).
4. Pick the SOC 2 firm and start the 6-12 month observation window.
5. Done: Solana devnet (and Sepolia, Osmosis, Bitcoin testnet4). Still to do: Stripe test mode.
6. Stand up isolated-host MPC parties and a hardware HSM.
7. Protect the default branch and turn on Dependabot and secret scanning (this week; minutes of work).
8. Sign a compliance platform, approve the policy pack, name the roles; start the SOC 2 window.
9. Close `LAUNCH-CHECKLIST.md` section 1, then re-assess.
