# Compliance mapping for a Sprinto programme: SOC 2, ISO 27001, GDPR, POPIA

As of 11 October 2026. Prepared for the first call with Sprinto (or any compliance-automation
platform) so FORGE CUSTODY knows what is already covered, what needs a policy or a person, and what
needs something outside the code.

**How to read this.** Sprinto does not publish one fixed checklist: it builds controls from the
AICPA Trust Services Criteria (SOC 2), ISO 27001:2022 and the privacy laws in scope, and automates
evidence through integrations (cloud account, code host, identity provider, HR system, an agent on
each laptop). The control *areas* below are the ones those platforms check; Sprinto's own control
IDs and wording will differ and should be reconciled during onboarding. Statuses are a
self-assessment by the people who built the system, not an auditor's view.

| Status | Meaning |
|---|---|
| **Have** | The platform or repo already does it, with evidence a test, script or document shows |
| **Partial** | Exists, but needs records over time, a named owner, or applying in a real environment |
| **Gap** | A policy, process or record a person must create and run (Sprinto templates help) |
| **Outside** | Needs a vendor, hardware, a hire or legal counsel |
| **Unknown** | Depends on how the company's own accounts are set up; Sprinto's integrations will tell us |

**Scope recommendation.** Two things, kept separate: (1) **the company** (FORGE CUSTODY's people,
laptops, Google Workspace, GitHub, the AWS staging account and any hosted service), which is what
SOC 2 and ISO 27001 certify; (2) **the product** customers self-host, whose controls (threshold
signing, approvals, freeze, Travel Rule) are strong *evidence* for many criteria but are operated
by the customer in their own environment. If FORGE hosts the platform for a customer, that hosted
environment comes into scope too.

> **Built since this mapping was written (same day).** The ISMS pack in `docs/compliance/isms/`
> covers the items marked **Gap** for documents: scope, information security policy, risk register,
> Statement of Applicability (93 controls), objectives, internal audit and management review
> templates, 17 policies, record templates, tabletop and DR test scripts, and a privacy pack (RoPA,
> privacy notice, PAIA manual, DPA, DPIA, request procedure, Information Officer registration,
> cross-border transfers, outreach opt-out). All are **drafts awaiting approval**, so the statuses
> below stay as they were until they are approved and records exist. The product now has a built-in
> **access review** (Controls > Access review): signed with step-up, immutable, with a SHA-256 of
> the snapshot. Check scripts for AWS and GitHub are in `scripts/compliance/`.
> `docs/PHASE3-ISO27001-ISMS.md`, which claimed appointments and meetings that had not happened, has
> been replaced with a pointer to the pack.

---

## 1. SOC 2 (Security, plus Availability, Confidentiality, Processing Integrity; Privacy optional)

