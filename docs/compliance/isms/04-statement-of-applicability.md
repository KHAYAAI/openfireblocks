# 04 Statement of Applicability (ISO/IEC 27001:2022 Annex A)

Clause 6.1.3 d). Version 0.1, draft, 11 October 2026. Owner: ISMS owner. **Approver: CEO. Not yet signed.**

All 93 controls of ISO/IEC 27001:2022 Annex A are listed. For each one the table says whether it
applies, why, how it is (or will be) implemented, and its status. "Risk" refers to
`03-risk-register.md`.

**Status:** **Impl** = implemented with evidence. **Part** = partly implemented, or implemented
without records yet. **Plan** = planned, with an owner and date in the risk register or
objectives. **Inh** = inherited from a supplier (AWS) and covered by that supplier's reports.
**N/A** = not applicable, with the reason given.

**Assumption to confirm:** FORGE works remotely and has no office of its own [to complete].
If that is wrong, the physical controls A.7.1 to A.7.6 become applicable to the office.

## A.5 Organisational controls (37)

| Control | Applies | Reason | Implementation / reference | Status |
|---|---|---|---|---|
| 5.1 Policies for information security | Yes | Required by the ISMS | `02-information-security-policy.md`, `policies/` | Part (not yet approved) |
| 5.2 Information security roles and responsibilities | Yes | Roles must be assigned | `02`, section 4 | Plan (holders to be named) |
| 5.3 Segregation of duties | Yes | Funds movement; R-03, R-19 | Product: M-of-N, no self-approval, quorum floor (migration 023). Company: release signing split, compensating controls for R-19 | Part |
| 5.4 Management responsibilities | Yes | Leadership must require compliance | `02`, sections 2 and 6; management review | Plan |
| 5.5 Contact with authorities | Yes | Information Regulator, FIC, SAPS, CSIRT | `docs/security/incident-response-plan.md` §10 (regulatory notification) and §11 (contacts) | Part |
| 5.6 Contact with special interest groups | Yes | Threat information for crypto custody | Advisory feeds (GitHub advisories, Go vuln DB); [membership, e.g. a CSIRT or industry group: to complete] | Part |
| 5.7 Threat intelligence | Yes | Fast-moving threats to custody | `docs/security/TSS-LIB-ADVISORY-REVIEW.md`; dependency gates; review in the monthly cycle | Part |
| 5.8 Information security in project management | Yes | Security in new features | `policies/SECURE-DEVELOPMENT.md` §2 (security considered in every design; threat model updated) | Part |
| 5.9 Inventory of information and other associated assets | Yes | Know what to protect | `records/ASSET-INVENTORY.md` | Plan |
| 5.10 Acceptable use of information and other associated assets | Yes | Staff use of systems | `policies/ACCEPTABLE-USE.md` | Part |
| 5.11 Return of assets | Yes | Leavers | `records/JOINER-MOVER-LEAVER.md` | Plan |
| 5.12 Classification of information | Yes | Key material vs public docs | `policies/ASSET-AND-DATA-CLASSIFICATION.md` | Part |
| 5.13 Labelling of information | Yes | Customers must know what is confidential | Same policy, §3 (labels in documents and file names) | Plan |
| 5.14 Information transfer | Yes | Sharing with customers and vendors | `policies/ASSET-AND-DATA-CLASSIFICATION.md` §4; NDAs; TLS | Part |
| 5.15 Access control | Yes | Core | `policies/ACCESS-CONTROL.md`; `docs/security/access-control-policy.md` (product) | Part |
| 5.16 Identity management | Yes | Unique identities | Product: one account per person, SSO/OIDC; company: no shared accounts | Part |
| 5.17 Authentication information | Yes | Passwords, TOTP secrets, API keys | Product: hashed passwords, TOTP, API-key expiry and rotation; company: password manager, MFA | Part |
| 5.18 Access rights | Yes | Granted, reviewed, removed | Product: access review built in (Controls > Access review, immutable, step-up); company: `records/ACCESS-REVIEW-COMPANY.md` quarterly | Part (product **Impl**) |
| 5.19 Information security in supplier relationships | Yes | Cloud and SaaS dependence | `policies/SUPPLIER-MANAGEMENT.md`; `records/VENDOR-REGISTER.md` | Part |
| 5.20 Addressing information security within supplier agreements | Yes | Contracts must cover security | Vendor register: DPA and security terms column | Plan |
| 5.21 Managing information security in the ICT supply chain | Yes | Open-source dependencies, base images | SBOM, pinned digests, audit gates, cosign (`docs/security/SUPPLY-CHAIN.md`) | Part |
| 5.22 Monitoring, review and change management of supplier services | Yes | Suppliers change | Yearly vendor review in the register | Plan |
| 5.23 Information security for use of cloud services | Yes | AWS, GitHub, Google | `policies/CLOUD-SERVICES.md`; AWS baseline check | Part |
| 5.24 Incident management planning and preparation | Yes | Core | `docs/security/incident-response-plan.md` | Part (owner to be named) |
| 5.25 Assessment and decision on information security events | Yes | Triage | Incident plan §3 severity table | Part |
| 5.26 Response to information security incidents | Yes | Core | Incident plan; emergency freeze stops all signing in one act | Part |
| 5.27 Learning from information security incidents | Yes | Improvement | Incident plan post-incident review; corrective-action log | Part |
| 5.28 Collection of evidence | Yes | Legal action, regulators | Immutable product records; incident plan evidence handling | Part |
| 5.29 Information security during disruption | Yes | Security must hold during outages | `policies/BUSINESS-CONTINUITY.md`; freeze fails closed | Part |
| 5.30 ICT readiness for business continuity | Yes | Recovery of keys and data | Restore and key-recovery drills (`docs/evidence/`); `exercises/DR-TEST.md` | Part |
| 5.31 Legal, statutory, regulatory and contractual requirements | Yes | POPIA, FIC, GDPR | `01-scope-and-context.md` §5 legal register | Part |
| 5.32 Intellectual property rights | Yes | Open-source licences, own IP | `docs/LICENSING.md`; SBOM | Part |
| 5.33 Protection of records | Yes | Audit and transfer records | Product: append-only tables with guard triggers; company: Sprinto/Google retention | Part |
| 5.34 Privacy and protection of PII | Yes | POPIA, GDPR | `policies/PRIVACY.md`, `privacy/` | Plan |
| 5.35 Independent review of information security | Yes | External assurance | Pentests (three AI-driven rounds); planned human pentest, cryptographic review, internal audit | Part |
| 5.36 Compliance with policies, rules and standards | Yes | Checks that policies are followed | Check scripts, Sprinto monitoring, internal audit | Plan |
| 5.37 Documented operating procedures | Yes | Repeatable operations | `docs/runbook.md`, `docs/RUNBOOKS.md`, `docs/deployment/*` | Impl |

