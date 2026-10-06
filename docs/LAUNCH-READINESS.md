# Launch readiness

As of 2026-10-06, branch `claude/platform-explanation-h0st5y`. This is a
summary; `LAUNCH-CHECKLIST.md` is the evidence-based source and wins if they
disagree.

## Verdict

| Audience | Verdict |
|---|---|
| Fintech, supervised pilot on testnet or capped balances | **Ready once an environment exists** (cluster, RPC, Stripe test key) |
| Banks, governments, uncapped mainnet funds | **Not ready. Bounded pilots only** |
| Production | **Not ready.** Blocked by external work, not by engineering |

## What is built and how well it is proven

| Area | State | Proof |
|---|---|---|
| 2-of-3 threshold ECDSA and Ed25519 signing (tss-lib) | Built | Real keygen and signing across processes in CI; a key survives losing every party |
| Approvals, M-of-N, no self-approval, OIDC roles | Built | Real-Postgres CI job; OIDC job against an independent provider |
| Tenant isolation (Postgres RLS) | Built | CI tenant-isolation tests |
| Deposit sweeps, TRISA link, multi-custodian, tokenisation | Built | CI specs; TRISA cross-checked against TRISA's Go reference library; token contract on a dev chain |
| Bitcoin, Solana, Cosmos, EVM | Protocol-tested | Fixtures and mock nodes; never accepted by a real public network |
| Billing | Built | Stripe job runs only if `STRIPE_TEST_API_KEY` is set, otherwise it skips; no card has been charged |
| Helm chart, NetworkPolicy, backups | Rendered and schema-checked | Never applied to a cluster |
| Vulnerability gate | Working | Called Go advisories fixed; three btcd advisories accepted with owner and expiry (`docs/security/accepted-vulnerabilities.json`) |

## CI state

Run for `de4eef6`: every job green except `Go (services/mpc-party)`, which
timed out generating safe primes under `-race` on a shared runner. `807db51`
runs those three real-keygen tests without `-race`. **Not yet seen to pass.**

## Blocked, and who unblocks it

| Item | Needs | Owner |
|---|---|---|
| Build images, install chart on a real cluster | A cluster and registry | You |
| Solana devnet run | An RPC endpoint (and a funded devnet key) | You |
| Stripe test run | A test key as repo secret `STRIPE_TEST_API_KEY` (CI then runs it) | You |
| Cryptographic audit (tss-lib layer, `PermissionedToken.sol`) | Auditor contract; brief and plan in `docs/assurance/` | You; longest lead time |
| Penetration test | Vendor, plus a deployed staging environment | You |
| SOC 2 Type II | CPA firm, 6-12 months of observed controls | You |
| MPC parties on isolated hosts, `terraform apply`, hardware HSM | Separate hosts or accounts, credentials, HSM | You |
| Live TRISA network | TRISA membership and certificates | You |
| Custody against a real custodian | Custodian account | You |
| Licensing, ToS, privacy policy, insurance, sanctions/KYC vendor | Legal and commercial work | You |

## Not proven anywhere

A real public chain, a built Docker image, a real cluster, the TRISA link on
the live network, the custody executor against anything but a stand-in. The
token contract is unaudited.

## Critical path

1. Engage the audit firm now (longest lead time), then the pen-test vendor.
2. Pick the SOC 2 firm and start the 6-12 month observation window.
3. Provide a cluster, a Solana devnet RPC and a Stripe test key; the build,
   install and devnet runs follow in days.
4. Stand up isolated-host MPC parties and a hardware HSM.
5. Close `LAUNCH-CHECKLIST.md` section 1, then re-assess.
