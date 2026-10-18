# OpenFireblocks Pilot Evaluation Agreement — Draft

**DRAFT FOR LAWYER REVIEW. NOT LEGAL ADVICE. NOT FOR SIGNATURE AS-IS.**
This is a filled-in starting draft, built from `PILOT-AGREEMENT-OUTLINE.md`, so a lawyer has
text to mark up rather than a blank page. Every bracketed item `[...]` is a decision that
still needs to be made or confirmed by counsel. Governing law, liability caps, data-protection
clauses and enforceability vary by jurisdiction and must be reviewed by a qualified lawyer
before this is sent to any counterparty.

---

**PILOT EVALUATION AGREEMENT**

This Pilot Evaluation Agreement ("**Agreement**") is entered into as of **[Effective Date]**
by and between:

**[Your Company Legal Name]**, a company registered in **[jurisdiction]** with registration
number **[number]**, of **[registered address]** ("**Supplier**"); and

**[Customer Legal Name]**, a company registered in **[jurisdiction]** with registration
number **[number]**, of **[registered address]** ("**Customer**"),

each a "**Party**" and together the "**Parties**".

## Recitals

A. Supplier has developed OpenFireblocks, self-hosted digital-asset custody software
providing threshold key signing, transaction policy, approval workflows and related
compliance tooling (the "**Software**").

B. Customer wishes to evaluate the Software in a time-limited pilot, using test networks
only, to assess its fit for Customer's requirements.

C. The Parties wish to set out the terms of that evaluation in this Agreement.

**The Parties agree as follows.**

## 1. Definitions

1.1 "**Confidential Information**" means non-public information disclosed by one Party to the
other in connection with this Agreement, including the Software's source code, Customer's
business and technical information, and the terms of this Agreement, but excluding
information that is public through no breach of this Agreement, was already known to the
receiving Party without confidentiality obligation, or is independently developed.

1.2 "**Feedback**" means suggestions, defect reports, and findings about the Software that
Customer provides during the Pilot Period.

1.3 "**Pilot Environment**" means Customer's own infrastructure, or a staging environment
Supplier makes available, where the Software is installed for the pilot.

1.4 "**Pilot Period**" means the period beginning on the Start Date and ending **[six (6)]**
months later, unless extended or terminated earlier under this Agreement.

1.5 "**Start Date**" means **[date]**, or the date the Software is first installed in the
Pilot Environment, whichever is later.

1.6 "**Success Criteria**" means the criteria set out in **Schedule A**.

1.7 "**Test Network**" means a blockchain network whose native and other tokens have no
monetary value (for example, a chain's designated "testnet" or "devnet").

## 2. Scope: Test Networks Only

2.1 The Software is licensed under this Agreement for evaluation purposes **on Test Networks
only**.

2.2 Customer shall not, during the Pilot Period: (a) use the Software with any assets of
monetary value; (b) connect the Software to any blockchain main network; or (c) use the
Software for any production, live, or customer-facing purpose.

2.3 Breach of Section 2.2 is a material breach of this Agreement, entitling Supplier to
suspend Customer's access to the Software and/or terminate this Agreement immediately on
written notice. **[Decision: automatic termination vs. suspension-then-cure — confirm with
counsel.]**

2.4 Any use of the Software with real assets or in production requires a separate, signed
written production agreement between the Parties. Nothing in this Agreement commits either
Party to enter such an agreement.

## 3. Licence Grant

3.1 Subject to this Agreement, Supplier grants Customer a limited, non-exclusive,
non-transferable, revocable licence, during the Pilot Period, to: (a) install and operate the
Software in the Pilot Environment solely for evaluation under Section 2; and (b) review the
Software's source code for the purpose of that evaluation.

3.2 The Software is distributed under the Elastic License 2.0. **[Lawyer: confirm how this
pilot licence interacts with, and does not conflict with, the Elastic License 2.0 terms
accompanying the source code; state which governs in the event of conflict.]**

3.3 Third-party and open-source components included with the Software remain subject to their
own licences, listed in the Software's `NOTICE` file. **[Lawyer: confirm pass-through
obligations.]**

3.4 No licence is granted for production use, resale, sublicensing, or provision of the
Software (or any service built on it) to any third party.

