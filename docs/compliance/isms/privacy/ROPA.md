# Register of processing activities

GDPR art. 30; POPIA s17 (documentation of processing operations), s18. Owner: Information Officer.
Version 0.1, draft, 11 October 2026. Review: yearly and when a new activity starts.

Responsible party / controller: FORGE CUSTODY [legal name, registration number, address: to complete].
Information Officer: [to complete].

| # | Activity | Data subjects | Personal information | Purpose | Lawful basis (POPIA s11 / GDPR art. 6) | Source | Recipients / operators | Outside SA? | Retention | Security |
|---|---|---|---|---|---|---|---|---|---|---|
| P1 | Business development outreach | Contacts at prospective customers (fintechs, banks, government bodies, asset managers) | Name, job title, business email, organisation, correspondence | Offer pilots and partnerships | Legitimate interest (s11(1)(f) / art. 6(1)(f)); **s69 applies to electronic direct marketing, see `OUTREACH-OPT-OUT.md`** | Public websites, professional networks, the contact themselves | Google (Gmail) | Yes (Google) | Until objection, or 2 years after last contact | Company accounts, MFA |
| P2 | Customer relationship and contracts | Customer staff | Name, title, email, phone, signatures | Perform and manage contracts | Contract (s11(1)(b) / 6(1)(b)); legitimate interest | Customer | Google; Stripe (billing) | Yes | 5 years after contract | Company accounts |
| P3 | Billing | Customer billing contacts | Name, email, billing address; card data held only by Stripe | Invoice and collect | Contract | Customer | Stripe | Yes | Tax law: 5 years | Stripe PCI DSS |
| P4 | Support and incident communication | Customer staff | Name, email, the content of the issue | Support; security notices | Contract; legitimate interest | Customer | Google | Yes | 5 years | Company accounts |
| P5 | Staff and contractors | Employees, contractors | Identity, contact, banking, tax, contract, performance, training records | Employment | Contract; legal obligation (s11(1)(c) / 6(1)(c)) | Staff | Payroll provider [to complete]; Sprinto (training) | [to complete] | Employment + 5 years | Restricted access |
| P6 | Screening | Candidates and staff with privileged access | Identity number, criminal record (**special personal information**, POPIA s26), qualifications, references | Protect customer funds | Consent and legal justification (s27(1)(a)); legitimate interest | Candidate; screening provider | Screening provider (operator) | No, if a South African provider | Decision + 6 months, then pass/fail only | Restricted |
| P7 | Device and account monitoring | Staff | Device posture (encryption, OS version), account activity logs | Security of company systems | Legitimate interest; contract | Devices, SaaS logs | Sprinto; Google; GitHub; AWS | Yes | 1 year | Restricted |
| P8 | Website | Visitors | IP address, pages viewed [analytics: to complete]; contact-form data | Operate the site, answer enquiries | Legitimate interest; consent for non-essential cookies | Visitor | Hosting provider [to complete] | [to complete] | [to complete] | — |
| P9 | Hosted platform: console users (only if FORGE hosts) | Customer staff using the console | Name, email, password hash, TOTP secret, sign-in times, actions (approvals, decisions) | Provide the service; security; audit | FORGE is **operator**; customer's basis | Customer | AWS | Depends on region | Per the customer's instructions (DPA) | Product controls (RLS, MFA, immutable records) |
| P10 | Hosted platform: Travel Rule data (only if FORGE hosts) | Originators and beneficiaries of transfers | IVMS101 natural-person names, addresses, national identifiers, account numbers / wallet addresses | Customer's FIC Act / Travel Rule obligations | FORGE is **operator**; customer's legal obligation | Customer; counterparty VASPs via TRISA | AWS; TRISA counterparties | Yes (counterparty VASPs abroad) | 5 years (FIC Act), per customer | Encryption in transit; RLS; immutable |
| P11 | Pentests and AI tooling | Possibly any data in staging (test data only) | Test data; no real personal information should be present | Security testing, development | Legitimate interest | — | Anthropic; pentest providers; DeepSeek (via Shannon) | Yes | Per vendor | Only test data in staging |

In customer-operated installations FORGE processes none of P9 or P10.
