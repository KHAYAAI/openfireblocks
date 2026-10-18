# Launch readiness

As of 2026-10-18, branch `claude/platform-explanation-h0st5y`. This is a
summary; `LAUNCH-CHECKLIST.md` is the evidence-based source and wins if they
disagree.

## Verdict

| Audience | Verdict |
|---|---|
| Fintech, supervised pilot on testnet or capped balances | **Ready to start.** A real key generation and signature pass on a real cluster (lite profile and AWS full profile), with public HTTPS. An external AI-driven pentest has run against the AWS deployment and every finding is fixed and was redeployed to staging on 2026-10-17 (a fourth run, covering the newest routes, is deferred until after the separate-hosts work; CloudHSM is deferred on cost, see step 7 below). Threshold-signed transactions have been accepted by public test networks on four chains (see below); a Stripe run is still to do |
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
reapplied; Solana SPL tokens ran on devnet on 2026-10-17; the MCP server and the new agent routes have not been
through a pentest round, agent limits are denominated in rand only, and the recordings' agent sentences are scripted
(no language model in the loop); the independent human pentest, cryptographic
audit, SOC 2 Type II, isolated hosts with HSM, live TRISA, a real custodian, legal/licensing and a capped mainnet
run all remain.

## Build update, 2026-10-16

**Verdict unchanged.** Four items that needed nothing from outside the team are now built and tested.
None of them has been deployed to staging yet: pushes to the branch are blocked until the GitHub ruleset
is retargeted.

| Item | Result | Evidence |
|---|---|---|
| Per-user wallets (wallet-as-a-service) | **Built.** `POST /wallets` gives one of a customer's users their own 2-of-3 threshold key on each chain they need, under the customer's own reference (no personal information). Creation is idempotent. A per-user freeze stops every signing route; the API key can freeze, but only an admin can unfreeze. The database refuses re-pointing, deleting, or linking another organisation's key | 7 tests against real Postgres; `docs/USER-WALLETS-AND-SOLANA-TOKENS.md` |
| Solana SPL tokens | **Built, not yet on devnet.** USDC/USDT-style tokens through the existing Solana route, with policy, approvals and Travel Rule on the token amount. Token accounts are derived and the recipient's is created when needed. The signer refuses, before the ceremony: wrong decimals, too few tokens, frozen accounts, too little SOL for fees and rent, and Token-2022 mints | Derivation and message checked against `@solana/spl-token` and `@solana/web3.js`; 9 Go and 9 gateway tests. A devnet transfer is still to do |
| Travel Rule retention | **Built.** A logged purge that can remove only records older than the five-year legal minimum (enforced in the database trigger, not only in code), and only finished ones | 6 tests against real Postgres |
| Agents never receive Travel Rule personal data | **Tested.** No agent route returns IVMS101 names, addresses or ID numbers | 1 new test (13 in the agent suite) |

Full gateway suite: 59 suites, 628 tests passing. Signer: all Go packages pass. 175 routes, all guarded or deliberately public.

## Staging update, 2026-10-17

**Verdict unchanged.** Staging now runs the current code, the items built on 2026-10-16 were proven on a public
test network, and three controls that existed only on paper have each run once against real AWS. Everything
below is evidenced in `docs/evidence/` and `docs/security/`; what it does **not** show is listed after the table.

