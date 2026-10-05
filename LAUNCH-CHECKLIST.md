# OpenFireblocks launch checklist

**Status: NOT production ready.** Suitable for a supervised pilot on testnet
or capped balances. Not cleared for uncapped mainnet funds.

This file used to declare the platform "PRODUCTION READY / APPROVED FOR
PRODUCTION LAUNCH". That was wrong: it listed files that do not exist
(`services/auth/*`, `api-gateway/middleware.go`), Terraform modules that do
not exist, and ticked a security review, DR testing and SOC 2 controls that
have not happened. It has been replaced with claims that can be checked in
this repository. Where a box is ticked, there is a file, test or workflow
that proves it.

Companion documents, in the order to read them:

- `docs/LAUNCH-THESIS.md` part 2 — what is built, read out of the code.
- `docs/security/what-claude-cannot-build.md` — what no coding session can
  produce (licences, audits, insurance, real accounts).
- `docs/security/AUDIT-READINESS.md`, `docs/security/audit-checklist.md`.

How to read a status:

- **Verified** — exercised by a test or drill that runs in CI or was run
  against real backing services (Postgres, Temporal, SoftHSM2, a dev EVM
  chain, Bitcoin regtest).
- **Protocol-tested** — implemented to the published protocol and tested
  against fixtures or a mock node. It has never been accepted by a real
  network from this repository.
- **Not done** — nothing exists, or it is a stub that fails closed.

---

## 1. Gates that code cannot close

Every row blocks "production ready". None of them is an engineering task.

| Gate | Status | Why it is outside the code |
|---|---|---|
| Independent cryptographic audit of the threshold-signing layer (tss-lib, `mpc-party`, `mpc-signer/tss`) | Not started | Needs reviewers who did not write it. Longest lead time. `docs/security/TSS-LIB-ADVISORY-REVIEW.md` |
| External penetration test | Not started | A third party attacking the live system |
| SOC 2 Type II | Not engaged | Needs a CPA firm observing controls over 6-12 months |
| MPC parties on isolated hosts, applied for real | Code and Terraform exist; never deployed | Needs separate hosts / accounts. On one host this is effectively a single-owner key |
| `terraform apply` against a real AWS account | Never applied | Needs credentials and an account |
| Hardware HSM in the loop | PKCS#11 tested on SoftHSM2 only | Needs a physical HSM or cloud HSM |
| Real network acceptance for Bitcoin, Solana, Cosmos | Not done from this repo | Needs RPC endpoints and funded testnet keys |
| Sanctions/KYC vendor contract | Not contracted | Needs a vendor and an API key |
| Stripe live account | Billing has never charged a card | Needs a Stripe account |
| Money-transmitter / custody licensing, ToS, privacy policy | Not done | Legal work |
| Custody / crime insurance | Not done | Needs an underwriter |

## 2. Services

| Service | Status | Notes |
|---|---|---|
| mpc-signer | Verified (EVM, Bitcoin regtest, PKCS#11 on SoftHSM2) | See section 3 for Solana and Cosmos |
| mpc-party | Verified (live multi-party DKG, signing, resharing over HTTP/mTLS) | Isolation only simulated on one host |
| temporal-worker | Verified | Workflows run against a real Temporal dev server in CI |
| api-gateway | Verified (29 specs, plus live specs on real Postgres) | JWT/role console; API-key API |
| policy-service | Verified | OPA/Rego engine; sanctions list reloadable |
| webhooks | Verified, thin tests | HMAC-signed delivery drilled in `webhook-drill.sh` |
| vault-pki-init | Verified | mTLS leaf issuance |
| compliance | See section 4 | |
| billing | Not done for production | Never charged a card; gateway has a separate billing module |
| backup | See section 4 | |
| vault-unseal | Dev only | Source says "NOT PRODUCTION-GRADE KEY HANDLING" |
| policy, settlement, marketplace | Not part of the launch path | See section 4 |

## 3. Chains

| Chain | Signing | Address | Broadcast | Real-network accepted |
|---|---|---|---|---|
| Ethereum / Polygon | Verified | Verified | Verified (dev chain) | Not from this repo |
| Bitcoin | Verified | Verified | Verified (regtest) | Not from this repo |
| Solana | Protocol-tested | Protocol-tested | Protocol-tested | **No** |
| Cosmos | Protocol-tested | Protocol-tested | Protocol-tested | **No** |

(This table is updated as Solana and Cosmos work lands; see git history.)

## 4. Known gaps in code

Tracked here so they are not rediscovered. Each is fixed or stays listed.

- Sanctions/AML screening in `services/compliance` previously always
  returned an error, so it could never pass. Status below.
- `temporal-worker/db/ceremony_rounds.go` round-data persistence was a stub.
- Only Postgres failover is real. Vault, gateway and Temporal failover are
  not implemented, and `services/backup` had no image or chart.
- `deploy.yaml` built three of the images the platform needs.
- Legacy duplicates remain: `sdks/go`, `sdks/javascript`, `sdks/python`.
- `apps/web`, `apps/admin`, `apps/mobile`, `apps/customer` are unbuilt
  scaffolds that call routes the gateway does not have. The working UI is
  the gateway console (`/console`).

## 5. Minimum bar to call it production ready

All of section 1 closed, section 3 "Real-network accepted" ticked for every
chain being sold, and no item left in section 4.
