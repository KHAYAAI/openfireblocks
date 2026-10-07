# OpenFireblocks, explained: the business, the technology, and how we run pilots

Status as of 2026-10-07, branch `claude/platform-explanation-h0st5y`.

How to read this. Three kinds of statement appear below and are kept apart:

- **Built and checkable** - there is code, a test, or a workflow in this repository.
- **Judgement** - a market view, a price, a timeline. It can be wrong. Marked as such.
- **Not proven** - nothing in this repository shows it works. Marked as such.

`LAUNCH-CHECKLIST.md` and `docs/LAUNCH-READINESS.md` win if this file disagrees with them.

---

## Part 1 - The business

### 1.1 What it is

OpenFireblocks is **self-hosted digital-asset custody software**. An institution runs it in
its own infrastructure. It provides threshold ("MPC") key custody, transaction policy,
approval workflows, compliance controls, and an audit trail. We license the software and
support it. We never hold the customer's keys or funds.

### 1.2 The problem

An institution that wants to hold or move digital assets today has three options:

1. **Use a hosted custodian.** The custodian holds the key shares. The buyer's risk team asks
   where the keys are, and the answer is "a vendor's cloud, possibly in another jurisdiction".
2. **Build it in-house.** Threshold cryptography, policy, orchestration, audit and recovery is
   a multi-year, specialist project that most institutions cannot staff.
3. **Use a spreadsheet and a few hardware wallets.** This works at tiny scale and fails
   operationally and in audit.

### 1.3 The bet (judgement)

**Self-hosting is the product, not just a deployment option.**

- A hosted custodian cannot honestly offer "you hold everything, we hold nothing", because
  holding shares is its security model.
- The objection that sinks a small vendor in a custody deal is "what happens to our assets if
  you disappear?". A hosted custodian answers with a balance sheet and insurance. A
  self-hosted product answers with a recovery procedure the customer can run without us. The
  smaller and less known the vendor, the stronger that answer is by comparison. That inversion
  is the thesis.
- The target buyer is too large to trust a retail exchange and too small to interest the
  tier-one vendors: a regional bank, a payments processor, a stablecoin issuer, a fintech
  holding float.

This is untested until someone pays for it. See 1.8 for what would prove it wrong.

### 1.4 What we actually sell

Not the cryptography. The threshold-signature library is open and free. We sell the surrounding
system that lets a control function sign off:

- **Policy that reads the transaction.** Limits and rules evaluated on decoded content, not just
  the destination. Deny by default.
- **Approvals with real segregation of duties.** Enforced in the database, not only the UI.
- **Durable orchestration.** A transfer or key ceremony survives a pod dying halfway.
- **An audit trail** that is hard to alter.
- **A console** a compliance officer or approver can use without being an engineer.
- **Recovery procedures that are exercised**, with a drill that shows a key surviving the loss
  of every signing party.

The licence is Elastic 2.0. The customer receives the source, can read it and escrow it, and
cannot resell it as a competing service. The source is the reassurance, not the giveaway.

### 1.5 Who buys, in what order (judgement)

| Stage | Buyer | Why | Needs |
|---|---|---|---|
| 1. Design partners | 2-3 fintechs or payment firms | Feel the custody problem, decide quickly, buy influence | A paid pilot, testnet or capped balances |
| 2. Production licences | Fintechs, payment processors | Have a platform team, buy on capability | Independent crypto audit, separated signing parties, real-network runs |
| 3. Regulated | Banks, stablecoin issuers, governments | Biggest contracts | SOC 2 Type II, audit report, hardware HSM, legal clearance |

We do not chase banks first. A bank's first request is a SOC 2 Type II report, and that is
months to a year away. A long procurement we cannot pass teaches us little and burns runway.

### 1.6 Pricing and revenue (the repo's plan; judgement, unvalidated)

From `docs/COMMERCIAL-MODEL.md`:

- Annual platform licence from **$75,000**, with higher tiers at about $180,000 and from
  $400,000. Overage around $0.10 per signature and $20 per key at the entry tier.
