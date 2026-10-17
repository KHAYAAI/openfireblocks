# Data processing agreement (operator agreement) — template for counsel

POPIA s20–21 (operator); GDPR art. 28(3) (processor); UK GDPR art. 28. For use when FORGE hosts the
custody platform for a customer. Not needed for customer-operated installations, where FORGE
receives no personal information. Customers that are banks often send their own; compare it with
this one rather than signing blind.

---

**Parties.** [Customer] (the "Responsible Party" / "Controller") and FORGE CUSTODY [legal name]
(the "Operator" / "Processor").

**1. Subject matter and duration.** Hosting and operating the FORGE CUSTODY platform for the
Customer, for the term of the main agreement [reference] and any exit period.

**2. Nature and purpose.** Storage and processing needed to provide custody, approval, policy,
Travel Rule, reconciliation and reporting functions as the Customer configures them.

**3. Data subjects and categories of personal information.**
- Customer's users of the console: name, email, role, sign-in times, authentication factors
  (password hash, TOTP secret), actions taken.
- Parties to transfers, where the Customer records them: IVMS101 Travel Rule data (names,
  addresses, national identifiers, account numbers or wallet addresses).
- No special personal information is expected. The Customer will not load it unless agreed.

**4. Operator's obligations.** The Operator will:
a) process personal information only on the Customer's documented instructions, including on
   transfers outside the country, unless the law requires otherwise (and then tell the Customer
   first, unless the law forbids it);
b) ensure the people authorised to process it are bound by confidentiality;
c) apply the security measures in Annex 1 (POPIA s19; GDPR art. 32);
d) not engage a sub-operator without the Customer's prior general written authorisation; keep the
   list in Annex 2 current; give 30 days' notice of changes so the Customer can object; impose the
   same obligations on sub-operators; remain liable for them;
e) help the Customer respond to data-subject requests, taking into account the nature of the
   processing (the platform's export and role-management functions);
f) help the Customer with security, breach notification, impact assessments and prior
   consultation;
g) **notify the Customer without undue delay, and in any case within [24/48] hours, after becoming
   aware of a personal-information compromise** (POPIA s21(2); GDPR art. 33(2)), with the
   information in clause 4 of Annex 3;
h) at the end of the services, at the Customer's choice, return or delete the personal
   information, except where law requires retention (for example FIC Act records, which remain
   the Customer's responsibility to retain or receive);
i) make available the information needed to show compliance, and allow audits: primarily by
   providing its current SOC 2 report or ISO 27001 certificate and answering questionnaires; on-site
   or direct audits on [30] days' notice, at the Customer's cost, no more than once a year unless
   after a breach or required by a regulator.

**5. Customer's obligations.** The Customer is responsible for the lawfulness of the processing,
for notices to data subjects, and for its regulatory reporting (including Travel Rule and FIC
Act duties). The Customer controls user roles, approvals, retention settings and exports in the
platform.

**6. Transfers.** Personal information is hosted in [AWS region, e.g. af-south-1 (Cape Town)].
Transfers outside South Africa or the EEA are made only as set out in Annex 4
(`CROSS-BORDER-TRANSFERS.md`).

**7. Liability.** As in the main agreement [counsel].

**Annex 1 — Security measures.** 2-of-3 threshold signing with no whole key; M-of-N approvals
with no self-approval and step-up authentication; per-customer row-level isolation in the database;
immutable records of approvals, transfers, whitelist changes, sweeps, Travel Rule and access
reviews; emergency freeze; encryption in transit (TLS, mTLS between services) and at rest
[confirm for the hosting configuration]; daily backups with off-site encrypted copies [once live];
MFA for staff; quarterly access reviews; vulnerability management with fixed deadlines; yearly
penetration testing; incident response plan; ISMS aligned to ISO/IEC 27001 (see `docs/compliance/isms/`).

**Annex 2 — Sub-operators.** Amazon Web Services (hosting; [region]); [others, if any, e.g. an
email provider for notifications].

**Annex 3 — Breach notice content.** Nature of the compromise; categories and approximate numbers
of data subjects and records; likely consequences; measures taken or proposed; contact point.

**Annex 4 — Transfer mechanism.** See `CROSS-BORDER-TRANSFERS.md`.