| Area | Result | Evidence |
|---|---|---|
| Staging redeploy | Rebuilt at `61079b5` with the existing secrets (not the dev defaults); 4 migrations applied (036-039); settings that `up.sh` resets are now one script (`~/reapply.sh`); smoke test **PASS** (real key generation, 2-of-3 signature recovers to the address, over-limit denied, cross-tenant refused) | `2026-10-17-staging-redeploy-spl-wallets-retention.md` |
| Solana SPL token on devnet | Devnet USDC registered and verified from the chain (6 decimals); a 2-of-3 key sent 1 USDC, policy and the Travel Rule applied to the token amount. An unpriced token is treated as over the Travel Rule threshold and refused until details are given | same |
| User wallets | A wallet was created, sent 0.5 USDC from its own key, then frozen: the wallet route (409), the key's own route (403) and the generic transfer route (403) all refused, with no signature produced | same |
| Travel Rule retention | Dry run on staging: cutoff exactly five years back, 0 rows, logged; a 30-day period refused | same |
| Signed images on a real registry | 14 images built, pushed to private ECR, signed keyless and given SBOM attestations by the deploy workflow through GitHub OIDC (no stored AWS key). `api-gateway`'s signature **and** its SBOM attestation verified; a wrong identity was rejected | `2026-10-17-cosign-ecr.md` |
| Off-site backups | A full backup (database and key shares) uploaded encrypted to a Governance-locked bucket in another region; the backup login cannot delete; three faults found and fixed on the way (a wrong key, no CA bundle in the image, no Vault address for the backup) | `2026-10-17-offsite-backup-s3.md` |
| Restore from the off-site copy | Fetched with only the bucket and the saved checksums, restored into scratch copies: **60 of 60** Vault key-share paths identical, the marker before the backup present and the one after absent; 207 seconds. Scratch copies and decrypted files deleted | `2026-10-17-dr-offsite-restore-scratch.md` |
| Our own pentest checks of the new routes | 26 hand-run checks with two tenants (cross-tenant access, no or wrong credentials, malformed wallet and token input): all as expected, no findings | `docs/security/PENTEST-ROUND-4-PRECHECKS-2026-10-17.md` |
| Small fixes found by doing it | Registering a duplicate token symbol answered 500 (now 409); the deploy workflow's gate jobs could never pass (now repaired), and it pushed `:latest` to tag-immutable repositories (removed); the backup chart had no Vault setting of its own (added); `reload-image.sh` rebuilds one image | commits `6e9b642`, `5cc526f`, `9a255a8`, `4a31b38` |
| GitHub | The default branch is `claude/openfireblocks-main`; pull request #19 merged to it. The ruleset now has a bypass actor | |

