# Outreach emails: cryptographic audit, smart-contract audit, penetration test, SOC 2

Ready to copy, personalise the bracketed fields, and send. Send the **screening email first**
to 3-4 firms per category before sending the full scope — it filters out firms that would
quote the wrong kind of review, and keeps the real scope document (with repo access) out of
inboxes that never need it.

**Before sending any of these:** fill in `[Your name]`, `[Your company]`, `[Your email]`. Do
not attach the repository or grant access until a firm has signed an NDA and answered the
screening question well.

---

## 1. Cryptographic / threshold-signature audit

**Who to contact:** firms with named MPC or threshold-signature review experience. Starting
list to research and shortlist (verify current offerings and reputation before contacting —
don't take this list as a recommendation, just a starting point to check):
Trail of Bits, Kudelski Security, NCC Group, Least Authority, Zellic, Cure53.

### Email 1 — screening (send first)

> **Subject: Threshold-signature custody — request for a review quote**
>
> Hi [Firm contact name],
>
> I'm [Your name] at [Your company]. We build self-hosted MPC custody infrastructure:
> 2-of-3 (configurable k-of-n) threshold ECDSA and EdDSA, built on `bnb-chain/tss-lib` v2.0.0,
> with Go services coordinating distributed key generation and signing across independent
> parties.
>
> We're looking for an independent review of the signing layer, against a pinned commit, plus
> a separate review of a ~140-line permissioned ERC-20 contract (Solidity).
>
> Before sending the full scope: could you name a threshold-signature or MPC implementation
> your team has reviewed, and one class of protocol-level finding you made in one? If this is
> a good fit, we'll follow up with the detailed scope, an NDA, and repository access.
>
> Thanks,
> [Your name]
> [Your email]

### Email 2 — full scope (send once they answer the screening question well)

> **Subject: Re: Threshold-signature custody — scope and next steps**
>
> Thanks — that's a strong fit.
>
> Attached / linked:
> - `docs/security/AUDIT-READINESS.md` — the original brief, scope and evidence
> - `docs/assurance/AUDIT-SCOPE-ADDENDUM.md` — everything added since, sized, with the specific
>   question each area raises
>
> We'd like to pin the review to a specific commit on request, and we're open to including a
> re-review window for fixes in the same engagement if that's more cost-effective than a
> separate pass.
>
> Could you send:
> 1. A quote
> 2. An estimated start date and duration
> 3. Whether a re-review window is included or separate
> 4. A sample redacted report, if you can share one
>
> We're also separately looking for a smart-contract auditor for the ERC-20 contract mentioned
> above (`contracts/PermissionedToken.sol`, ~140 lines) — let us know if that's something your
> team does as well, or if you'd recommend a specialist.
>
> Happy to sign an NDA before sharing repository access.
>
> [Your name]

---

## 2. Smart-contract audit (PermissionedToken.sol)

Separate specialism from the cryptographic review — ask the crypto-audit firms if they cover
it (above), and also contact dedicated smart-contract auditors. Starting list to research:
OpenZeppelin, Trail of Bits, Zellic, Spearbit, ConsenSys Diligence, Cyfrin. Verify current
availability and reputation before contacting.

> **Subject: Smart-contract audit — small permissioned ERC-20, request for quote**
>
> Hi [Firm contact name],
>
> We have a single Solidity contract, `PermissionedToken.sol` (~140 lines), that needs an
> independent audit before any production use. It implements a permissioned ERC-20 with
> mint, burn, forced transfer, freeze and pause — issuer-grade powers, since it's meant for
> regulated token issuance.
>
> Specific questions we'd want covered: the forced-transfer and burn powers, two-step
> ownership transfer, `removeHolder` and stranded balances, reentrancy and accounting
> correctness, and whether the cap can be bypassed.
>
> Could you send a quote and an estimated turnaround for a contract this size? Happy to share
> the source under NDA.
>
> Thanks,
> [Your name]
> [Your email]

---

## 3. Penetration test

**Prerequisite — do not send yet:** the scope requires a deployed, non-laptop staging
environment (see `docs/assurance/PENTEST-SCOPE.md`). Send once that exists (Part 2 of our
plan). Starting list to research: Trail of Bits, NCC Group, Bishop Fox, Cobalt, Include
Security.

> **Subject: Web/API penetration test — digital-asset custody platform, request for quote**
>
> Hi [Firm contact name],
>
> We're [Your company], building self-hosted digital-asset custody software (threshold
> signing, policy, approvals, compliance tooling). We're looking for an application
> penetration test against a staging deployment, targeting a NestJS API gateway (~149 routes),
> a single-page console, and server-side integrations (webhook delivery, custodian connectors)
> that are natural SSRF candidates.
>
> Full scope, objectives, and rules of engagement are documented and attached:
> `docs/assurance/PENTEST-SCOPE.md`, plus a generated, CI-checked route inventory
> (`docs/assurance/ROUTE-INVENTORY.md`).
>
> Test accounts across roles (admin, approver, operator, auditor, viewer) and a second
> cross-tenant organisation will be provisioned for you. Test chains only — Sepolia, Solana
> devnet, Cosmos testnet. Never mainnet.
>
> Could you send:
> 1. A quote based on the attached scope
> 2. Estimated duration and earliest start date
> 3. Whether retesting of high/critical findings is included
> 4. A sample redacted report
>
> Happy to sign an NDA and provide a scoping call.
>
> [Your name]
> [Your email]

---

## 4. SOC 2 (CPA firm)

Speak to 2-3 firms licensed to issue SOC 2 reports. Many bundle a compliance-automation
platform (Vanta, Drata, Secureframe, etc.) — ask whether they require one or you can use your
own evidence collection (`scripts/collect-soc2-evidence.sh` already exists for this).

> **Subject: SOC 2 — self-hosted software vendor, initial conversation**
>
> Hi [Firm contact name],
>
> We're [Your company], a vendor of self-hosted digital-asset custody software — customers run
> it in their own infrastructure; we never hold their keys or data. We're starting to plan a
> SOC 2 engagement and wanted to ask a few questions before committing to a platform or firm:
>
> 1. How do you typically scope SOC 2 for a self-hosted product, where the "service
>    organisation" boundary is mostly our build/release pipeline and support processes rather
>    than a hosted environment we operate?
> 2. Do you recommend a Type I first, before the Type II observation window?
> 3. Do you require a specific compliance-automation platform, or can we bring our own
>    evidence collection?
> 4. Rough timeline and cost range for a first Type I, and typical Type II observation window
>    length.
>
> We have a control matrix already mapped internally (`docs/assurance/SOC2-CONTROL-MATRIX.md`)
> and can share it once we're talking seriously.
>
> Thanks,
> [Your name]
> [Your email]

---

## Tracking

Keep a simple log as replies come in: firm, category, date contacted, screening answer
quality, quote, start date, decision. The first good answer to the screening question in each
category is usually worth prioritising over the cheapest quote — a wrong-fit firm on the
cryptographic review is a worse outcome than a slower one.

## After you engage someone

Update `docs/assurance/PROCUREMENT-PLAN.md` and `docs/LAUNCH-READINESS.md` to say which firm,
what scope, and the real start date — replacing "being procured" with "contracted, starting
[date]" only once it's true, per the rule in `docs/pilot/DESIGN-PARTNER-CONVERSATIONS.md`.
