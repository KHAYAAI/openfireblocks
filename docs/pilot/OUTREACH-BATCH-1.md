# Outreach batch 1 (FORGE Custody)

Sender: Tebogo Mvelase, Founder and CEO, FORGE, TKM@myforgepay.com, https://myforgepay.com

Status: drafts only. Nothing here has been sent. The Gmail connector dropped and its
draft calls needed approval, so these live in the repo until the connector is back.
Send 10-15 a day, personalised, and log each in the tracker at the bottom.
Do not attach or link the repository before an NDA is signed.

## A. Vendor shortlist (contact routes found; verify on the firm's site before sending)

| Category | Firm | Route found |
|---|---|---|
| Crypto audit | Trail of Bits | info@trailofbits.com (from their published reports) or trailofbits.com/contact |
| Crypto + contract audit | Zellic | form at zellic.io/contact (no verified intake email) |
| Pentest | Cure53 | form at cure53.de (only a named staff address found; use the form) |
| Crypto audit | NCC Group, Kudelski Security, Least Authority | still to research |
| Contract audit | OpenZeppelin, Spearbit, Cyfrin, ConsenSys Diligence | still to research |
| Pentest | Bishop Fox, Doyensec, Cobalt, Include Security | still to research |
| SOC 2 | Via Vanta / Drata / Secureframe partner CPA firms | still to research |

## B. Emails

### 1. Cryptographic / threshold-signature audit (screening)

Subject: Threshold-signature custody - request for a review quote

Hello [Contact name],

I'm Tebogo Mvelase, founder and CEO of FORGE (myforgepay.com). We build self-hosted MPC
custody infrastructure: 2-of-3 (configurable k-of-n) threshold ECDSA and EdDSA, built on
bnb-chain/tss-lib v2.0.0, with Go services coordinating distributed key generation and
signing across independent parties.

We're looking for an independent review of the signing layer, against a pinned commit, plus
a separate review of a ~140-line permissioned ERC-20 contract (Solidity).

Before sending the full scope: could you name a threshold-signature or MPC implementation
your team has reviewed, and one class of protocol-level finding you made in one? If this is
a good fit, we'll follow up with the detailed scope, an NDA, and repository access. We will
not share the repository before an NDA is signed.

Thank you,
Tebogo Mvelase
Founder and CEO, FORGE | TKM@myforgepay.com

### 2. Penetration test (request for quote)

Subject: Web/API penetration test - digital-asset custody platform, request for quote

Hello [Contact name],

I'm Tebogo Mvelase, founder and CEO of FORGE. We build self-hosted digital-asset custody
software (threshold signing, policy, approvals, compliance tooling). We're looking for an
application penetration test of a staging deployment: a NestJS API gateway (~150 routes), a
single-page console, and server-side integrations such as webhook delivery and custodian
connectors.

Scope, objectives and rules of engagement are documented. Test accounts across roles (admin,
approver, operator, auditor, viewer) and a second cross-tenant organisation will be
provisioned. Test chains only (Sepolia, Solana devnet, Cosmos testnet), never mainnet.

For context: an AI-driven pentest has already run against this deployment and found 11
issues, all now fixed. We want an independent, human-led test and treat that run as a
baseline only.

Could you send: (1) a quote based on our scope, (2) estimated duration and earliest start,
(3) whether retesting of high/critical findings is included, (4) a sample redacted report.

Happy to sign an NDA and hold a scoping call first.

Thank you,
Tebogo Mvelase
Founder and CEO, FORGE | TKM@myforgepay.com

### 3. SOC 2 (initial conversation)

Subject: SOC 2 - self-hosted software vendor, initial conversation

Hello [Contact name],

I'm Tebogo Mvelase, founder and CEO of FORGE, a vendor of self-hosted digital-asset custody
software. Customers run it in their own infrastructure. We're planning a SOC 2 engagement:

1. How do you scope SOC 2 for a self-hosted product, where the service-organisation boundary
   is mostly our build/release pipeline and support processes?
2. Do you recommend a Type I first, before the Type II observation window?
3. Do you require a specific compliance-automation platform, or can we bring our own
   evidence collection?
4. Rough timeline and cost for a first Type I, and typical Type II observation window?

We have a control matrix mapped internally and can share it once we're talking seriously.

Thank you,
Tebogo Mvelase
Founder and CEO, FORGE | TKM@myforgepay.com

### 4. Institutions (banks, asset managers, insurers, fintechs, government)

Subject: Self-hosted MPC custody - invitation to a supervised pilot

Dear [Name],

I'm Tebogo Mvelase, founder and CEO of FORGE (myforgepay.com). FORGE Custody is self-hosted
digital-asset custody software: threshold signing (no single key ever exists), M-of-N
approvals with no self-approval, per-tenant isolation, policy limits, Travel Rule and
sanctions tooling. You run it in your own infrastructure; we never hold your keys or data.

Where it stands, plainly:
- Real key generation and signing have run on a real cloud cluster with public HTTPS.
- An AI-driven penetration test found 11 issues; all are fixed.
- Not yet done: an independent cryptographic audit, an independent human-led pentest, SOC 2,
  and a live public-chain run. We are procuring these now.

Because of that we are offering a bounded, supervised pilot on testnet or capped balances,
not production custody. If [Institution] is exploring digital-asset custody, we'd welcome a
30-minute call, and can send a short proposal and project overview.

Would you be open to that?

Kind regards,
Tebogo Mvelase
Founder and CEO, FORGE | TKM@myforgepay.com | https://myforgepay.com

Segment openers (replace the second sentence):
- Bank: "...for a controlled custody capability with four-eyes approval and audit trails."
- Asset manager: "...for safekeeping of tokenised funds with approval workflows."
- Insurer: "...for custody of reserves or insured digital assets."
- Government: "...for sovereign or public-sector digital-asset safekeeping with full audit."

Attach: a one-page proposal and project overview (to be built from
docs/PLATFORM-OVERVIEW.md and docs/pilot/PILOT-OFFER.md).

## C. Regions and restrictions

South Africa, UK, US, UAE first. Russia: lowest priority, only private non-sanctioned
entities, and only after a sanctions check and legal advice. No sanctioned banks or
government bodies.

## D. Tracker

| Date | Firm/institution | Category | Route | Status |
|---|---|---|---|---|