## A.6 People controls (8)

| Control | Applies | Reason | Implementation / reference | Status |
|---|---|---|---|---|
| 6.1 Screening | Yes | Staff with key or production access | `policies/HR-SECURITY.md` §2 | Plan |
| 6.2 Terms and conditions of employment | Yes | Security duties in contracts | HR policy §3 | Plan |
| 6.3 Information security awareness, education and training | Yes | All staff | `records/TRAINING-LOG.md`; Sprinto training | Plan |
| 6.4 Disciplinary process | Yes | Enforcement | HR policy §5 | Plan |
| 6.5 Responsibilities after termination or change of employment | Yes | Confidentiality continues | HR policy §4; NDA terms | Plan |
| 6.6 Confidentiality or non-disclosure agreements | Yes | Staff, contractors, prospects | NDA templates [to complete]; customer NDAs | Part |
| 6.7 Remote working | Yes | The team works remotely | `policies/ENDPOINT-AND-REMOTE-WORK.md` | Part |
| 6.8 Information security event reporting | Yes | Everyone must report | Incident plan §4; security contact address | Part |

## A.7 Physical controls (14)

| Control | Applies | Reason | Implementation / reference | Status |
|---|---|---|---|---|
| 7.1 Physical security perimeters | Inherited | No office; servers in AWS data centres | AWS SOC 2 / ISO 27001 reports | Inh |
| 7.2 Physical entry | Inherited | As 7.1 | AWS | Inh |
| 7.3 Securing offices, rooms and facilities | No | No office [confirm] | — | N/A |
| 7.4 Physical security monitoring | Inherited | As 7.1 | AWS | Inh |
| 7.5 Protecting against physical and environmental threats | Inherited | As 7.1 | AWS | Inh |
| 7.6 Working in secure areas | No | No secure areas of our own [confirm] | — | N/A |
| 7.7 Clear desk and clear screen | Yes | Home working, laptops | Endpoint policy §3: screen lock ≤5 min, no key material on paper | Part |
| 7.8 Equipment siting and protection | Yes | Laptops at home | Endpoint policy §3 | Part |
| 7.9 Security of assets off-premises | Yes | All work is off-premises | Endpoint policy (encryption, travel) | Part |
| 7.10 Storage media | Yes | USB drives, HSM tokens, recovery material | Endpoint policy §4; key-recovery procedure | Part |
| 7.11 Supporting utilities | Inherited | Data-centre power and cooling | AWS | Inh |
| 7.12 Cabling security | Inherited | As 7.11 | AWS | Inh |
| 7.13 Equipment maintenance | Yes | Laptops | Endpoint policy (OS updates, supported hardware) | Part |
| 7.14 Secure disposal or re-use of equipment | Yes | Laptops, HSMs | Data retention policy §4 (wipe and record) | Plan |

