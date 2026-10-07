# Launch readiness

As of 2026-10-07, branch `claude/platform-explanation-h0st5y`. This is a
summary; `LAUNCH-CHECKLIST.md` is the evidence-based source and wins if they
disagree.

## Verdict

| Audience | Verdict |
|---|---|
| Fintech, supervised pilot on testnet or capped balances | **Ready to start once a staging environment exists.** A real key generation and signature now pass on a cluster (lite profile). Solana devnet and Stripe runs are still to do |
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
| Helm chart, NetworkPolicy, backups | Installed on a local cluster (lite profile) | NetworkPolicy and backups not exercised; full profile not run on a small machine |
| Vulnerability gate | Working | Called Go advisories fixed; three btcd advisories accepted with owner and expiry (`docs/security/accepted-vulnerabilities.json`) |

## CI state

Green on `5d20de7` (push and pull request). The `Go (services/mpc-party)` job
had been flaky: the real secp256k1 key-generation tests timed out generating
safe primes under `-race`. All five of those tests now run without `-race`;
everything else in the package stays under it. One green run is not proof the
flake is gone, so treat it as unconfirmed until several runs pass.

## Blocked, and who unblocks it

| Item | Needs | Owner |
|---|---|---|
| Install on a staging cluster with a public URL, then the full (non-lite) profile | A cloud VM or cluster with 8+ vCPU and 16-32 GB | You |
| Solana devnet run | An RPC endpoint (and a funded devnet key) | You |
| Stripe test run | A test key as repo secret `STRIPE_TEST_API_KEY` (CI then runs it) | You |
| Cryptographic audit (tss-lib layer, `PermissionedToken.sol`) | Auditor contract; brief and plan in `docs/assurance/` | You; longest lead time |
| Penetration test | Vendor, plus a deployed staging environment | You |
| SOC 2 Type II | CPA firm, 6-12 months of observed controls | You |
| MPC parties on isolated hosts, `terraform apply`, hardware HSM | Separate hosts or accounts, credentials, HSM | You |
| Live TRISA network | TRISA membership and certificates | You |
| Custody against a real custodian | Custodian account | You |
| Licensing, ToS, privacy policy, insurance, sanctions/KYC vendor | Legal and commercial work | You |

## Local kind cluster runs (2026-10-06 and 2026-10-07, Apple Silicon Mac)

Run by the project owner from `infrastructure/kind/up.sh`. Stated plainly:

- **First run, full profile (2026-10-06): did not complete.** The cluster
  started, all 14 images built and loaded, all 34 migrations applied, the
  Vault PKI bootstrap ran, and the chart installed with every pod `Running`.
  The smoke test then failed: the gateway stopped listening during
  `POST /keys` and `kubectl` timed out. No logs were collected, so the cause
  is unknown. The Docker VM had about 5.8 GB of memory, which is a suspect.
- **Three script bugs found and fixed** on the way: bash 3.2 has no
  `mapfile`; an empty array under `set -u`; images imported for a hardcoded
  `amd64` on an arm64 host.
- **Second run, lite profile (2026-10-07): PASSED, twice.**
  `LITE=1 infrastructure/kind/up.sh` deploys only the signing path (gateway,
  three MPC parties, signer, policy service, worker), one replica each.
  `smoke-test.sh` then:
  - ran a real 2-of-3 distributed key generation across three pods (active
    after 52 s, then 46 s on the repeat);
  - confirmed all three parties sealed a share in Vault;
  - refused raw-digest signing by default, then allowed it once granted;
  - produced a threshold signature from parties 1 and 2 through the API;
  - denied an over-limit request (403) and blocked another tenant from using
    the key (404);
  - recovered the signer from the signature and matched it to the key's
    address.
- **What this does and does not show.** It shows the threshold key
  generation, sealed shares, signing, policy gate and tenant isolation work
  together on a real Kubernetes cluster. It does not cover billing,
  settlement, compliance, webhooks, the marketplace or redundant replicas
  (none were deployed), a public chain, or the full profile on a small
  machine. The earlier gateway stop was not reproduced in the lite profile and
  remains unexplained. `infrastructure/kind/diagnose.sh` captures the
  evidence if it recurs.

## Not proven anywhere

A real public chain, the full chart on a small machine, the TRISA link on the
live network, the custody executor against anything but a stand-in, Stripe
with a real key, and signing parties on isolated hosts. The token contract is
unaudited. (Built images and a real cluster are no longer on this list: see
the kind runs above.)

## Critical path

1. Engage the audit firm now (longest lead time), then the pen-test vendor.
2. Pick the SOC 2 firm and start the 6-12 month observation window.
3. Stand up a staging environment with a public URL, then run Solana devnet
   and Stripe test mode. A Solana devnet RPC and a Stripe test key are needed.
4. Stand up isolated-host MPC parties and a hardware HSM.
5. Close `LAUNCH-CHECKLIST.md` section 1, then re-assess.
