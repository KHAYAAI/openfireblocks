# Design-partner conversations: playbook

For the founder to use when speaking to institutions. Nothing here has been tested with real
buyers; treat the scripts as a first draft and edit them after the first conversations.

**Before sending anything:** a lawyer has seen the pilot offer (`PILOT-OFFER.md`,
`PILOT-OFFERS-BY-SEGMENT.md`), and you have checked every claim below against today's status.

## The rules

1. **Say it is a testnet pilot, unprompted, in the first message.**
2. **Do not say:** "production-ready", "audited", "SOC 2", "bank-grade", "regulated", or give
   any date for certification or audit completion.
3. **Say "an independent audit is being procured" only if you have contacted firms.** Say
   "contracted" only when it is signed.
4. **No live demo** until a staging environment is up (step 2 of the plan). Offer the
   recorded console demos and the evidence file instead.
5. **Never ask for, or accept, real funds, keys or credentials** during a pilot.
6. **Don't promise features.** Say what exists, what is tested, and what is not.
7. **Write down every question you cannot answer** and answer it within two working days.

## What you can show today

- The evidence file `docs/evidence/2026-10-07-kind-lite-smoke-test.md`: a real 2-of-3 key
  generation and signature on a Kubernetes cluster (signing path only).
- The recorded console demos in `docs/showcase/` for fintech, bank and government flows.
- `docs/LAUNCH-READINESS.md`: the honest status and gaps.
- The source code, under the Elastic License 2.0, for technical review.

## Who to approach first

Judgement, to be corrected by the first conversations:

1. **Fintechs and payment firms.** They decide fastest and do not need certification to
   start a testnet pilot. Best first design partners.
2. **Asset managers** with a digital-assets desk and an operations lead who owns controls.
3. **Banks,** through a digital-assets or innovation team, expecting a slow start.
4. **Governments,** through an innovation unit, development partner or central-bank
   sandbox, expecting the slowest start.

Look for: a person who owns the custody or controls problem and can fund a small pilot. Avoid:
firms that only want a free trial, or want to use real funds immediately.

## Outreach messages

### Short first message (email or LinkedIn)

> Hi [Name],
>
> I'm [your name] at [company]. We build OpenFireblocks, self-hosted digital-asset custody
> software: threshold (2-of-3) key signing, policy on decoded transactions, and approval
> workflows that run inside your own environment, so we never hold your keys.
>
> We're looking for two or three design partners for a testnet pilot. To be clear about
> where we are: it's pilot-stage, testnet only, and not yet independently audited. We're
> after teams who want to shape the product and test it against their real controls.
>
> Would a 30-minute call be worthwhile? Happy to share a short written summary first.
>
> [Name]

### Fintech or payments

> Subject: Testnet pilot: controlled custody and stablecoin payouts through an API
>
> Hi [Name],
>
> If [Company] holds float or settles in stablecoins, you've probably met the custody
> question: hosted custodians hold your keys, and building it yourself is a big project.
>
> OpenFireblocks is self-hosted custody you integrate through an API and SDKs: 2-of-3
> threshold signing, policy and approvals, webhooks, Travel Rule data capture. We're running
> 3-4 month testnet pilots with a few design partners. Pilot-stage and not yet audited, so
> testnet only.
>
> Open to a 30-minute call to see whether it fits?
>
> [Name]

### Asset manager

> Subject: Testnet pilot: committee-grade approvals and audit evidence for digital assets
>
> Hi [Name],
>
> For a fund holding digital assets, the hard parts tend to be who can move assets, who
> approved it, and evidence your auditors will accept.
>
> OpenFireblocks is self-hosted custody software with named approvers, quorum, enforced
> separation of duties, auditor roles and a full audit trail, running in your environment. We
> are running six-month testnet pilots with a few design partners. It is pilot-stage and not
> yet audited. One open question we would want to explore with you is whether self-hosted
> custody can meet your fund's custody requirements, which depends on your regulator.
>
> Would a 30-minute call be useful?
>
> [Name]

### Bank

> Subject: Evaluating self-hosted digital-asset custody inside your perimeter
>
> Hi [Name],
>
> Many banks exploring digital-asset custody are blocked by one point: keys held in a
> vendor's cloud. OpenFireblocks is self-hosted, so keys and data stay inside your security
> perimeter, with threshold signing, single sign-on, segregation of duties and a source-
> available codebase your team can inspect.
>
> We are running six-month testnet pilots with a few design partners. We are pilot-stage:
> no SOC 2 or independent audit report yet, and I would rather say that now than have your
> team find it. The pilot ends with a written production-readiness plan.
>
> Could we have a 30-minute call with you or a colleague who owns this?
>
> [Name]

### Government or public body

> Subject: Technical evaluation of sovereign, inspectable digital-asset custody
>
> Dear [Name],
>
> We build OpenFireblocks, self-hosted digital-asset custody software that can run entirely
> within your own infrastructure and jurisdiction, with source code your technical team can
> inspect, multi-person authorisation and an audit trail for oversight.
>
> We are offering technical evaluations on test networks only, with no real assets. The
> software is not yet independently audited or accredited, and we recognise that any
> engagement must follow your procurement rules, which we would like to understand first.
>
> Could we arrange a short introductory call, or would you prefer a written summary?
>
> [Name]

