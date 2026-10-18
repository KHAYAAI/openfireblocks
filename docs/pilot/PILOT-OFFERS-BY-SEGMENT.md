# OpenFireblocks pilot offers by customer type

**Drafts for a lawyer to review before anything is sent. Prices, durations and dates are
proposals, not commitments.** Segment-specific wording is judgement about what each buyer
cares about; check each point against the prospect's own requirements.

All four offers share the same base (see `PILOT-OFFER.md`): self-hosted, testnet only, six
phases, written success criteria, we never hold keys or funds. What differs is the use case,
the controls tested, the people involved, and the honest limits.

## What is true for every segment today

Say this unprompted in every conversation.

- **Testnet only.** No real funds, no mainnet.
- **Not audited.** An independent cryptographic audit is being procured. Say "being
  procured" only once you have contacted firms; say "contracted" only once it is signed.
- **No SOC 2 or ISO 27001 report.**
- **Not production-ready.**
- **Shown so far:** a real 2-of-3 key generation, threshold signature, policy denial and
  tenant-isolation check passing on a Kubernetes cluster (evidence in
  `docs/evidence/2026-10-07-kind-lite-smoke-test.md`). That run deployed the signing path
  only.
- **Never accepted by a live public network** from this project: Bitcoin, Solana, Cosmos.
  The pilot uses test networks.

---

## 1. Asset managers

### The pitch
**Run digital-asset custody with the controls your investment committee and your auditors
expect, in your own environment.**

### Their problem
A fund or asset manager holding digital assets needs defensible control over who can move
assets and who approved it, evidence an auditor will accept, and clean records for valuation
and reconciliation. Hosted custodians hold the keys. Hardware wallets and spreadsheets do not
scale or audit well.

### What the pilot tests
- **Approval workflow that mirrors the committee.** Named approvers, M-of-N quorum, and no
  one approving their own transfer, enforced in the database.
- **Auditor and viewer roles.** Read-only access for administrators, auditors and
  compliance, kept separate from people who can initiate transfers.
- **Policy limits and whitelists** per portfolio or mandate, and an emergency freeze.
- **Reporting and reconciliation.** Per-currency threshold reporting, a transaction history
  showing decoded recipients and amounts, and reconciliation of balances.
- **Stablecoin handling** including rand-pegged and dollar tokens, held to separate limits.
- **Optional: tokenisation.** Issue and control a permissioned token (for example a
  tokenised fund unit) with controlled transfers on a test network. The token contract is
  unaudited.

### Who takes part
Head of operations or COO as sponsor; operations lead as champion; CCO or compliance officer;
two or three approvers; an auditor or fund administrator observer; one engineer.

### Success criteria (examples)
- A transfer above a stated size needs the right number of approvers and cannot be
  self-approved.
- An over-limit or non-whitelisted transfer is denied with a recorded reason.
- Your administrator or auditor can reconstruct "who did what, when, with whose approval"
  from the audit trail alone.
- A reconciliation of balances against the test chain matches.

### Honest limits for this buyer
- **Regulatory custody rules.** Some jurisdictions require a fund's assets to be held by a
  qualified or regulated custodian. Whether self-hosted custody satisfies that for your funds
  is a question for your counsel and regulator, not something we can promise. Raise it in the
  first call.
- Fund-administrator and accounting-system integration is custom work, not shipped.
- No audit or SOC 2 report yet, which many institutional allocators will ask about.

### Suggested terms
Six months, about R450,000 (roughly US$25,000). Optional tokenisation workstream scoped
separately.

---

## 2. Banks

### The pitch
**Evaluate self-hosted digital-asset custody that keeps keys, data and control inside your
own security perimeter, and get a concrete production-readiness plan.**

### Their problem
A bank offering custody or settlement in digital assets faces model-risk, outsourcing and
third-party-risk review, security architecture review, regulatory scrutiny of where keys
live, and integration with its identity and core systems. A vendor holding key shares in its
own cloud is often a blocker.

### What the pilot tests
- **Self-hosted deployment** in the bank's own Kubernetes environment, with internal traffic
  secured by mutual TLS and certificates issued inside the environment.
- **Single sign-on** through the bank's identity provider (OIDC) and role separation across
  admin, operator, approver, auditor, viewer and billing roles.
