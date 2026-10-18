# 02 Information security policy and roles

ISO/IEC 27001:2022 clauses 5.1 to 5.3 and Annex A 5.1, 5.2, 5.3, 5.4. SOC 2 CC1.1 to CC1.5, CC2.2, CC5.3.
Version 0.1, draft, 11 October 2026. Owner: ISMS owner. **Approver: CEO. Not yet signed.**

## 1. Purpose

FORGE CUSTODY builds software that controls money. A failure of security can mean the loss of
customers' funds. This policy sets the direction for protecting the confidentiality, integrity
and availability of the information FORGE holds, of the software it ships, and of the systems
that build it.

## 2. Commitments

The CEO, for FORGE CUSTODY, commits to:

1. Meet the legal, regulatory and contractual requirements in `01-scope-and-context.md`, section 5.
2. Base security decisions on assessed risk (`03-risk-register.md`), and fund the treatment the risk requires.
3. Set measurable security objectives each year (`05-objectives.md`), and review them.
4. Give everyone in scope the policies, the training and the authority to follow them.
5. Keep improving the ISMS: act on audit findings, incidents and reviews (`08-corrective-actions.md`).

## 3. Principles

- **No single person can move funds.** In the product this is enforced in code: 2-of-3 threshold keys, M-of-N approvals, no self-approval. The company holds itself to the same standard for release signing and production access.
- **Least privilege, by default.** Access is granted for a purpose, reviewed every quarter, and removed when the purpose ends.
- **Fail closed.** If a control cannot decide, the action stops: policy, sanctions, velocity, freeze status.
- **Evidence, not claims.** A control counts only if a record shows it working.
- **Secrets never in code, chat or email.** Use a secrets manager, by reference.

## 4. Roles and responsibilities

FORGE is a small team, so one person may hold several roles. Where roles that should be
separate fall on one person, the risk is recorded (R-19) and compensated as set out there.

| Role | Responsibilities | Holder |
|---|---|---|
| CEO (top management) | Approves policy, scope, risk acceptance and resources; chairs the management review | [to complete] |
| ISMS owner | Runs the ISMS cycle: documents, risk register, SoA, internal audit arrangements, Sprinto | [to complete] (a CISO is not required at this size) |
| Information Officer (POPIA s55) | Privacy compliance, PAIA requests, Regulator contact, data-subject requests | [to complete]; by default the CEO under POPIA |
| Deputy Information Officer | Optional | [to complete or "none"] |
| Incident lead | Leads incident response; decides on freezing and notification | [to complete] |
| Engineering lead | Secure development, change control, vulnerability management, release signing | [to complete] |
| Key custodians (staging) | Hold the three signing parties' credentials in staging; never all three in one person once separate hosts are live | [to complete] |
| Everyone | Follows the policies, completes training, reports incidents and weaknesses to security@[domain] [to complete] | All |

Changes to role holders are recorded in `09-document-control.md` (role register).

## 5. Policies under this policy

| Policy | File |
|---|---|
| Acceptable use | `policies/ACCEPTABLE-USE.md` |
| Access control | `policies/ACCESS-CONTROL.md` (company), with `docs/security/access-control-policy.md` (product) |
| Asset management and data classification | `policies/ASSET-AND-DATA-CLASSIFICATION.md` |
| Data retention and disposal | `policies/DATA-RETENTION.md` |
| Cryptography and key management | `policies/CRYPTOGRAPHY.md`, with `docs/security/key-rotation.md` and `docs/deployment/KEY-RECOVERY.md` |
| Secure development and change management | `policies/SECURE-DEVELOPMENT.md` |
| Vulnerability and patch management | `policies/VULNERABILITY-MANAGEMENT.md` |
| Logging and monitoring | `policies/LOGGING-AND-MONITORING.md` |
| Incident response | `docs/security/incident-response-plan.md` |
| Business continuity and disaster recovery | `policies/BUSINESS-CONTINUITY.md` |
| Supplier (vendor) management | `policies/SUPPLIER-MANAGEMENT.md`, with `docs/security/vendor-risk-assessment.md` |
| Human resources security | `policies/HR-SECURITY.md` |
| Physical, remote working and endpoints | `policies/ENDPOINT-AND-REMOTE-WORK.md` |
| Risk management | `policies/RISK-MANAGEMENT.md` |
| Privacy and data protection | `policies/PRIVACY.md` |
| Code of conduct | `policies/CODE-OF-CONDUCT.md` |
| Cloud services | `policies/CLOUD-SERVICES.md` |

## 6. Compliance and exceptions

A breach of the policies may lead to disciplinary action (`policies/HR-SECURITY.md`, section 5).
An exception must be requested in writing. The ISMS owner records it in the risk register with
an expiry date, and the CEO approves it. No exception may remove the need for two people to
move funds.

## 7. Review

This policy is reviewed every year, and after any significant change to the business, the
scope or the threat picture.

## Approval

| Name | Role | Signature / Sprinto acceptance | Date |
|---|---|---|---|
| [to complete] | CEO | | |
