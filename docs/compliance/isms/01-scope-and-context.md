# 01 Scope and context

ISO/IEC 27001:2022 clauses 4.1 to 4.4. SOC 2 system description (DC 200) inputs.
Version 0.1, draft, 11 October 2026. Owner: ISMS owner. Approver: CEO.

## 1. The organisation

FORGE CUSTODY [legal entity name and registration number: to complete] is based in South
Africa. It builds a digital-asset custody platform (repository name: OpenFireblocks). The
platform has threshold (2-of-3) signing, approvals with separated duties, policy limits,
emergency freeze, address whitelists, sweeps, Travel Rule (IVMS101 and TRISA), reconciliation
against the chain, tokenisation, and an MCP server so AI agents can pay within limits.

Customers are fintechs, banks, government bodies and asset managers. Today the product is
**self-hosted**: the customer runs it in its own environment. FORGE also runs a **staging**
environment on AWS for development and demonstrations. It holds no customer funds and no
customer personal data.

## 2. Scope statement

> The information security management system of FORGE CUSTODY covers the people, devices,
> SaaS accounts and cloud accounts used to design, build, test, release and support the FORGE
> CUSTODY custody software. It also covers the staging environment on AWS. If FORGE hosts the
> platform for a customer, that hosted environment is added to the scope before it holds
> customer assets or data.

**In scope**

| Area | Items |
|---|---|
| People | Everyone with access to source code, release signing, cloud accounts or customer communication [names and count: to complete] |
| Devices | Company and personal laptops used for work [inventory: `records/ASSET-INVENTORY.md`] |
| SaaS | Google Workspace (email, documents), GitHub (code, CI, releases), Sprinto (once signed), WorkOS (SSO for the product, if used in staging), Stripe (billing, test mode) [confirm the list] |
| Cloud | The AWS account(s) for staging (`infrastructure/terraform`, `infrastructure/kind`) |
| Product | The source code, build pipeline, signed container images, SBOMs, and the documentation shipped to customers |
| Processes | Secure development, change and release, vulnerability management, incident response, vendor management, customer support |

**Out of scope, and why**

| Item | Reason |
|---|---|
| Customer-operated installations | The customer operates them. FORGE supplies the software and the documentation (the "complementary user entity controls" in a SOC 2 report). |
| Physical data centres | There are none. AWS provides facilities, and its SOC 2 and ISO 27001 reports cover them (inherited controls). |
| Public blockchains | FORGE does not control them. Their failure modes are covered in the risk register. |

**Interfaces and dependencies.** GitHub Actions runs CI and builds images. AWS hosts staging.
Public RPC endpoints and testnets serve development. Google Workspace handles email.
Customers receive releases and documentation, and contact FORGE by email.

## 3. External and internal issues (4.1)

| Issue | Effect on the ISMS |
|---|---|
| Custody is a high-value target: theft of keys or approval bypass means direct loss of funds | Key management and approvals are the core of the risk register |
| South African regulation: POPIA; FIC Act and FSCA crypto-asset (CASP) licensing for customers who are licensed; Travel Rule (FIC Directive 9) | Legal register below; Travel Rule built in |
| EU and UK prospects bring GDPR, DORA (for EU financial customers, as their ICT supplier) and MiCA | Privacy pack; supplier assurance requests expected |
| Small team: few people hold many roles | Separation of duties in the product is strong; inside the company it depends on a few people, which is accepted and recorded as a risk |
| The repository is public | Changes the confidentiality of source code. It contradicts "source under NDA" in sales material until either changes (risk R-21) |
| AI agents as users of the product (MCP) | New threats (prompt injection leading to payments). Mitigated by agent limits, recipient lists and expiry (risk R-12) |

## 4. Interested parties (4.2)

| Party | Requirements relevant to security |
|---|---|
| Customers (fintechs, banks, government, asset managers) | Assurance reports (SOC 2, ISO 27001), a penetration test summary, secure releases, vulnerability notification, data processing terms if hosted |
| Customers' regulators (SARB/PA, FSCA, FIC; EU NCAs under DORA) | Outsourcing and ICT third-party risk requirements passed down through customers |
| Information Regulator (South Africa) | POPIA compliance, registration of the Information Officer, breach notification (s22) |
| EU/UK supervisory authorities | GDPR compliance where EU/UK personal data is processed |
| Staff and contractors | Clear rules, training, a safe way to report problems |
| Investors and the board | Risk visibility, no material incidents |
| Suppliers (AWS, GitHub, Google) | Their terms of service and shared-responsibility models |

## 5. Legal, regulatory and contractual register

| Requirement | Applies because | Owner | Where handled |
|---|---|---|---|
| POPIA (Act 4 of 2013) and its regulations | FORGE processes personal information in South Africa | Information Officer | `privacy/` |
| PAIA (Act 2 of 2000), s51 manual | Private body in South Africa | Information Officer | `privacy/PAIA-MANUAL.md` |
| GDPR / UK GDPR | EU/UK contacts and any hosted EU data | Information Officer | `privacy/` |
| ECTA (Act 25 of 2002) and POPIA s69 | Electronic direct marketing (outreach) | CEO | `privacy/OUTREACH-OPT-OUT.md` |
| FIC Act and Directive 9 (Travel Rule) | Customers' obligations; the product must support them | Product owner | `docs/deployment/TRAVEL-RULE.md` |
| FAIS / FSCA CASP declaration | Applies to customers who provide crypto-asset services; FORGE as a software vendor is not itself a CASP while it hosts nothing [counsel to confirm] | CEO | Counsel |
| Cybercrimes Act (Act 19 of 2020) | Reporting duties for some cybercrimes (s54 applies to electronic communications service providers and financial institutions) | Incident lead | `docs/security/incident-response-plan.md` |
| Contracts with customers | NDAs, any SLA, security schedules | CEO | [to complete] |
| Open-source licences | Third-party components | Engineering | `docs/LICENSING.md`, SBOM |

## 6. The ISMS and its processes (4.4)

The ISMS runs as a yearly cycle, with quarterly checkpoints:

| When | Activity | Record |
|---|---|---|
| Continuously | CI checks, dependency gates, alerts, product audit records | CI logs, `docs/evidence/` |
| Monthly | Review of Sprinto alerts or the AWS/GitHub check scripts; fix and log | `08-corrective-actions.md` |
| Quarterly | Access review of company systems; risk register check; vulnerability status | `records/ACCESS-REVIEW-COMPANY.md`, `03-risk-register.md` |
| Yearly | Policy review; full risk assessment; training; tabletop; DR test; internal audit; management review; penetration test | The documents named in each |