- One-time implementation: about $30,000 standard, $60,000 for high-availability with
  separated signing parties, $90,000 and up for custom integration.
- Support and SLA annually on top. A managed service is a later option.
- Design-partner pilots: roughly **R450,000 (about $25,000) for six months**, priced so that a
  person must defend the spend. A free pilot has no internal champion and dies quietly. In
  return: a named reference, a case study, and a year-one production licence locked at 50% of
  list.
- The plan's year-one picture is about $200k and year two about $780k. These are projections,
  not forecasts based on any signed customer.

### 1.7 What is honest to say today

| Say | Do not say |
|---|---|
| "Threshold signing with approvals and policy, running in your infrastructure" | "Production ready" |
| "Pilots on testnet or capped balances" | "Audited" |
| "Independent audit is being procured" (only once true) | "SOC 2 compliant" or "bank-grade" |
| "We hold none of your keys" | Any date for certification |

Saying "testnet only" unprompted is the fastest way to show a buyer we are not overselling.
Serious buyers test for exactly that.

### 1.8 What would show the thesis is wrong

- Design partners will not pay for a self-hosted deployment. Then the answer is a managed
  service, which is a different and weaker business.
- The cryptographic audit finds a structural problem in how the threshold library is driven.
  The timeline then moves by months.
- Buyers ask for SOC 2 before a pilot, not before production. Then compliance spend is needed
  much earlier than planned.

---

## Part 2 - The technology

### 2.1 System shape

Thirteen services deployed on Kubernetes through one Helm chart. Internal calls use mutual TLS
with certificates issued per pod by Vault PKI. Postgres holds state. Temporal runs durable
workflows. Vault stores sealed key shares and secrets.

| Service | Language | Role |
|---|---|---|
| `api-gateway` | TypeScript (NestJS 11) | The one public entry: REST API, console, auth, approvals, policy calls, transfers, sweeps, custody, tokenisation, Travel Rule |
| `mpc-party` (x3) | Go | Each holds one key share and takes part in distributed key generation and signing |
| `mpc-signer` | Go | Coordinates a ceremony across the parties; builds and signs chain transactions |
| `temporal-worker` | Go | Runs durable workflows such as key provisioning |
| `policy-service` | Go | Evaluates embedded OPA/Rego policies: amount limits, approval rules, sanctions, token limits |
| `compliance` | Go | Sanctions and AML screening, audit and asset reporting |
| `billing` | Go | Plans, usage, Stripe |
| `webhooks` | Go | Delivers transfer-lifecycle events with retries |
| `settlement`, `marketplace`, `policy` | Go | Supporting domain services |
| `backup` | Go | Off-cluster backups |
| `vault-pki-init`, `vault-unseal` | Go | Bootstrap of the certificate authority and Vault unsealing |

### 2.2 Keys and signing

**Threshold signatures.** 2-of-3 by default, using Binance's tss-lib v2.0.0.

- **No full private key ever exists.** Three `mpc-party` processes each hold one share. Any two
  can produce a signature. One alone cannot.
- ECDSA on secp256k1 covers Ethereum, Bitcoin and Cosmos. Ed25519 covers Solana.
- Key creation is a distributed key generation (DKG) ceremony across the parties. Signing is a
  separate ceremony with two parties.
- **Shares are sealed** in Vault and encrypted at rest. Node-to-node messages are authenticated
  with replay protection.
- **Pre-parameters.** The ECDSA protocol needs Paillier keys built from safe primes, an
  expensive randomised search. It runs in a background pool so it stays off the ceremony's
  critical path. Doing it inline made ceremonies fail on real clusters, because peers gave up
  waiting. This was found by running the services as separate pods.
- **Committees.** A signing committee is chosen from the available parties and the same
  committee handling is used for both curves.
- **Verification.** The tests check that signatures verify with the standard library for each
  curve (the Ed25519 one is the code a Solana validator runs), and that a signature recovers to
  the address the DKG derived. A signature can look well-formed and still be rejected by
  every node, which is why recovery and real-node tests matter.