## 4. Supplier Obligations

4.1 Supplier will provide reasonable assistance with installation and integration of the
Software in the Pilot Environment, as further described in **Schedule B**.

4.2 Supplier will hold a status call with Customer at least weekly during the Pilot Period
and maintain a shared record of issues raised.

4.3 Supplier will use reasonable efforts to address defects Customer reports during the Pilot
Period and will provide Customer a written findings report and proposed production plan
within **[30]** days of the end of the Pilot Period.

4.4 **Supplier makes no commitment regarding uptime, availability, or service levels during
the Pilot Period.** The Software is provided for evaluation only.

## 5. Customer Obligations

5.1 Customer will designate a project sponsor and a day-to-day champion, and will provide the
personnel and time described in Schedule B.

5.2 Customer will install, configure, and operate the Software within the Pilot Environment,
which Customer controls and secures.

5.3 Customer will use only test data, and will not input, process, or store any production
personal data, real financial account information, or real payment credentials in the Pilot
Environment. **[Decision: if any personal data use is contemplated, this section needs
data-protection terms — see Section 12.]**

5.4 Customer will retain sole control over all cryptographic key material, credentials, and
infrastructure used in the Pilot Environment. Supplier does not hold, access, or control
Customer's keys, funds, or credentials at any time.

5.5 Customer will provide Supplier with good-faith written feedback against the Success
Criteria during and at the end of the Pilot Period.

## 6. Fees

6.1 Customer will pay Supplier a pilot fee of **[amount and currency, e.g. ZAR 450,000 /
approximately USD 25,000]** (the "**Pilot Fee**"), covering Supplier's implementation
assistance and engineering access during the Pilot Period. **[Decision: payment schedule —
e.g. 50% on signature, 50% at pilot midpoint; whether VAT/sales tax is additional; invoicing
and payment terms, e.g. net 30.]**

6.2 Customer is responsible for its own costs of participating in the pilot, including its
infrastructure, personnel time, and any third-party fees it incurs.

6.3 **[Decision: refund terms, if any, on early termination by either Party.]**

## 7. Conversion Option

7.1 If, at the end of the Pilot Period, Customer wishes to proceed to production use, Customer
may, within **[twelve (12)]** months of the end of the Pilot Period, enter a production
licence agreement with Supplier at **fifty percent (50%)** of Supplier's then-current list
price for the first year of that licence (the "**Conversion Option**").

7.2 The Conversion Option does not obligate either Party to enter a production agreement on
any particular terms; it fixes only the first-year production licence fee, and is conditional
on the Parties agreeing all other terms of a production agreement, including the production
prerequisites referenced in Section 14.

7.3 **[Decision: is this option exclusive to Customer for a defined territory or use case, or
non-exclusive? Confirm with counsel and commercial strategy.]**

## 8. Intellectual Property; Feedback

8.1 As between the Parties, Supplier owns all right, title and interest in and to the
Software, including all modifications, improvements and derivative works, subject to the
licence granted in Section 3.

8.2 As between the Parties, Customer owns all right, title and interest in its own systems,
data, and any code it writes to integrate with the Software ("**Customer IP**"), excluding
the Software itself.

8.3 Customer grants Supplier a perpetual, irrevocable, royalty-free licence to use, and
incorporate into the Software, any Feedback Customer provides, without obligation to Customer,
provided that such use does not disclose Customer's Confidential Information.

## 9. Confidentiality

9.1 Each Party will protect the other's Confidential Information with at least the same care
it uses for its own confidential information of similar importance, and not less than
reasonable care, and will use it only to perform this Agreement.

9.2 This Section survives termination of this Agreement for **[three (3)]** years, except for
trade secrets, which are protected for as long as they remain trade secrets.

9.3 The Software's source code is Supplier's Confidential Information, except to the extent
the Elastic License 2.0 expressly permits its use or disclosure.

9.4 Customer may disclose security findings about the Software to Supplier under this
Agreement. **[Decision: responsible-disclosure timeline if Customer finds a vulnerability —
e.g. notify Supplier within X days, no public disclosure before an agreed date.]**

## 10. References and Publicity