## A.8 Technological controls (34)

| Control | Applies | Reason | Implementation / reference | Status |
|---|---|---|---|---|
| 8.1 User endpoint devices | Yes | Laptops hold code and credentials; R-06 | Endpoint policy; Sprinto agent | Plan |
| 8.2 Privileged access rights | Yes | Admin roles, AWS, GitHub owners | Product: admin role separate, step-up for decisions; company: privileged accounts listed in access review | Part |
| 8.3 Information access restriction | Yes | Core | Product: tenant role guard on every route, RLS; route inventory checked in CI | Impl |
| 8.4 Access to source code | Yes | R-07, R-21 | GitHub permissions; branch protection; `github-controls-check.sh` | Part (repository is public: R-21) |
| 8.5 Secure authentication | Yes | Core | Product: OIDC/SSO with PKCE, TOTP, JWT revocation, lockout without oracle; company: MFA everywhere | Part (product **Impl**) |
| 8.6 Capacity management | Yes | Hosted service availability | HPA, PDB in the Helm chart; monitoring when hosted | Part |
| 8.7 Protection against malware | Yes | Laptops; containers | Endpoint anti-malware (Sprinto check); read-only non-root containers | Part |
| 8.8 Management of technical vulnerabilities | Yes | Core | `policies/VULNERABILITY-MANAGEMENT.md`; CI gates; pentests | Part |
| 8.9 Configuration management | Yes | Infrastructure as code | Terraform, Helm, kind configs in git; AWS baseline check | Part |
| 8.10 Information deletion | Yes | Retention limits | `policies/DATA-RETENTION.md` | Plan |
| 8.11 Data masking | Yes | Secrets and personal data in logs | Secrets never logged or returned; console shows key prefixes only | Impl |
| 8.12 Data leakage prevention | Yes | Key material, customer data | No key material leaves the signer; secrets by reference; secret scanning planned | Part |
| 8.13 Information backup | Yes | R-10, R-11 | Daily backups (Helm CronJob); off-site client-side encryption built and tested against an S3-compatible stand-in, not yet a real bucket (`docs/deployment/OFFSITE-BACKUPS.md`); restore and key-recovery drills | Part |
| 8.14 Redundancy of information processing facilities | Yes | Availability | 2-of-3 parties (one may fail); multi-AZ when hosted | Part |
| 8.15 Logging | Yes | Core | Audit events, immutable product records, CloudTrail (to verify) | Part |
| 8.16 Monitoring activities | Yes | Detection | Product alerts (freeze, approvals, failed transfers, sweeps, Travel Rule); GuardDuty planned | Part |
| 8.17 Clock synchronization | Yes | Log correlation, TOTP | Hosts use NTP (AWS Time Sync); TOTP depends on it | Inh / Impl |
| 8.18 Use of privileged utility programs | Yes | `kubectl`, `psql` as admin, cloud CLIs | Access control policy §5: named admins, used only for operations, logged | Part |
| 8.19 Installation of software on operational systems | Yes | Production images | Only signed images from CI; read-only root filesystem | Part |
| 8.20 Networks security | Yes | Core | NetworkPolicies; security groups; mTLS | Part |
| 8.21 Security of network services | Yes | TLS, RPC providers | TLS everywhere; RPC providers in vendor register | Part |
| 8.22 Segregation of networks | Yes | Signer isolation | NetworkPolicy limits mpc-signer to gateway and worker (verified on AWS) | Impl (staging) |
| 8.23 Web filtering | Yes | Staff browsing on work laptops | Browser and DNS protection on laptops (endpoint policy §3). Note: the WAF in front of the API is network protection (8.20), not this control | Plan |
| 8.24 Use of cryptography | Yes | Core | `policies/CRYPTOGRAPHY.md`; threshold keys, sealed shares, TLS, PKCS#11 | Impl (product) |
| 8.25 Secure development life cycle | Yes | Core | `policies/SECURE-DEVELOPMENT.md` | Part |
| 8.26 Application security requirements | Yes | Features handle funds | Security requirements in design notes; threat model | Part |
| 8.27 Secure system architecture and engineering principles | Yes | Core | Fail closed, least privilege, defence in depth (`docs/architecture.md`) | Impl |
| 8.28 Secure coding | Yes | Core | Review, linters, typed DTOs with whitelisting validation, no raw HTML in the console | Part |
| 8.29 Security testing in development and acceptance | Yes | Core | Unit, live-DB and end-to-end tests in CI; pentests | Impl |
| 8.30 Outsourced development | Yes | AI coding assistants and any contractors | Same review and CI gates for all changes; vendor register entry for AI tools | Part |
| 8.31 Separation of development, test and production environments | Yes | Core | Local/kind, staging (AWS), customer production are separate; dev-only flags (`RISK_ALLOW_DISABLED`) only in dev profiles | Impl |
| 8.32 Change management | Yes | Core | PR + CI; migrations tested; branch protection (to confirm) | Part |
| 8.33 Test information | Yes | Test data must not be real personal data or real keys | Tests use generated data (example.test addresses, dev chains, testnets) | Impl |
| 8.34 Protection of information systems during audit testing | Yes | Pentests against staging | `docs/assurance/PENTEST-SCOPE.md` rules of engagement | Impl |

## Summary

| Theme | Controls | Applicable | Inherited | Not applicable |
|---|---|---|---|---|
| A.5 Organisational | 37 | 37 | 0 | 0 |
| A.6 People | 8 | 8 | 0 | 0 |
| A.7 Physical | 14 | 6 | 6 | 2 |
| A.8 Technological | 34 | 34 | (8.17 partly) | 0 |
| **Total** | **93** | **85** | **6** | **2** |

## Approval

| Name | Role | Signature / Sprinto acceptance | Date |
|---|---|---|---|
| [to complete] | CEO | | |