- **Optional HSM mode** uses PKCS#11. It is tested against SoftHSM2 only.

### 2.3 Policy and controls

- **Policy** is OPA/Rego embedded in the policy service: amount limits, approval rules,
  sanctions checks, token limits. It **fails closed**: if it cannot decide, the request is
  denied.
- **Decoded content.** ERC-20 transfers carry the recipient and amount inside calldata, so the
  raw transaction's destination is the token contract and its value is zero. The gateway
  decodes the calldata before policy runs, so limits apply to the real recipient and amount.
  Malformed calldata is handled without throwing.
- **Controls per organisation:** address whitelists with a whitelist mode, an emergency
  freeze and unfreeze, and threshold reporting per currency including rand-pegged stablecoins.
- **Roles** (`roles.ts`): admin, approver, operator, auditor, viewer, billing admin. Whoever can
  start a transfer and whoever can approve one are different people. Admins are in both sets,
  and **the database blocks a person approving a transfer they started** (migration 023). An
  approver cannot start transfers. A billing admin can add a card but cannot see keys or start
  transfers.
- **Approvals:** named approvers, M-of-N quorum, and a pending-transfer record. All signing
  routes, across EVM, Bitcoin, Solana and Cosmos, go through the gate.
- **Sign-in:** API keys, and generic OIDC single sign-on, tested against an independent
  provider in CI.
- **Tenant isolation:** Postgres row-level security with a restricted application role;
  immutability triggers protect audit-relevant tables. CI runs real tenant-isolation tests.

### 2.4 Chains and money movement

| Chain | Scheme | Threshold | Build and broadcast route | Proven by |
|---|---|---|---|---|
| Ethereum / EVM | ECDSA | Yes | `POST /keys/:id/transactions`, `/transfers`, ERC-20 via `/token-transfers` | Real multi-node network drills |
| Bitcoin | ECDSA | Yes | `POST /keys/:id/bitcoin-transactions` | Bitcoin Core regtest drill |
| Solana | Ed25519 | Yes | `POST /keys/:id/solana-transactions` | Protocol tests; **never accepted by a real network** |
| Cosmos | ECDSA | Yes | `POST /keys/:id/cosmos-transactions` | Protocol tests; **never accepted by a real network** |

Further built features, all with CI tests:

- **Deposit sweeps** (consolidation of deposit addresses).
- **Multi-custodian orchestration**: routing transfers across custodians through one approval
  flow. Tested only against a stand-in custodian.
- **Tokenisation**: issuing a permissioned ERC-20 (built with solc), holders, and controlled
  transfers. The contract is **unaudited**.
- **Travel Rule**: IVMS101 data and a TRISA link (protobuf envelopes over mTLS gRPC, with
  AES-256-GCM, HMAC-SHA256 and RSA-OAEP). It was cross-checked against TRISA's Go reference
  library. It has **not** been tested against the live TRISA network.
- **Webhooks** for the transfer lifecycle, **reconciliation**, **agent accounts** with budgets
  for automated callers, and **billing** through Stripe.

### 2.5 Operations and assurance

- **Deployment:** Helm chart with opt-in features (TRISA, custody, metrics token), a
  NetworkPolicy, pod disruption budgets and hardened security contexts. Manifests are checked
  with kubeconform in CI.
- **Recovery:** a drill shows a key surviving the loss of every party. Off-cluster backups and
  a key-recovery runbook exist.
- **Supply chain:** signed images and an SBOM. A vulnerability gate in CI fails on any called
  advisory not recorded with a reason, owner and expiry. Three btcd advisories are accepted
  that way because fixing them needs a threshold-library upgrade.
- **Verification:** `scripts/verify-all.sh` runs everything that runs on one machine. CI covers
  Go modules, the gateway, SDKs, Terraform, Kubernetes manifests, real-process end-to-end
  runs, OIDC, approvals against real Postgres, and the Stripe API shape.