## First call: 30-45 minutes

**Goal:** decide whether a pilot is a fit. Not to sell production.

1. **Open (3 min).** Who you are. State up front: testnet pilot, not audited, not
   production. Ask if that is acceptable before going further.
2. **Their situation (10-15 min).** Questions below.
3. **What we do (5-10 min).** Only the parts that match what they said. Use the segment
   offer. Offer the evidence file instead of a demo.
4. **Fit and gaps (5 min).** Name the gaps for their segment honestly.
5. **Next step (5 min).** A written scoping document, a technical session with their
   engineers, and who else must be involved.

### Discovery questions (all segments)
- What are you trying to do with digital assets, and what is the timeline?
- How do you hold or move them today? What goes wrong?
- Who owns this: operations, technology, risk, compliance? Who signs off spend?
- What controls would you need to see: approvals, limits, audit trail, access roles?
- Which chains and tokens?
- What would a successful pilot look like in your words?
- What would stop this at your organisation (security review, procurement, regulator)?
- Is there budget for a pilot, and from where?
- Who else needs to be in the room?

### Extra questions by segment
- **Asset managers:** Who is your custodian today? Do your custody rules allow
  self-hosted keys? Who is your administrator and auditor, and what evidence do they need?
- **Banks:** What is your vendor-approval process and how long does it take? Which identity
  provider and cloud platform? Do you require an HSM or a certification before a technical
  sandbox?
- **Fintechs:** Which chains and stablecoins? What does your partner bank require? How many
  engineer-days could you give?
- **Governments:** Which procurement route would a pilot follow? Where must data and keys
  reside? Is there a funding programme or partner?

## Qualification scorecard

Score each 0-2 after the first call. Proceed to a scoping document at 10 or more. Total
10-14 is a good fit.

| Criterion | 0 | 1 | 2 |
|---|---|---|---|
| Real, owned problem | Curious only | A project exists but unowned | A named owner with a deadline |
| Budget | None | Possible | Identified and approved route |
| Champion | None | Interested contact | Will drive it internally |
| Accepts testnet-only | Wants real funds | Reluctant | Clear and comfortable |
| Engineering time | None | Maybe | Named people |
| Decision path | Unknown | Long or unclear | Clear and short |
| Fit with what is built | Needs missing features | Partial | Matches |

**Stop if:** they need real funds or certification now, will not accept testnet-only, or have
no budget or champion. Say so politely and keep in touch.

## Objections and honest answers

| They say | A truthful answer |
|---|---|
| "Are you audited?" | "No. An independent cryptographic audit is [being procured / contracted for date]. This pilot is on testnet for exactly that reason." (Use only the true wording.) |
| "Are you SOC 2 compliant?" | "No. We are not certified. Here is our readiness document with what exists and what does not." |
| "Can we use real funds?" | "Not in this pilot. Real funds come after the audit, isolated-host deployment and your own approvals." |
| "Why not Fireblocks or Metaco?" | Don't attack them. "They are mature and I would not claim otherwise. We are self-hosted, so you hold all key material, and you can read the source. That is the trade." (Verify any specific claim before making it.) |
| "What if you go out of business?" | "Your keys and infrastructure are yours. The recovery procedure is documented and your team can run it. You also have the source." |
| "Is it secure?" | "It uses a threshold-signing library, enforced approvals and isolation, with CI tests. It has not been independently audited. That is what the audit is for, and why we are not asking for real funds." |
| "Which chains work?" | "Ethereum and EVM chains and Bitcoin are the most tested. Solana and Cosmos have been tested with fixtures but not accepted by a live network from our project yet." (Update when true.) |
| "Why is it not free?" | "A paid pilot means someone owns it on your side and our engineers can give it proper time. Free pilots tend to stall." |
| "Do you hold our keys?" | "No. The signing parties run in your environment." |
| "Can you do a demo?" | "I can show recorded demos and the test evidence now. A live demo will follow once our staging environment is up." |
| "Do we need a licence for this?" | "That is a question for your counsel and regulator. We give no assurance on it." |

## After the call

Send within 24 hours:

> Thank you for your time today. A short summary:
> - What you told us you need: [..]
> - What the pilot would test: [..]
> - What is true about our status today: testnet only, not independently audited, no
>   certification, not production-ready.
> - Proposed next step: [a technical session with your engineers on [date] / a scoping
>   document by [date]].
> Attached: the pilot offer and our readiness summary.

Then write up what they asked that you could not answer, answer it within two working days,
and update the tracker.

## Tracker (keep a simple sheet)

Columns: organisation, segment, contact and role, source, date of first contact, stage
(contacted, call booked, call held, scoping sent, agreement with lawyer, signed, pilot
running, review, converted, stopped), scorecard total, budget route, blockers, next action
and date, questions I could not answer.

## When a prospect says yes

1. Send the scoping document and the draft agreement (after your lawyer has reviewed).
2. Agree Schedule A success criteria in writing before any installation.
3. Book the technical kickoff.
4. Make sure staging or their own environment is ready, and that the smoke test passes
   there first.
5. Put the weekly call in both diaries.
