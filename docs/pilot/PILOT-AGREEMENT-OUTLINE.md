# Pilot agreement outline (for a lawyer to mark up)

**This is an outline of issues and proposed positions, not a contract and not legal advice.**
A qualified lawyer in your jurisdiction (and, where relevant, the customer's) must draft and
review the real agreement. Square brackets are decisions for you or the lawyer.

The agreement should be short enough that a customer's champion can get it signed quickly.
Banks and governments will send their own paper; this outline is the position to hold or
trade from.

---

## 1. Parties and background
- [Your legal entity, registered address, jurisdiction] ("Supplier").
- [Customer legal entity] ("Customer").
- Background: Supplier provides OpenFireblocks, self-hosted digital-asset custody software.
  Customer wishes to evaluate it in a time-limited pilot on test networks.

## 2. Definitions
- **Software:** the OpenFireblocks source code and builds supplied for the pilot.
- **Pilot Environment:** Customer's own infrastructure where the Software is installed, plus
  any staging environment Supplier provides.
- **Test Network:** a blockchain network whose tokens have no monetary value.
- **Pilot Period:** [six months] from the start date. [3-4 months for fintech pilots.]
- **Success Criteria:** the written criteria in Schedule A.
- **Feedback:** suggestions, defect reports and findings about the Software.
- **Confidential Information.**

## 3. Scope: testnet only (the most important clause)
- The Software is licensed for evaluation **on Test Networks only**.
- Customer **must not** use it with real funds, on any main network, or for any production
  or customer-facing purpose, during the Pilot Period.
- [Decision: whether the agreement should terminate automatically, or Supplier may suspend
  support, if Customer breaches this.]
- Any move to real funds requires a separate written production agreement, which depends on
  the independent audit and other conditions in Section 14.

## 4. Licence
- Limited, non-exclusive, non-transferable licence to install and run the Software in the
  Pilot Environment for evaluation, and to review the source code.
- The Software is distributed under the Elastic License 2.0: Customer may not provide it to
  third parties as a hosted or managed service, circumvent licence-key functionality, or
  remove notices. [Lawyer: confirm how the evaluation licence and the Elastic License 2.0
  interact, and which governs on conflict.]
- Third-party and open-source components stay under their own licences (see the repository
  `NOTICE` file). [Lawyer: confirm obligations.]

## 5. Supplier obligations
- Reasonable assistance with installation and integration, as set out in Schedule B
  (hours, channels, response targets). [Decision: named contacts, working hours, time zone.]
- A weekly call and a shared issue list.
- Fix or document defects found; deliver a written findings report and production plan at the
  end.
- **No uptime or service-level commitment** during the pilot. State this plainly.

## 6. Customer obligations
- Provide a named champion and executive sponsor, and the engineering and review time in
  Schedule B.
- Install and operate the Software in its own environment, and secure it.
- Keep all keys, credentials and infrastructure under its own control. Supplier does not hold
  Customer's keys, funds or credentials.
- Use test data only. [Decision: whether any personal data may be processed. Default
  position: none.]
- Give honest, timely written feedback against the Success Criteria.

## 7. Fees and payment
- Pilot fee: [about R450,000 / about US$25,000 for six months; fintech pilots lower; bank and
  government pilots scoped]. [Decisions: currency, VAT or other taxes, invoicing schedule,
  payment terms, refund position, late-payment terms.]
- Customer bears its own costs (infrastructure, staff, third-party fees).
- [Decision: whether to require payment up front. Reason to do so: a paid pilot gets an
  internal owner.]

## 8. Conversion option
- If the pilot succeeds, Customer may, within [twelve] months after the pilot ends, take a
  year-one production licence at [50%] of Supplier's then-current list price.
- Production use requires a separate agreement and is **not** promised by this one. Supplier
  makes no commitment about production readiness, certifications or dates.
- [Decision: whether the option is exclusive, and what happens to list-price changes.]

## 9. Intellectual property and feedback
- Supplier keeps all rights in the Software. Customer keeps all rights in its own systems,
  data and configurations.
- Feedback may be used by Supplier freely without obligation, provided it does not disclose
  Customer's Confidential Information.
- [Decision: any joint work or Customer-funded features; default position is Supplier owns
  the Software and Customer owns its integration code.]

## 10. Confidentiality
- Mutual obligations, [3-5 years] after the pilot. Standard exclusions.
- The Software source code is Supplier's Confidential Information, except as the Elastic
  License 2.0 permits.
- Customer's security review findings about the Software may be shared with Supplier. Supplier
  must treat Customer's environment details as confidential.
- [Decision: how security-vulnerability findings are reported and when they may be disclosed.]

## 11. References and publicity
- Supplier may name Customer, use its logo, or publish a case study **only with Customer's
  prior written approval of the specific wording**.
- Supplier may cite Customer's compliance team's review only with approval of the wording.
- [Decision: whether the reference is a condition of the discount.]

## 12. Security and data protection
- Customer hosts the Software; Customer is responsible for its environment's security.
- Supplier will not access Customer's environment except with Customer's permission and
  under Customer's controls.
- Test data only; no production personal data. [Lawyer: if any personal data is processed,
  add data-processing terms and consider the relevant privacy laws of the Customer's
  jurisdiction.]
- Each side notifies the other promptly of any security incident affecting the pilot.
- [Decision: a defined process for Customer security testing of the Software. Whether the
  Customer may run penetration tests on its own installation.]

## 13. Warranties, disclaimers and liability
- The Software is provided **"as is" for evaluation**, with no warranty of fitness,
  security, accuracy, availability or non-infringement. [Lawyer: confirm what can lawfully be
  disclaimed where the Customer is located, especially for consumers or public bodies.]
- Make the status explicit in the agreement itself: the Software has **not** been
  independently audited, has no SOC 2 or ISO 27001 report, and is **not** production-ready.
  This protects both sides and prevents later claims of misrepresentation.
- Cap on Supplier's liability at [the fees paid]. Exclude indirect and consequential loss and
  loss of profit, funds or data. [Lawyer: confirm enforceability and carve-outs for fraud,
  death or personal injury, and anything that cannot be excluded.]
- Customer indemnifies against claims arising from its breach of the testnet-only clause.
  [Decision: whether to include indemnities at all, to keep the agreement short.]

## 14. Production prerequisites (informational schedule)
A schedule, not a promise, listing what must happen before any real-funds use:
- Independent cryptographic audit, with findings addressed.
- Signing parties deployed on separately controlled hosts.
- Hardware HSM where required.
- Real-network acceptance runs for each chain used.
- Customer's own regulatory, legal and security approvals.
- Certifications Customer requires (for example SOC 2 Type II).
State clearly that Supplier gives no date for any of these.

## 15. Compliance with laws
- Each party complies with applicable law, including sanctions, export controls, and
  anti-bribery and anti-corruption law.
- Customer represents that it is authorised to run the pilot and, where it is a regulated
  entity, that doing so is consistent with its regulatory obligations. [Lawyer: whether
  Supplier needs any licence or registration to supply and support custody software in the
  relevant jurisdictions.]
- Neither party is a regulated custodian of the other's assets by virtue of this agreement.
  Supplier does not hold, control or have access to Customer's keys or funds.

## 16. Term and termination
- Starts on signature; ends at the end of the Pilot Period unless extended in writing.
- Either party may terminate on [30] days' notice, or immediately for material breach
  (including breach of the testnet-only clause) or insolvency.
- On end: Customer stops using the Software and deletes its pilot environment (or converts
  to production under a new agreement); each party returns or deletes Confidential
  Information; the findings report stays with both parties.
- Survival: confidentiality, IP, liability, governing law.
- [Decision: refund position on early termination.]

## 17. General
- Entire agreement, variation in writing, assignment, notices, force majeure, severability.
- Governing law: [jurisdiction]. Disputes: [courts or arbitration seat]. [Decision for
  banks and governments: they will usually insist on their own law and forum.]

---

## Schedule A: Success Criteria (agreed in writing at the start)
Taken from the relevant segment offer in `PILOT-OFFERS-BY-SEGMENT.md`. Each criterion should
be measurable and have an owner and a date.

## Schedule B: Support and effort
- Supplier hours and channels; response targets (best-effort, not guaranteed).
- Customer people and hours: sponsor, champion, engineers, compliance, approvers.
- Meeting cadence: a weekly call.

## Schedule C: Fees and payment plan

## Schedule D: Production prerequisites (see Section 14)

---

## Segment-specific points to expect

**Asset managers.** Whether holding assets in self-hosted custody satisfies the fund's
custody rules or an allocator's requirements is the customer's question, but your agreement
should say you give no assurance about it. Expect questions about the fund administrator's
access and audit evidence.

**Banks.** Expect their own paper, with third-party-risk and outsourcing clauses: audit and
inspection rights, business-continuity and exit plans, sub-contractor controls, data
location, incident-notification times, and insurance. Decide in advance which you can accept
for a testnet pilot with no personal data, and which you cannot. Do not agree to
certifications or uptime you cannot meet.

**Fintechs.** They want it short and fast. Offer the standard outline with a shorter term and
a clear conversion clause. Watch for requests to use real customer funds during the pilot.
The answer is no.

**Governments and public bodies.** Expect procurement rules, tender or exemption routes,
anti-corruption and transparency clauses, local-content or in-country requirements, data and
security-classification rules, immunities and different dispute forums, and slower
approvals. Do not start work before the contract is in place under the body's rules.
Funding may come from a grant or development partner with its own conditions.

---

## Questions for your lawyer
1. Does supplying and supporting self-hosted custody software, with no access to customer
   keys or funds, need any licence or registration in your jurisdiction, or in the
   customers' jurisdictions?
2. How should the pilot licence and the Elastic License 2.0 fit together?
3. Which disclaimers and liability caps are enforceable for each customer type and place?
4. Do you need data-processing terms, or can the agreement exclude personal data entirely?
5. What should the clause say about security research and vulnerability disclosure?
6. Can the conversion option and reference rights be structured as drafted?
7. Is the pilot-fee model (an up-front fee, a testnet scope) acceptable under local
   consumer, tax and regulatory rules?
8. For each segment, what must change when the customer insists on its own contract?