- **Console:** a read-only dashboard and an approval console served from the gateway, with
  create-key and send-transfer screens. It is server-rendered, with nothing extra to deploy.

### 2.6 What is not proven

- A real key generation and threshold signature **on a cluster from this branch**. My first
  install on a laptop got every pod running, then the gateway died during key creation. The
  cause was not diagnosed.
- Any real public network for Bitcoin, Solana or Cosmos from this repo.
- Stripe against a real account. The CI Stripe job skips without a test key.
- The TRISA live network, a real custodian, an HSM, and signing parties on isolated hosts.
- The three audits and certifications: cryptographic audit, penetration test, SOC 2 Type II.

---

## Part 3 - How we run pilots with institutions

### 3.1 Principles

1. **Testnet or a hard cap. No uncapped real funds.** Until the audit and legal work are done,
   a pilot evaluates the product, not production custody.
2. **Written scope and success criteria before anything is installed.** Pilots fail when "see
   how it goes" is the goal.
3. **Paid, with a named internal champion.** A free pilot gets no engineering time from the
   customer.
4. **Honest status shared up front**, including `LAUNCH-READINESS.md`.
5. **We never hold their keys.** They run it. We help.

### 3.2 Prerequisites on our side (do these before the first pilot)

- A **stable staging environment** with a public URL and TLS. The smoke test has never
  completed end to end, so getting it to pass on a cluster with enough CPU is the first job. A
  failed live demo costs more than a delayed one.
- The **Solana devnet** run and the **Stripe test-mode** run, so those claims are real.
- A **pilot agreement** reviewed by a lawyer: scope, testnet-only, no warranty, data handling,
  confidentiality of their feedback, IP in the feedback, exit terms.
- The **audit procured and dated**, so the answer to "what about independent review?" is a
  contract date, not "planned".
- Onboarding material: the client onboarding write-up, SDKs, the API reference, and the demo
  console recordings.

### 3.3 Pilot phases (about 6 months; judgement on duration)

**Phase 0 - Qualify (1-2 weeks).**
Confirm the buyer, the champion, the use case (treasury, payouts, stablecoin settlement,
tokenisation), the chains, and the control requirements. Agree they understand it is a
testnet pilot. Exit here if they need production funds or a certification we cannot provide.

**Phase 1 - Scope and sign (1-2 weeks).**
Write the success criteria (3.4), the architecture plan, the roles who will take part, and the
data they will and will not share. Sign the pilot agreement.

**Phase 2 - Deploy (2-4 weeks).**
Two options:
- *In their environment:* we guide them through the Helm install on their cluster. This is the
  honest test of the self-hosting claim.
- *In ours:* a staging tenant, used only to start fast, with a plan to move.

Verify with the same checks we run: health, the smoke test, and the chain tests against their
testnet. Record what broke. Real installs find real defects, as ours already did.

**Phase 3 - Integrate (4-8 weeks).**
Their engineers connect through the API and SDKs, send test transfers, receive webhooks, and
wire OIDC single sign-on. Their compliance officer and approvers use the console. We configure
their policies, whitelists, approval quorum and roles to mirror their real control structure.

**Phase 4 - Test their controls (2-4 weeks).**
A scripted exercise set (3.5), run by their people with ours observing.

**Phase 5 - Review (1-2 weeks).**
Joint review against the success criteria, a written findings report, a list of gaps, and the
production plan: what must be true (audit, isolated hosts, HSM, real-network runs, legal) and
what it would cost.

### 3.4 Success criteria (agree in writing in phase 1)

Examples, to adapt per institution:

- Keys created by a 2-of-3 ceremony across three parties, with all three shares sealed.
- A transfer above the approval threshold cannot execute without the required approvers, and
  cannot be approved by its initiator.
- A transfer breaching a limit or to a non-whitelisted address is denied with a specific
  reason, and the denial is in the audit log.