- **Segregation of duties** enforced in the database, and a full audit trail.
- **Compliance controls:** sanctions screening, Travel Rule data capture (IVMS101, and the
  TRISA link tested against TRISA's reference library, not the live network), whitelists, and
  emergency freeze.
- **Resilience:** removing a signing party and continuing, losing parties and recovering a
  key from backups using the runbook, and rolling the deployment.
- **Architecture and security review** by the bank's team against the source, which is
  available for inspection.

### Who takes part
Head of digital assets or innovation as sponsor; a product owner as champion; platform and
security engineering; information-security and third-party-risk reviewers; compliance and
operations-risk; internal audit as observer.

### Success criteria (examples)
- Installed in the bank's environment by the bank's team with our guidance.
- Sign-in works through the bank's identity provider and roles match the bank's control
  matrix.
- The bank's security team completes an architecture review and records findings.
- A key is recovered by the bank's staff following the runbook within an agreed time.
- A written production-readiness plan lists every gate (audit, hardware HSM, isolated hosts,
  certifications) with owners and dates.

### Honest limits for this buyer
- **No SOC 2 Type II, ISO 27001 or audit report.** Banks will ask first. State the status
  plainly and share the readiness document. Do not give certification dates.
- **Hardware HSM** support is tested against a software HSM only, and signing parties on
  genuinely separate hosts have not been deployed. Both are production prerequisites.
- Core-banking and ledger integration is custom work.
- Expect long vendor-approval timelines. A pilot may start with a technical sandbox before
  procurement completes.

### Suggested terms
Six months, from about R450,000, scoped after the first call. Bank pilots usually need extra
security-review time, so budget for a larger scope than a fintech. This is judgement; set the
price after scoping.

---

## 3. Fintechs and payment firms

### The pitch
**Add controlled digital-asset custody and stablecoin payouts to your platform through an
API, in weeks, with approvals and compliance built in.**

### Their problem
A fintech holding float or settling in stablecoins needs custody it can integrate quickly,
policy and approval controls its regulators and partner banks will accept, and compliance
tooling, without building threshold cryptography itself.

### What the pilot tests
- **API-first integration** through the REST API and the JavaScript, Python and Go SDKs.
- **Stablecoin transfers** under the same policy and approval controls, with decoded
  recipient and amount checked.
- **Webhooks** for every transfer lifecycle event, with retries.
- **Automated callers:** agent accounts with their own budgets, for payout engines and bots.
- **Deposit sweeps** and balance reconciliation.
- **Compliance:** Travel Rule data capture and sanctions screening.
- **Usage billing** in Stripe test mode, if relevant to how they resell.

### Who takes part
A CTO or head of payments as sponsor, a platform engineer as champion, two to four engineers,
and a compliance lead for the control tests.

### Success criteria (examples)
- First transfer on a test network from the customer's own code within a stated number of
  engineer-days. Record the number.
- Over-limit and non-whitelisted payouts are denied and recorded.
- Webhooks arrive for each event, including after an induced failure.
- Their compliance lead signs off the approval and audit trail design.

### Honest limits for this buyer
- Billing has never charged a real card; Stripe is tested against its test mode only (and
  only once that run has been done).
- Solana and Cosmos have never been accepted by a live network from this project.
- No audit or certification. Partner banks may ask for one.

### Suggested terms
Shorter, about 3 to 4 months, from about R300,000 to R450,000. Fintechs decide fast and want
a lighter touch. This is a proposal to test, not a validated price.

---

## 4. Governments and public-sector bodies

### The pitch
**Evaluate digital-asset custody and token operations that stay under your jurisdiction's
control, with source code your technical team can inspect.**

### Their problem
Public bodies considering digital-asset treasury custody, custody of seized or forfeited
assets, regional or national tokens, or public-sector stablecoin pilots face sovereignty,
procurement, audit, and oversight requirements. Keys held in a foreign vendor's cloud are
often unacceptable, and procurement may require evaluation before commitment.

### What the pilot tests
- **Deployment inside the body's own infrastructure and jurisdiction,** with the source
  available for inspection and escrow under the Elastic License 2.0.
- **Multi-person authorisation:** named approvers, quorum rules, and separation of duties,
  suited to treasury and seizure-handling procedures.
- **Audit trail and oversight roles** for an auditor-general or an oversight body.
- **Controlled tokens:** issue a permissioned token with restricted holders and controlled
  transfers on a test network. The token contract is unaudited.
- **Emergency freeze** and policy limits.
- **Recovery:** the runbook, exercised by the body's own staff.

### Who takes part
A senior sponsor in the relevant ministry or agency; a programme manager as champion;
technical staff; legal and procurement; an auditor or oversight representative.

### Success criteria (examples)
- Deployed in an environment the body controls, by its staff.
- The body's technical reviewers complete a source and architecture review.
- Authorisation rules reproduce the body's real procedures, including who can authorise
  what.
- A written findings report and a statement of what would be required for production use.

### Honest limits for this buyer
- **Procurement.** Public tenders, local-content rules, security clearances and export or
  sanctions rules may apply. We do not know which apply to a given body. Take legal advice
  early. Do not assume a pilot can proceed outside the body's procurement rules.
- **No audit, SOC 2 or government security accreditation.** Any accreditation scheme would be
  a separate programme of work.
- **Air-gapped or offline operation has not been tested.** Do not claim it.
- This is a technical evaluation, not a national system.
- Holding or issuing assets on behalf of the public, central-bank digital currency, and
  similar uses are regulated and political. We make no promise that the software fits them.

### Suggested terms
Often funded through a pilot or innovation budget, grant, or development partner. Price after
scoping and after confirming the procurement route. Longer lead time, so start the
conversation early.

---

## Comparison

| | Asset managers | Banks | Fintechs | Governments |
|---|---|---|---|---|
| Main draw | Committee-grade approvals and audit evidence | Keys and data inside the perimeter | Fast API integration | Sovereignty and inspectable source |
| Key questions | Is self-hosted custody acceptable to our regulator and allocators? | Can this pass our security and third-party review? | How fast can we integrate? | Does this fit procurement and oversight rules? |
| Sponsor | COO / head of operations | Head of digital assets | CTO / head of payments | Ministry or agency sponsor |
| Typical length | 6 months | 6 months (plus approval lead time) | 3-4 months | 6 months (plus procurement lead time) |
| Indicative fee | About R450,000 | From R450,000, scoped | About R300,000-R450,000 | Scoped, often grant-funded |
| Biggest gap to production | Regulatory custody status, audit | SOC 2, HSM, isolated hosts, audit | Audit, live-network runs | Procurement, accreditation, audit |

## Before sending any of these

1. Have a lawyer review the offer and the pilot agreement.
2. Check every sentence against today's status. Update the "being procured" wording only when
   it is true.
3. Replace the placeholder prices after your first conversations with real buyers.
4. Share `docs/LAUNCH-READINESS.md` openly. Buyers who find a gap in diligence that you did not
   mention will stop trusting the rest.