10.1 Neither Party will use the other's name, logo, or any reference to this Agreement or the
pilot in any public communication without the other Party's prior written approval of the
specific content.

10.2 Supplier may request, and Customer may in its sole discretion agree to provide, a
reference, a case study, and/or permission to cite Customer's internal review of the Software,
in each case only with Customer's prior written approval of the specific wording to be used.
**[Decision: is any reference right a condition of the Pilot Fee discount, or purely
optional? If conditional, state so explicitly and have counsel review enforceability.]**

## 11. Security

11.1 Customer is solely responsible for the security of the Pilot Environment and any
infrastructure it controls.

11.2 Supplier will not access Customer's Pilot Environment except with Customer's prior
permission and subject to Customer's access controls.

11.3 Each Party will notify the other promptly (and in any event within **[48-72]** hours of
becoming aware) of any security incident reasonably likely to affect the other Party or the
pilot.

## 12. Data Protection

12.1 The Parties intend that no personal data of Customer's own customers or other third
parties will be processed in the Pilot Environment during the Pilot Period.

12.2 **[Lawyer: if any personal data processing is contemplated (e.g. test user accounts with
real names/emails), this section needs full data-processing terms addressing the relevant
law(s) — e.g. POPIA, GDPR, or others depending on Customer's and Supplier's jurisdictions. Do
not rely on this draft for that case.]**

## 13. Warranties and Disclaimers

13.1 Each Party warrants it has full power and authority to enter into this Agreement.

13.2 **THE SOFTWARE IS PROVIDED "AS IS" FOR EVALUATION PURPOSES ONLY, WITHOUT WARRANTY OF ANY
KIND, EXPRESS OR IMPLIED, INCLUDING WITHOUT LIMITATION WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR PURPOSE, SECURITY, ACCURACY, OR NON-INFRINGEMENT.** **[Lawyer: confirm
enforceability of this disclaimer in the relevant jurisdiction(s), and whether any statutory
warranties cannot be excluded, particularly if Customer could be treated as a consumer or
public body under local law.]**

13.3 Customer acknowledges and agrees that, as of the Effective Date: (a) the Software has
**not** been independently audited for security or cryptographic correctness; (b) Supplier
holds no SOC 2, ISO 27001, or comparable certification in respect of the Software; and (c) the
Software is **not** represented as production-ready. Customer's use of the Software under
this Agreement is solely for evaluation on Test Networks in light of this status.

## 14. Production Prerequisites (Informational — See Schedule D)

14.1 The Parties acknowledge that, before any production or real-asset use of the Software
could reasonably be considered, the matters listed in **Schedule D** would need to be
addressed. Supplier gives no warranty or commitment as to whether or when any of them will be
completed.

## 15. Limitation of Liability

15.1 **[Lawyer: draft this section specifically for the jurisdiction(s) involved. A starting
position, to be reviewed:]**

(a) Neither Party's liability for breach of confidentiality, infringement of the other
Party's intellectual property rights, or Customer's breach of Section 2 (Test Networks Only),
is subject to the cap in (b).

(b) Subject to (a), each Party's total aggregate liability arising out of or in connection
with this Agreement is limited to the Pilot Fee paid or payable under this Agreement.

(c) Neither Party is liable to the other for any indirect, incidental, special, consequential,
or punitive damages, or for loss of profits, revenue, data, or digital assets, even if advised
of the possibility of such damages.

(d) Nothing in this Agreement excludes or limits either Party's liability for death or
personal injury caused by its negligence, fraud, or any other liability that cannot be
excluded or limited under applicable law.

## 16. Compliance with Laws

16.1 Each Party will comply with applicable law in performing this Agreement, including
sanctions, export control, and anti-bribery and anti-corruption laws.

16.2 Customer represents that it is duly authorised to participate in this pilot and, to the
extent it is a regulated entity, that doing so is consistent with its regulatory obligations.
**[Lawyer: confirm whether Supplier requires any licence, registration, or exemption to
provide and support self-hosted custody software to Customer, given Customer's and Supplier's
respective jurisdictions and regulatory status.]**