- Emergency freeze stops all signing for the organisation within an agreed time.
- Webhooks deliver every lifecycle event, with retries after an induced failure.
- A signing party is removed and signing continues with the other two.
- A key is recovered after losing parties, following the runbook, by their staff.
- Their auditor or risk team can answer "who did what, when, with whose approval" from the
  audit trail alone.
- Integration effort measured in engineer-days, recorded as a number.

### 3.5 Test scenarios to run

| Area | Scenario | Pass looks like |
|---|---|---|
| Custody | Create a key; sign with two parties | Signature verifies and recovers to the derived address |
| Failure | Kill a party pod, then sign | Still signs with the remaining two; kill two and it correctly refuses |
| Approvals | Initiator tries to approve own transfer | Rejected by the database |
| Approvals | Quorum 2-of-3, one approver rejects | Transfer not executed |
| Policy | Over-limit, sanctioned or non-whitelisted destination | Denied with a reason; audit entry written |
| Tokens | ERC-20 transfer to a blocked recipient | Policy sees the decoded recipient and denies |
| Controls | Freeze, attempt a transfer, unfreeze | Blocked, then permitted |
| Isolation | One tenant tries another tenant's key | Refused |
| Recovery | Follow `KEY-RECOVERY.md` from backups | Key usable again; time recorded |
| Compliance | Travel Rule data on a transfer | Data captured and transmitted in test |
| Billing | Usage recorded and invoiced in test mode | Matches the signature and key counts |
| Operations | Roll the deployment during traffic | No lost or duplicated transfers |

### 3.6 Roles on a pilot

| Their side | Our side |
|---|---|
| Executive sponsor, who owns the budget | Pilot lead, who owns scope and the report |
| Champion, who owns day-to-day access and internal escalation | Engineer, who handles deployment and defects |
| Platform engineers, who integrate | Security contact, who answers diligence questions |
| Compliance or risk reviewer, who tests controls | |
| Approvers, who exercise the approval flow | |

### 3.7 Cadence and reporting

Weekly call, shared issue list with severity, and a running findings log. Every defect found is
fixed or listed in the report with its impact. At the end, a written report covers criteria met
and not met, defects, effort, and a production plan.

### 3.8 Exit paths

- **Convert:** the production plan is accepted, with the audit and hosting requirements agreed.
  The year-one licence price is locked per the pilot agreement.
- **Extend:** a second pilot phase for an unmet criterion.
- **Stop:** both sides keep the report. The customer deletes their environment. We keep the
  agreed reference or anonymised learnings only.

### 3.9 Risks specific to pilots

- **Overstatement.** One loose claim about audits or certifications can end a diligence
  process. Use the table in 1.7.
- **A failed first install.** Mitigate with the staging run and a rehearsed install.
- **CPU and sizing.** The key generation is CPU heavy. Document the minimum cluster size, and
  do not run a pilot on a laptop-sized machine.
- **Scope creep** into production custody. The agreement says no, and we repeat it.
- **Legal.** Whether holding or moving client assets needs a licence depends on jurisdiction
  and structure. Take legal advice before any real funds are involved. This document is not
  legal advice.

---

## Part 4 - What has to happen next

| Order | Item | Owner |
|---|---|---|
| 1 | Get the smoke test passing on a cluster with enough CPU; find why the gateway died during key creation | Engineering |
| 2 | Contract the cryptographic audit firm; then the pen-test vendor | Founder |
| 3 | Provide a Solana devnet endpoint and a Stripe test key; run both | Founder, then engineering |
| 4 | Staging environment with public URL and TLS | Engineering |
| 5 | Lawyer review of the pilot agreement and custody-licensing view | Founder |
| 6 | Start the first design-partner conversations | Founder |
| 7 | Isolated-host signing parties, HSM, and SOC 2 observation | Founder and engineering |

Source documents: `docs/LAUNCH-THESIS.md`, `docs/COMMERCIAL-MODEL.md`,
`docs/LAUNCH-READINESS.md`, `LAUNCH-CHECKLIST.md`, `docs/deployment/`, `docs/assurance/`.
