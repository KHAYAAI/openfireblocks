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
| mpc-signer | Verified (EVM, Bitcoin regtest, PKCS#11 on SoftHSM2); Solana and Cosmos protocol-tested | See section 3 |
| mpc-party | Verified (live multi-party DKG, signing, resharing over HTTP/mTLS) | Isolation only simulated on one host |
| temporal-worker | Verified | Workflows run against a real Temporal dev server in CI |
| api-gateway | Verified (29 specs, plus live specs on real Postgres) | JWT/role console; API-key API |
| policy-service | Verified | OPA/Rego engine; sanctions list reloadable |
| webhooks | Verified, thin tests | HMAC-signed delivery drilled in `webhook-drill.sh` |
| vault-pki-init | Verified | mTLS leaf issuance |
| compliance | Pilot: OFAC address screening works and fails closed; KYC vendor and filings unverified | See section 4 |
| billing | Not done for production | Never charged a card; gateway has a separate billing module |
| backup | Deployable (image, chart, daily schedule, token auth); image not built here | Dumps stay in the same cluster; only Postgres failover is real |
| vault-unseal | Dev only | Source says "NOT PRODUCTION-GRADE KEY HANDLING" |
| policy, settlement, marketplace | Not part of the launch path; off by default in the chart | Nothing calls them |

## 3. Chains

| Chain | Key generation | Address | Spend path | Real-network accepted |
|---|---|---|---|---|
| Ethereum / Polygon | Verified | Verified | Verified (dev chain) | Not from this repo |
| Bitcoin | Verified | Verified | Verified (regtest) | Not from this repo |
| Solana (native SOL) | Verified: real 3-party Ed25519 DKG, address is the base58 key | Verified | Protocol-tested: fake node, signature checked against the fee payer before relay | **No** |
| Cosmos (bank send) | Verified: real 3-party secp256k1 DKG, bech32 address from the group key, threshold signature verifies as secp256k1 | Verified against independent known answers | Protocol-tested: SIGN_MODE_DIRECT decoded and verified the way a node does, low-S enforced, fake LCD | **No** |

What "protocol-tested" does and does not mean here. Nothing in this
environment can reach a public chain, so no real Solana or Cosmos node has
ever accepted a transaction from this code. The Solana wire format is the
published legacy message format; the Cosmos encoding follows the SDK protos'
field numbers but there is no SDK-published wire vector among the tests. The
first thing to do with either chain is point it at devnet / a public testnet,
fund a key, and send a transfer (`SOLANA_RPC_URL`, `COSMOS_LCD_URL`; see the
chart values). Until that has happened, do not sell either as live.

Not offered for Solana and Cosmos: SPL tokens, staking, IBC, CosmWasm, and
reconciliation against the chain (it is EVM-only).

## 4. Known gaps in code

Each is fixed or stays listed.

Fixed in this pass:

- Sanctions screening in `services/compliance` always returned an error.
  It now reads the same synced OFAC list as policy-service, refuses when
  unconfigured, unreadable or stale, and the chart runs the sync for it.
  Still not a vendor integration: it screens addresses against OFAC's list
  only, with no Chainalysis/TRM-style risk scoring.
- Keys for Solana were generated on the wrong curve (the worker sent the chain
  name, mpc-party read a curve field). Fixed and tested over real HTTP parties.
- The worker refused any message that was not 32 bytes, so no Solana
  transaction could be signed.
- The EVM transaction route would sign an Ethereum transaction for a Solana or
  Cosmos key.
- Bitcoin spends reached policy in satoshis, so amount limits and the
  high-value approval rule could never fire. Non-EVM amounts now reach policy
  in 18-decimal units. This is unit-correct but not price-aware: a limit of 10
  is ten whole coins on every chain.
- `ceremony_rounds.go` stubs that returned nil as if they persisted data were
  deleted; nothing called them.
- `deploy.yaml` built three of the thirteen images; it now builds every service
  in `scripts/build-images.sh --list`.
- The unmounted `multi-chain` module (with a hardcoded test API key) and the
  SDK methods that always returned 404 were removed. `policyApi`, `settlement`
  and `marketplace`, which nothing calls, now default to off in the chart.

Fixed in the second pass:

- **Billing never charged a card because it could not.** The Stripe call
  created a payment intent with no payment method, no `confirm` and no
  `off_session`, and nothing collected a card, so every intent sat unpaid. It
  now charges the customer's saved card and fails rather than leaving a
  half-finished intent. It first looks for a payment already taken for the
  invoice, so a changed card cannot cause a double charge, and a customer with
  no saved card is skipped with a reason. Cards are saved through a
  Stripe-hosted page (`/v1/billing/card-session`), not a form we serve, so the
  console's script policy and our PCI scope are unchanged. The return URLs are
  allowlisted. Live tests against Stripe's test mode exist and have not been
  run (no key here).
- The billing service had no authentication on any route, including charging
  and repointing a tenant's Stripe customer. It now needs a bearer token and
  refuses to act without one.
- `services/backup` now has an image, a chart workload with a retained volume
  and a daily schedule, and token auth (it exposed `/restore` and
  `/dr/failover` unauthenticated).
- Reconciliation now covers Solana and Cosmos, including a check for spends the
  platform never signed (Cosmos, one key per organisation).
- Bitcoin spends apply the Travel Rule.
- The deploy pipeline deploys the Helm chart, with one repository per service
  and a chart-wide `imageTag`, instead of updating one ECS service. It has
  never run against a cluster or AWS account.
- The console can create keys and send Bitcoin, Solana and Cosmos transfers.

Still open:

- Backups land on a volume in the same cluster, so losing the cluster loses
  them. Only Postgres failover is real; Vault, gateway and Temporal failover
  are not implemented. The backup image has not been built (no Docker daemon
  where this was written).
- No pipeline step has run against a real cluster or AWS account.
- Billing has not been run against Stripe. The gateway also has its own
  billing module that does not call `services/billing`, and nothing calls the
  billing service over HTTP yet, so card saving has no console button.
- Bitcoin, Solana and Cosmos transfers have no approval step: a transfer that
  policy says needs approval is refused. Only EVM settlements route to
  approvers.
- The console starts no EVM transfers directly: they begin as settlements,
  which need a nonce and gas fields the console does not collect.
- Solana and Cosmos amounts are governed by unit-normalised policy, not by
  price. A deployment that wants USD limits needs a price source.
- Cosmos' "spent without us" check needs exactly one Cosmos key per
  organisation, because the ledger does not record which key signed.
- `go vet` reports a lock copy at `mpc-party/tss_signing.go:257`
  (pre-existing).
- Legacy duplicates remain: `sdks/go`, `sdks/javascript`, `sdks/python`.
- `apps/web`, `apps/admin`, `apps/mobile`, `apps/customer` are unbuilt
  scaffolds that call routes the gateway does not have. The working UI is the
  gateway console (`/console`).

## 5. Minimum bar to call it production ready

All of section 1 closed, section 3 "Real-network accepted" ticked for every
chain being sold, and no item left in section 4.