**What this does not show.**
- *(Closed on 2026-10-18, see the update below: the 409 fix is live, a recipient's token account was created on devnet, one transaction was checked on the explorer, and all 14 signatures were verified for the first tag.)* Still open from this list: a normal push to the default branch still needs a Snyk token (or that step stays skipped with a warning).
- **No full rebuild from the off-site backup has been done**, so there is no measured recovery time. The nightly backup job has not yet been seen to run. The shares were decrypted onto a staging host for the restore check, acceptable for test keys only. The backup's Vault token on staging is the development root token.
- The agent routes and MCP server were not part of the 26 checks and have still not been through a pentest round.

## Staging update, 2026-10-18

**Verdict unchanged.** The gaps listed under the 2026-10-17 update were worked through. Evidence is in
`docs/evidence/2026-10-18-staging-checks.md`.

| Area | Result |
|---|---|
| Nightly backup | The scheduled job ran by itself at 02:17:02 UTC: a third backup is in the bucket, with all three files |
| Duplicate token symbol | Live on staging: registering `USDC-DEV` again answers **409** with an explanation |
| Recipient with no token account | A send of 1 USDC-DEV to a fresh address **completed**: the platform created the token account, the sending key paid rent of 1,488,440 lamports (read from the node), fee 5,000 |
| Solana explorer | The transaction above was checked by a person on the explorer: Success, Finalized, the fee payer is the first key, fee 0.000005 SOL |
| Signed images | All **14** images for the first tag verify against the deploy workflow's identity (14 PASS). Rebuilt from the merged default branch (`2571281`): gates and all 14 builds passed, and all 14 new images verify too |
| Old backup access key | Deleted; the backup login has one working key |
| Saved secrets | Both tenant API keys return 200 against the platform, and the saved backup encryption key matches the cluster's (compared by fingerprint, never printed) |
| Deploy gates | The first run with gates on failed at the secret scan (the shallow checkout lacked history); fixed in pull request #46 (full-history checkout). Re-run after the merge: both gates and the 14 builds passed. The same run's "Deploy to Staging" job fails at its AWS credentials step (not used by the single-server staging; not investigated) |

**Disaster-recovery rebuild, done later the same day** (`docs/evidence/2026-10-18-dr-rebuild-fresh-server.md`): a new
server was rebuilt from the off-site backup alone. The database came back with identical row counts, 66 of 66 key shares
matched, and the restored parties signed for the original address. **Measured recovery time: 41 minutes 42 seconds**
(including the faults found: no BuildKit on the new server, and two defects in the recovery drill, now fixed). Limits: one
key was restored, not all 24; the checksums needed to fetch a backup exist only in the cluster; test keys only.

**Unfreeze, done:** a signed-in organisation admin unfroze the frozen wallet through the organisation route (the reason was recorded with their email). There is no wallets screen in the console yet.

**A real bug found by CI and fixed (`75bb1f6`; verified locally and CI passed all 30 checks):** the signing, key-generation and refresh drivers could drop a party's
last protocol message when it finished, leaving its partner waiting for ever. It appeared only under load (most often
in restore-then-sign) and could not produce a wrong signature; a retry worked. A regression test pins it. It is merged (`c585a2e`, pull request #47) and staging has been
rebuilt with it: the smoke test (real key generation, 2-of-3 signature) passed afterwards.

**Dependency updates:** of the 16 open Dependabot pull requests, six were rebased onto current code, re-tested (60 of 60
checks each) and merged: `actions/setup-node`, `setup-python`, `setup-go`, jest, testify and the Prometheus client. Their
earlier `mpc-party` failures were the signing-driver hang above, not the bumps. `actions/checkout` 4 to 7 (#1) is being
recreated by Dependabot. Not touched yet, each needing its own review: the Vault client, immudb, the Temporal SDK, OPA 1
(a major version that can break the screening policies), NestJS 11 and TypeScript 6 and 7. Dependabot's own branches were
blocked by the repository ruleset until `dependabot/**` was excluded.

**Still not shown.** Separate hosts; hardware HSM (deferred, see step 7 below).

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
| Default branch has **no branch protection** | **Open: owner action, part done (2026-10-12)** | Default branch renamed to `claude/openfireblocks-main` (not `main`; `deploy.yaml`/`test-and-build.yml` therefore stay dormant). Ruleset "Neo-rules" is active with the right rules (no deletion, no force push, PR required, 0 approvals) but **targets no branch**, and its required-status-checks list is empty: add target "Include default branch" and the CI checks. Deferred by the owner; does not block pilots |
| **Dependabot alerts disabled**; secret scanning unknown | **Open: owner action, deferred** | Settings > Code security: Dependabot alerts, secret scanning, push protection. Does not block pilots; needed before the SOC 2 evidence window starts |
| **POPIA s69** on outreach | **Held** | Follow-ups due 2026-10-16 stay unsent until counsel confirms |
| Off-site backups never run against a real bucket | **Done 2026-10-17, restore still open** | A full backup uploaded encrypted to a locked bucket and a delete was refused (`docs/evidence/2026-10-17-offsite-backup-s3.md`); a restore from it into a fresh cluster has not been done |
| AWS baseline never run on the real account | **Open: blocked** | Same: re-authorise the AWS connector, or set `AWS_AUDIT_ROLE_ARN` |

**Demo videos, 2026-10-12:** all four re-recorded on the full local stack (21/21 end-to-end
checks) with narration pacing and cue files; `scripts/video/narrate.py` produces a voice-over through a
local VoiceStudio server (`docs/showcase/NARRATION.md`). Not yet narrated: VoiceStudio has to run on the
founder's machine, with a commercially licensed voice model (its default OmniVoice is non-commercial).

**Compliance timeline:** see "Measured timeline to certification and bank readiness" below. The
clock has not started: no platform is signed and no policy is approved.

## Measured timeline to certification and bank readiness (2026-10-12)

Assumptions: the compliance platform (Sprinto) is signed on 16 October 2026; the founder gives
it about 8–10 hours a week; auditors need 2–4 weeks' notice; the South African December–January
shutdown costs about three weeks. The vendor's own estimate (ISO 27001 and SOC 2 Type I by late
November, Type II in about 4.5 months) assumes none of that slack. These are planning dates,
not commitments.

**Compliance**

| Item | Realistic date | What drives it |
|---|---|---|
| Onboarding; fix what the platform flags (GitHub settings, AWS, laptops) | Early November 2026 | Connect AWS, GitHub, Google Workspace; laptop agent; CA-003, CA-004 |
| Policies approved, roles named, training done | Mid-November 2026 | Founder review of the drafts in `docs/compliance/isms/` |
| Background checks | Mid-November 2026 | Provider turnaround |
| GDPR readiness (no audit) | Late November 2026 | Counsel review of the privacy notice and DPA |
| POPIA: Information Officer registered, PAIA manual, s22 steps | Late November 2026 | Counsel review; registration itself takes a day |
| Penetration test (vendor partner, must include the MCP server) | Early to mid-December 2026 | 1–3 weeks to book, 1–2 weeks testing, fixes and retest |
| SOC 2 Type I | Audit as of mid-December 2026; report late January to February 2027 | 6–8 weeks of readiness, then auditor scheduling and the holidays |
| ISO 27001 | Stage 1 in December 2026, Stage 2 late January 2027; certificate late February to March 2027 | The ISMS must run for a while, with an internal audit and a management review on record before Stage 2 |
| SOC 2 Type II | April to May 2027 | 3-month observation window from about mid-December, then fieldwork and the report |

**Beyond compliance (banks, governments, uncapped funds)**

| Item | Realistic date |
|---|---|
| Signing parties on separate hosts, hardware HSMs | January to February 2027 |
| Live TRISA membership; a real custodian | Q1 2027 |
| Capped mainnet pilot | February to March 2027 |
| Independent cryptographic audit (tss-lib layer, token contract) | March to June 2027 |
| Crypto-asset service provider licence (only if FORGE itself holds client funds) | Late 2027 |

**What can be said to buyers, and when**

| Period | Position |
|---|---|
| Now to January 2027 | Fintech testnet and capped pilots. "SOC 2 Type I and ISO 27001 audits scheduled for December and January." |
| February to March 2027 | SOC 2 Type I report, ISO 27001 certificate, pentest report: enough to start most bank vendor-risk reviews |
| April to May 2027 | SOC 2 Type II report, which many banks and government buyers require before production |
| Mid-2027 | Production with banks, once the cryptographic audit is done and the signing parties run on separate hosts |

**Faster:** book the auditor and the pentester as soon as the platform is signed, before the holiday
rush; about 15 hours a week instead of 8–10; start the Type II window right after onboarding
(some auditors allow it), which could bring Type II to about March 2027. **Slower:** an
auditor's first slot in January, or pentest findings that need real engineering.

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
| Redeploy round-3 fixes to staging | **Done 2026-10-17** (at `61079b5`) | Done |
| Pentest round 4 (Shannon) on the redeployed staging, including wallets, tokens, retention, agents | Deferred until after separate hosts and the HSM; our own checks are done. Setup notes and brief are saved; use the Anthropic-compatible DeepSeek endpoint and `--validate-model` first | You |
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
| AWS baseline on the real account | Re-authorise the AWS connector, or set `AWS_AUDIT_ROLE_ARN` (SecurityAudit role). The account now also holds ECR, an OIDC role and a locked backup bucket, so run it again | You |

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
parties on isolated hosts, restoring **every** key after a rebuild at once (one key was restored on a fresh
server; the nightly checksums are not kept off the cluster), AI-agent payments on a
public network, the AWS baseline on the real account, and an independent
human-led pentest or cryptographic audit. The token contract is unaudited.
(A real cluster with public HTTPS, a real external AI-driven pentest with its
findings fixed and redeployed, transfers accepted by public **test** networks on
four chains, an SPL token and a user-wallet transfer on devnet, signed images in
a real registry, and an off-site backup in a real locked bucket are no longer on
this list.)

## What is needed next (updated 2026-10-18)

In the order to do them. "You" means it needs a person with access or authority; "Engineering" can be done
from here once you say go.

| # | Step | Effort | Who | Why now |
|---|---|---|---|---|
| 1 | ~~Merge this branch into the default branch~~ **Done** (#44, #46, #47) | | | |
| 2 | ~~Check the nightly backup ran; delete the wrong access key~~ **Done 2026-10-18** | | | |
| 3 | ~~Verify all 14 images' signatures; run the repaired Deploy workflow~~ **Done 2026-10-18** (14 of 14 for both `2571281` and `c585a2e`). Still open: the run's "Deploy to Staging" job fails at its AWS credentials step | | | |
| 4 | **Done 2026-10-18 (41 min 42 s; one key restored).** Disaster-recovery rebuild (Part B): fresh server, `up.sh`, restore database and key shares, stand the parties back up, sign with a restored key and check the address is unchanged, timed | half a day, a second instance for a day | You, with Engineering writing the commands | Gives the measured recovery time; the biggest open item in the backup story |
| 5 | ~~Redeploy staging; recipient with no token account; unfreeze~~ **Done 2026-10-18** (staging runs the signing-driver fix) | | | |
| 5b | **Keep the backup checksums off the cluster** so a backup can be fetched after total loss (suggested task queued) | 1 hour | Engineering | Found by the rebuild; today a nightly backup cannot be fetched without checksums saved elsewhere |
| 5c | **Restore every key after a rebuild** with one command, and restore the older test keys on staging | half a day | Engineering | The rebuild restored one key of 24; the party restart left the older staging keys unable to sign |
| 5d | Review the remaining Dependabot pull requests one at a time (OPA 1, NestJS 11, TypeScript, Vault client, immudb, Temporal) and the `checkout` bump | 1-2 days | You, with Engineering | Old dependencies; OPA 1 and NestJS 11 are major versions |
| 6 | **Signing parties on separate hosts** (three operators, three AWS accounts), then a new key generated on them | 1-2 weeks | Three people | The step between bounded pilots and real money |
| 7 | **CloudHSM** for the single-key signer. **Deferred, 2026-10-18:** about US$1,100 a month for one HSM and about US$2,200 for the two that high availability needs (hourly, so billed whether or not it is used; confirm the figure for the region on AWS's pricing page). It covers only the single-key path, not the 2-of-3 shares. The signing code already passes against SoftHSM2 in CI. Revisit when a customer or auditor requires FIPS 140-2 Level 3 custody, or once revenue covers it; a one-afternoon test (one HSM, then deleted) would cost a few dollars | about a week | You | Hardware key custody for that path; the 2-of-3 shares are not covered by it |
| 8 | **Pentest round 4** (Shannon), then commission the independent human-led test | 1-2 days, plus a vendor | You | Most useful on the final shape of the system (after 6 and 7) |
| 9 | Re-run the AWS baseline check once the AWS connector is re-authorised | 30 min | You | New resources exist in the account |
| 10 | Stripe test key as a repository secret | 5 min | You | Lets CI run the billing test |

**Needs a decision or signature, not engineering:** engage the cryptographic auditor and the SOC 2 firm; sign
the compliance platform and approve the policy pack; name the ISMS owner, Information Officer and incident
lead; register the Information Officer; counsel on licensing, terms, privacy and POPIA s69; a custodian
account and TRISA membership; insurance and a KYC/sanctions vendor; protect the default branch properly and
turn on Dependabot and secret scanning.

**What would change the verdict.** For banks and uncapped mainnet funds: steps 6, 7 and 8, the cryptographic
audit, and SOC 2 Type II. For a supervised fintech pilot on testnet or capped balances, steps 1 to 5 are the
ones that make it safer; none of them is a blocker to starting.