16.3 Nothing in this Agreement makes either Party a custodian, fiduciary, or trustee of the
other's assets. Supplier does not hold, control, or have access to Customer's digital assets
or cryptographic key material at any time under this Agreement.

## 17. Term and Termination

17.1 This Agreement begins on the Effective Date and continues until the end of the Pilot
Period unless terminated earlier under this Section or extended by written agreement of the
Parties.

17.2 Either Party may terminate this Agreement for convenience on **[30]** days' written
notice to the other.

17.3 Either Party may terminate this Agreement immediately on written notice if the other
Party commits a material breach (including, for Customer, breach of Section 2) that is not
cured within **[10]** business days of notice, or becomes insolvent.

17.4 On termination or expiry: (a) Customer's licence under Section 3 ends immediately; (b)
Customer will, within **[10]** business days, cease use of the Software and either delete all
copies from the Pilot Environment or, if the Parties are proceeding to a production agreement,
retain it under the terms of that agreement; (c) each Party will return or destroy the other's
Confidential Information on request, except as required by law or for archival/compliance
purposes; (d) the findings report under Section 4.3 (if not already delivered) will still be
delivered.

17.5 Sections 1, 8, 9, 13, 15, 16, and this Section 17.5 survive termination or expiry of this
Agreement.

## 18. General

18.1 **Entire Agreement.** This Agreement, including its Schedules, is the entire agreement
between the Parties regarding its subject matter and supersedes all prior discussions.

18.2 **Variation.** This Agreement may only be amended in writing signed by both Parties.

18.3 **Assignment.** Neither Party may assign this Agreement without the other's prior written
consent, except to a successor in a merger, acquisition, or sale of substantially all assets.

18.4 **Notices.** Notices under this Agreement must be in writing and sent to the addresses
set out above, or such other address as a Party notifies in writing.

18.5 **Severability.** If any provision of this Agreement is held unenforceable, the remaining
provisions continue in full force.

18.6 **Governing Law and Disputes.** This Agreement is governed by the laws of
**[jurisdiction]**, without regard to conflict-of-law principles. Disputes will be resolved
**[in the courts of [jurisdiction] / by arbitration under [rules], seated in [city]]**.
**[Decision for counsel: banks and government counterparties will often require their own
governing law/forum — decide your floor position in advance.]**

---

**Signed for and on behalf of [Supplier]:**

Name: _______________________ Title: _______________________ Date: _____________

**Signed for and on behalf of [Customer]:**

Name: _______________________ Title: _______________________ Date: _____________

---

## Schedule A: Success Criteria

**[Insert the agreed, measurable Success Criteria for this specific Customer, drawn from
`docs/pilot/PILOT-OFFERS-BY-SEGMENT.md` for the relevant segment (asset manager / bank /
fintech / government), negotiated and finalised before signature. Each criterion should have
an owner and, where possible, a target date within the Pilot Period.]**

Example structure:

| # | Criterion | How it will be tested | Owner | Target date |
|---|---|---|---|---|
| 1 | | | | |
| 2 | | | | |

## Schedule B: Support and Effort

**Supplier will provide:**
- [Named contact(s)], reachable via [channel], [working hours / time zone]
- Response target: [e.g. same business day for blocking issues]
- A weekly status call

**Customer will provide:**
- Project sponsor: [name/role]
- Champion: [name/role]
- Engineering time: [estimate, e.g. X person-days over the Pilot Period]
- Compliance/risk reviewer time for Schedule A testing: [estimate]

## Schedule C: Fees and Payment Schedule

| Item | Amount | Due |
|---|---|---|
| Pilot Fee | [amount] | [schedule] |

## Schedule D: Production Prerequisites (Informational Only)

The following would need to be addressed before production or real-asset use is considered.
This list is informational; Supplier commits to no date for any item.

- Independent cryptographic audit completed, with findings addressed
- Independent smart-contract audit of any token contract in use, with findings addressed
- Application penetration test completed, with high/critical findings remediated and retested
- Signing parties deployed on separately controlled infrastructure
- Hardware HSM in place where required by Customer's policy
- Real-network acceptance testing completed for each chain in use
- Customer's own regulatory, legal, security, and procurement approvals obtained
- Any certification Customer requires (e.g. SOC 2 Type II) obtained