| Sprinto control area | TSC | FORGE status | Evidence we already have | What still has to happen |
|---|---|---|---|---|
| Governance: org chart, roles, oversight, code of conduct | CC1.1–1.5 | **Gap** | Product enforces separated duties (migration 023) | Org chart, job descriptions with security duties, code of conduct signed, a board/advisor oversight record |
| Information security policies (set of ~15–20) | CC1, CC2, CC5.3 | **Partial** | `docs/security/access-control-policy.md`, `incident-response-plan.md`, `key-rotation.md`, `vendor-risk-assessment.md` | Adopt Sprinto's policy templates, merge ours in, approve and version them, staff acknowledge |
| Internal and external communication | CC2.1–2.3 | **Partial** | Incident and customer-communication plans written | Named owners, a status page, a security contact (security.txt), customer notification commitments in the terms |
| Risk assessment and risk register | CC3.1–3.4 | **Partial** | `threat-model.md`, `TSS-LIB-ADVISORY-REVIEW.md`, known-issues list | A dated risk register (likelihood, impact, owner, treatment), reviewed at least yearly; fraud risk considered |
| Monitoring of controls | CC4.1–4.2 | **Partial** | CI on every change; `scripts/collect-soc2-evidence.sh`; alerts service | Sprinto's continuous monitoring replaces the collector for company systems; run the collector on a schedule for the product |
| Control activities and technology controls | CC5.1–5.3 | **Have** | Route inventory check (156 routes guarded or recorded public), tenant role guard, fail-closed policy | — |
| HR: onboarding, background checks, offboarding | CC1.4, CC6.2, CC6.3 | **Gap** | — | Background checks for anyone with production or key access; joiner/mover/leaver checklist with records; Sprinto HR integration |
| Security awareness training | CC1.4, CC2.2 | **Gap** | — | Annual training through Sprinto, with completion records, from day one of the window |
| Identity and access: SSO, MFA, least privilege | CC6.1 | **Partial** (company) / **Have** (product) | Product: OIDC SSO with PKCE, TOTP step-up, RLS, API-key expiry and rotation, agent keys | Company: MFA enforced on Google Workspace, GitHub, AWS (including root); SSO where possible; no shared accounts — **Unknown** until Sprinto connects |
| Access reviews | CC6.2, CC6.3 | **Partial** | Product: built-in access review per organisation (findings, step-up sign-off, immutable record with snapshot hash); roles revocable on the next request | Quarterly review of Google Workspace, GitHub and AWS with the template in `docs/compliance/isms/records/ACCESS-REVIEW-COMPANY.md`; first product review recorded in staging |
| Cryptography and key management | CC6.1, CC6.7 | **Have** (product) / **Outside** (production) | 2-of-3 threshold keys, sealed shares (Vault or encrypted store), PKCS#11 HSM path, recovery drills, `KEY-RECOVERY.md` | Separate hosts and owners for the three parties; production HSM hardware; company secrets in a manager, not in files |
| Endpoint security (laptops) | CC6.8 | **Gap** | — | Sprinto's agent on every laptop: disk encryption, screen lock, OS updates, anti-malware, password manager |
| Network and boundary protection | CC6.6 | **Partial** | NetworkPolicies, mTLS between services, AWS security group limited to one IP, WAF resources defined | Apply and test NetworkPolicies on a real cluster; document the staging network |
| Encryption in transit and at rest | CC6.7, C1.1 | **Have** / **Unknown** | TLS and mTLS; client-side encrypted off-site backups; sealed key shares | Confirm AWS EBS/S3 encryption at rest — **Unknown** until Sprinto's AWS checks run |
| Malicious software, supply chain | CC6.8, CC7.1 | **Partial** | cosign keyless signing, SBOM, dependency audit gate, `govulncheck` in CI | Signing has never run on a real registry; GitHub secret scanning and Dependabot on |
| Vulnerability management and penetration testing | CC7.1 | **Partial** | Three Shannon (AI) pentest rounds, all findings fixed; dependency gates | **Outside**: an independent human-led pentest (include the MCP server); patch timelines in a policy |
| Logging, detection, alerting | CC7.2 | **Partial** | Alerts on freeze, pending approvals, failed transfers, sweeps, Travel Rule; immutable audit records | Company: AWS CloudTrail on and retained — **Unknown**; on-call and alert routing named |
| Incident response | CC7.3–7.5 | **Partial** | Written plan; freeze stops all signing in one act; lifecycle webhooks | A tabletop exercise with a record, then yearly |
| Change management | CC8.1 | **Partial** | PR + CI, migrations tested on real Postgres, chart validated | GitHub branch protection: required reviews, no direct pushes to main, signed-off releases (settings, not code) |
| Vendor management | CC9.2 | **Partial** | `vendor-risk-assessment.md` | Vendor list in Sprinto (AWS, Google, GitHub, Stripe, Sprinto itself, DeepSeek for Shannon), their SOC reports on file, DPAs signed |
| Business continuity and disaster recovery | CC9.1, A1.2–1.3 | **Partial** | Backups, off-site encrypted backups, recovery drills (restore with parties killed, with and without Vault) | A BCP/DR plan document, a yearly test with a record, recovery objectives (RTO/RPO) stated |
| Availability commitments and capacity | A1.1 | **Gap** / **Outside** | PodDisruptionBudgets, rolling updates | An availability target measured over time (only if FORGE hosts a service) |
| Confidentiality: data classification and retention | C1.1–1.2 | **Partial** | Per-tenant row-level security; secrets by reference only | Data classification policy, retention and secure disposal schedule |
| Processing integrity | PI1.1–1.5 | **Have** | Immutable records for approvals, transfers, whitelist, sweeps, Travel Rule; idempotency; one transfer at a time per key; reconciliation against the chain | — |
| Privacy (optional criterion) | P1–P8 | **Gap** | KYC references only; Travel Rule data kept as evidence | Covered by the GDPR and POPIA work below; add the Privacy criterion only if a customer asks |

