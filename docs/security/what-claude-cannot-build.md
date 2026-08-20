# What Cannot Be Built by an AI Coding Session

This document exists because it's easy for a long series of "fixed X, verified
Y" commits to create an impression that everything blocking launch is a
coding problem. It isn't. This is an explicit, non-exhaustive list of things
that are genuinely outside what any coding session — however thorough — can
produce, organized by why.

---

## 1. Legal and regulatory (cannot be built at all)

- **Money transmitter licensing.** Operating custody infrastructure that
  moves customer funds is regulated activity in essentially every
  jurisdiction. This requires actual legal counsel, actual state-by-state
  (or country-by-country) licensing applications, and in most cases months
  to years of process. No code changes this. **This is realistically the
  single most likely thing to actually block or delay a launch date**,
  ahead of any remaining engineering work.
- **Terms of service, privacy policy, customer agreements.** Need to be
  drafted or reviewed by a lawyer familiar with digital asset custody, not
  generated from a template.
- **Regulatory reporting obligations** (SAR/CTR in the US, equivalents
  elsewhere) — the *code* to generate a report can be built, but the
  determination of what your specific obligations are, in your specific
  jurisdictions, requires compliance/legal expertise.

## 2. Independent verification (cannot be self-certified)

- **SOC 2 Type II audit.** The policy documents in this directory
  (`access-control-policy.md`, `incident-response-plan.md`,
  `vendor-risk-assessment.md`) are necessary inputs, not the audit itself.
  A SOC 2 Type II report requires an independent CPA firm observing your
  controls actually operating, consistently, over a 6–12 month window.
  There is no way to compress this with better code.
- **Penetration testing.** An external firm attacking the live system.
  Code review (what I've been doing) and a penetration test are different
  activities that find different classes of bugs — the code-level fixes in
  this session do not substitute for one.
- **Independent cryptographic audit of the threshold signing
  implementation.** `services/mpc-party` now compiles and its tests pass,
  but per the audit checklist it is explicitly a non-cryptographic
  placeholder (XOR "key combination," summed "partial signatures" — not
  valid threshold-ECDSA math), not the real implementation. The genuine
  tss-lib integration (`services/mpc-signer/tss`) only runs in-process on
  one host so far. Neither is ready to *submit* for a crypto audit yet —
  and even once a real live-multi-party version exists and passes internal
  tests, a system whose entire value proposition is "no single party can
  steal funds" needs review by cryptographers who didn't write the code,
  before real money touches it.
- **Insurance underwriting.** Crime/custody insurance, which institutional
  customers will typically require before onboarding, requires an insurer's
  own risk assessment — informed by, but not replaceable by, the security
  posture documented here.

## 3. Real infrastructure and credentials (blocked on account/access, not code)

- **A real `terraform apply` against a live AWS account.** Everything
  verified this session stopped at `terraform plan` — the graph resolves,
  every reference is valid, but there are no AWS credentials in this
  environment. A real apply will surface things plan cannot: IAM permission
  gaps, service quota limits, region availability for specific instance
  types, actual resource creation ordering issues.
- **A live blockchain testnet/mainnet connection.** `services/settlement`
  now makes real go-ethereum and Bitcoin Core RPC calls instead of
  fabricating responses, but this session has no RPC endpoint (Infura,
  Alchemy, self-hosted node) to actually test against. The code is written
  against real, stable, documented APIs — but "written correctly against a
  documented API" and "verified against a live node" are different claims.
- **A live KYC/sanctions-screening vendor.** `services/compliance` fails
  closed specifically because no such vendor is contracted. Building a real
  integration requires an actual vendor relationship, a real API key, and
  ideally a sandbox environment to test against — none of which exist yet.
- **A live Stripe account.** Same pattern — billing fails closed rather
  than fabricate success, and closing that gap needs a real contracted
  relationship.

## 4. Organizational decisions (not mine to make)

- **Which sanctions-screening vendor, which KYC vendor, which RPC
  provider.** These are procurement and risk decisions with cost, coverage,
  and liability tradeoffs that belong to the business, not to whoever
  happens to be writing the integration code.
- **Risk appetite / go-live threshold.** How many of the ⬜ items in
  `audit-checklist.md` need to become ✅ before launch is a business risk
  decision, not an engineering one. I can tell you honestly what's true
  about the code; I can't tell you what risk your business should accept.
- **On-call rotation, incident response team assignments.** The incident
  response plan defines roles (Incident Commander, Technical Lead, etc.) —
  who actually holds those roles is a staffing decision.

## 5. What this session *did* do, for contrast

To be clear about the boundary: this session fixed real, verified bugs
across authentication, infrastructure-as-code, and eight backend services —
including several that would have caused silent fund-movement failures or
security control bypasses in production. That work was genuinely necessary
and is documented in the corresponding commit messages. The point of this
document isn't to undercut that work — it's to be equally honest about the
much larger set of things that work doesn't and can't cover, so this list
doesn't get quietly forgotten the way "production ready" claims tend to get
made and then not revisited.

---

**If you're deciding what to do next**: the legal/licensing question (§1) is
the one item on this list that, if unresolved, blocks everything else
regardless of code quality. It's worth starting in parallel with, not after,
the remaining engineering work.