---

## 2. ISO 27001:2022

### Management-system clauses (4 to 10) — these are what the certification audit tests first

| Clause | FORGE status | What still has to happen |
|---|---|---|
| 4 Context, interested parties, ISMS scope | **Partial** | A one-page scope statement (company plus any hosted service), interested parties and their requirements |
| 5 Leadership, policy, roles | **Gap** | Information security policy signed by the CEO; an ISMS owner named (a CISO is not required at this size) |
| 6 Risk assessment, risk treatment, **Statement of Applicability**, objectives | **Gap** | Risk register with treatment plan; the SoA listing all 93 Annex A controls, applicable or not and why — Sprinto generates the template |
| 7 Resources, competence, awareness, documented information | **Gap** | Training records, document control (versions, approvals) |
| 8 Operation, risk treatment carried out | **Partial** | The engineering controls exist; needs records that treatment actions were done |
| 9 Monitoring, **internal audit**, **management review** | **Gap** | One internal audit (an independent person, Sprinto can arrange) and one management review with minutes before Stage 2 |
| 10 Nonconformity and continual improvement | **Gap** | A corrective-action log |

### Annex A themes (93 controls)

| Theme | Controls | FORGE status | Already have | Main gaps |
|---|---|---|---|---|
| A.5 Organisational | 37 | **Partial** | Access control, incident response, supplier assessment, threat intelligence (advisory reviews), secure development documents | Policy set approved; asset and information inventory; legal and regulatory register (POPIA, GDPR, FIC Act, FAIS/CASP); cloud-services policy (A.5.23); ICT readiness for continuity (A.5.30) |
| A.6 People | 8 | **Gap** | — | Screening, terms of employment with security duties, training, disciplinary process, NDAs, remote-working rules, event reporting |
| A.7 Physical | 14 | **Partial** | No own data centre: AWS covers facilities (inherited, with AWS's reports) | Clear-desk and screen, equipment and media handling for laptops, home-office rules |
| A.8 Technological | 34 | **Have / Partial** | Strong: authentication (A.8.5), access restriction (A.8.3), cryptography (A.8.24), secure development and testing (A.8.25–8.29), logging (A.8.15), backup (A.8.13), segregation of environments (A.8.31), data masking for secrets (A.8.11), network security including the WAF (A.8.20) | Endpoint devices (A.8.1), web filtering on laptops (A.8.23), privileged access records (A.8.2), configuration management records (A.8.9), monitoring of company systems (A.8.16), capacity (A.8.6), deletion of information (A.8.10) |

---

## 3. GDPR (for EU and UK prospects; UK GDPR is materially the same)

FORGE is mostly a **processor** (for customers' Travel Rule and user data when it hosts the
platform) and a **controller** for its own staff, prospects and website visitors. In the
self-hosted model, the customer is controller *and* processor of its own data; FORGE handles none.

| Requirement | Article | FORGE status | What still has to happen |
|---|---|---|---|
| Lawful basis and records of processing (RoPA) | 6, 30 | **Gap** | A RoPA: outreach contacts (legitimate interest), staff, website, any hosted customer data |
| Privacy notices | 13–14 | **Gap** | Website privacy notice; a notice for the outreach contacts (they were emailed on legitimate interest) |
| Data subject rights (access, erasure, objection) | 15–22 | **Gap** | A procedure and an inbox; note Travel Rule records are retained under AML law, which limits erasure — counsel to confirm wording |
| Data protection by design and by default | 25 | **Have** | Per-tenant RLS, secrets by reference, KYC references only for token holders, minimal personal data in transfers |
| Processor contracts (DPA) | 28 | **Gap** | A DPA template for customers if FORGE hosts; signed DPAs with AWS, Google, Sprinto, GitHub |
| Security of processing | 32 | **Have / Partial** | Encryption, access control, testing (the SOC 2 / ISO work covers this) |
| Breach notification (72 hours) | 33–34 | **Partial** | Incident plan exists | Add the 72-hour regulator and data-subject notification steps |
| DPIA for high-risk processing | 35 | **Gap** | One DPIA for Travel Rule data and agent payments if hosted |
| International transfers | 44–49 | **Gap** | Standard contractual clauses for EU data leaving the EEA (South Africa has no EU adequacy decision) |
| EU representative | 27 | **Outside** | Needed only if FORGE offers services to people in the EU without an EU establishment; counsel to advise |

---

## 4. POPIA (South Africa) — not a framework Sprinto lists, but FORGE's home law

POPIA's eight conditions overlap heavily with GDPR; most GDPR work satisfies it. The items that
are **only** POPIA:

| Requirement | Section | FORGE status | What still has to happen |
|---|---|---|---|
| Information Officer registered with the Information Regulator | 55, 56 | **Gap** | Register the CEO (or a delegate) as Information Officer; deputy optional |
| PAIA manual | PAIA s51 | **Gap** | Publish a PAIA manual on the website |
| Eight conditions (accountability, processing limitation, purpose, further processing, information quality, openness, security safeguards, data subject participation) | 8–25 | **Partial** | Security safeguards (s19) largely **Have**; the rest map to the GDPR rows above |
| Cross-border transfers | 72 | **Gap** | Contract terms for data leaving South Africa (AWS region choice matters) |
| Direct marketing by electronic communication | 69 | **Gap** | POPIA's data subjects include juristic persons, so business addresses are still covered: s69 needs consent from non-customers, who may be approached once to ask. Counsel to confirm before more sends; see `docs/compliance/isms/privacy/OUTREACH-OPT-OUT.md` |
| Security compromise notification | 22 | **Partial** | Add the Information Regulator notification step to the incident plan |

---

## 5. One piece of work, several frameworks

Do these in this order; each clears rows in several tables at once.

| # | Action | Clears | Owner | Rough effort |
|---|---|---|---|---|
| 1 | Sign Sprinto; connect AWS, GitHub, Google Workspace; install the laptop agent | SOC 2 CC6/CC7/CC8 checks, ISO A.8, shows every **Unknown** | CEO | 1–2 days |
| 2 | Adopt and approve the policy set (Sprinto templates + our existing documents) | SOC 2 CC1/CC2/CC5, ISO clause 5 and A.5, GDPR art. 24 | CEO | 1–2 weeks |
| 3 | Risk register + Statement of Applicability | SOC 2 CC3, ISO clause 6 | CEO + engineering | 1 week |
| 4 | Fix what the integrations flag: MFA everywhere, branch protection, CloudTrail, encryption at rest, laptop settings | SOC 2 CC6–CC8, ISO A.8 | Engineering | 1–2 weeks |
| 5 | HR: background checks, onboarding/offboarding records, training, NDAs | SOC 2 CC1.4/CC6.2, ISO A.6 | CEO | 1–2 weeks, then ongoing |
| 6 | Vendor list with SOC reports and DPAs | SOC 2 CC9, ISO A.5.19–5.23, GDPR art. 28, POPIA s72 | CEO | 1 week |
| 7 | Privacy pack: RoPA, website notice, PAIA manual, Information Officer registration, rights procedure | GDPR, POPIA, SOC 2 Privacy | CEO + counsel | 2–3 weeks |
| 8 | Incident tabletop and DR test, both recorded (add 72-hour and Information Regulator steps) | SOC 2 CC7/A1, ISO A.5.24–5.30, GDPR art. 33, POPIA s22 | Engineering | 2 days |
| 9 | Independent human pentest (including the MCP server) | SOC 2 CC7.1, ISO A.8.8, A.8.29 | Outside vendor | 2–4 weeks, booked now |
| 10 | Internal audit + management review | ISO clause 9 (needed before Stage 2) | Sprinto-arranged auditor | 1 week |
| 11 | Start the evidence window; keep access reviews, training and logs running every month | SOC 2 Type II | Everyone | 3+ months |

**What none of this covers:** the threshold-signing cryptography (needs the cryptographic
review), separate hosts and HSM hardware, a crypto-asset service provider licence (FSCA) and
FIC Act obligations. A SOC 2 report or ISO certificate says controls operate as described; it
does not say the signing scheme is sound.
