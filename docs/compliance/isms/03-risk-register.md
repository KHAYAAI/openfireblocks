# 03 Risk methodology and risk register

ISO/IEC 27001:2022 clauses 6.1.2, 6.1.3, 8.2 and 8.3. SOC 2 CC3.1 to CC3.4 (CC3.3 covers fraud), CC9.1.
Version 0.1, draft. **Assessment date: 11 October 2026.** Assessor: prepared from the
repository and test evidence; **to be reviewed and owned by the ISMS owner and accepted by the
CEO** [to complete]. Next full assessment: October 2027, or sooner after a significant change.

## 1. Method

**Assets and threats.** The risks below come from the product threat model
(`docs/security/threat-model.md`), the three external pentest rounds, the TSS library advisory
review, the launch-readiness assessment, and the company's own operations.

**Likelihood (L)**, over the next 12 months:

| Score | Meaning |
|---|---|
| 1 Rare | Not expected; would need several independent failures |
| 2 Unlikely | Could happen; no sign it has |
| 3 Possible | Has happened to similar companies; plausible here |
| 4 Likely | Expected to happen at least once |
| 5 Almost certain | Happening, or will happen without action |

**Impact (I):**

| Score | Meaning |
|---|---|
| 1 Minimal | No customer effect; under R10,000 |
| 2 Minor | Brief inconvenience; under R100,000 |
| 3 Moderate | A customer affected, a reportable event possible; under R1 million |
| 4 Major | Loss of customer funds or data at one customer; regulator involvement |
| 5 Severe | Loss of funds at several customers, or loss of trust that ends the business |

**Risk = L × I.** Levels: 1–4 **Low**, 5–9 **Medium**, 10–15 **High**, 16–25 **Critical**.
**Acceptance criteria:** Low may be accepted by the ISMS owner. Medium may be accepted by the
CEO, with a recorded reason. High and Critical must be treated. Any residual risk above Medium
needs the CEO's signature and a review date.

**Treatment options:** modify (add or improve controls), avoid (stop the activity), share
(insurance, contract), retain (accept). Controls refer to the Annex A numbers in
`04-statement-of-applicability.md`.

## 2. Register

"Inherent" is the risk before the listed controls; "residual" is after the controls that exist
today. "Planned" lists treatment not yet done.

| ID | Risk | Owner | Inh. L×I | Controls in place | Res. L×I | Level | Planned treatment | Due |
|---|---|---|---|---|---|---|---|---|
| R-01 | Theft of signing key material lets an attacker move customer funds | Eng lead | 3×5 | 2-of-3 threshold keys, no whole key ever exists; shares sealed (Vault or encrypted store); PKCS#11 HSM path; no key in logs or API (A.8.24) | 2×5 | **Medium** | Separate hosts and owners per party; production HSMs; independent cryptographic review | Before uncapped production |
| R-02 | A flaw in the threshold-signing library (tss-lib) is exploited | Eng lead | 3×5 | Advisory review with mitigations (`docs/security/TSS-LIB-ADVISORY-REVIEW.md`); pinned versions; CI keygen/sign tests | 2×5 | **Medium** | Independent cryptographic audit (`docs/assurance/PROCUREMENT-PLAN.md`) | Before uncapped production |
| R-03 | Approval bypass: a single person, or a compromised account, moves funds without the quorum | Eng lead | 3×5 | M-of-N approvals with a quorum floor of 2, no self-approval, TOTP or SSO step-up, enforced in the DB (migration 023); fixed pentest findings AUTHZ-02, AUTH-01 | 1×5 | **Low** | Keep the live-DB tests in CI | Ongoing |
| R-04 | Policy bypass: a transfer evades limits or the whitelist (for example via token calldata) | Eng lead | 3×4 | Calldata decoded before policy (MISC-01 fix); fail-closed policy and velocity; whitelist; sanctions screening | 1×4 | **Low** | Pentest round 3 to cover new routes | Next pentest |
| R-05 | Tenant data leaks to another tenant | Eng lead | 3×4 | Postgres row-level security on every tenant table; tenant role guard; CI isolation tests; route inventory | 1×4 | **Low** | — | — |
| R-06 | Compromise of a developer laptop leads to code or credential theft | ISMS owner | 3×4 | [Unknown: depends on laptop settings] | 3×4 | **High** | Sprinto agent: disk encryption, screen lock, updates, anti-malware, password manager (`policies/ENDPOINT-AND-REMOTE-WORK.md`) | Month 1 |
| R-07 | Compromise of a GitHub account or a malicious change merged to the release branch | Eng lead | 3×5 | CI checks; signed images (cosign) and SBOM defined | 3×5 | **High** | Branch protection with required review, MFA enforced, CODEOWNERS, signed releases from a protected branch; `scripts/compliance/github-controls-check.sh` | Month 1 |
| R-08 | Compromise of the AWS account (root or IAM keys) | Eng lead | 3×4 | Security group limited to one IP; WAF resources defined | 3×4 | **High** | Root MFA, no root keys, IAM users with MFA or SSO, CloudTrail, GuardDuty; `scripts/compliance/aws-baseline-check.sh` | Month 1 |
| R-09 | Supply-chain attack through a dependency or base image | Eng lead | 3×4 | Dependency audit gate, `govulncheck`, pinned digests, SBOM, accepted-vulnerability register | 2×4 | **Medium** | Dependabot and secret scanning on; signing on a real registry | Month 2 |
| R-10 | Loss of the database or its backups means loss of approval and transfer records | Eng lead | 2×4 | Daily backups; restore drills; off-site encrypted copy built but not yet run against a real bucket | 2×4 | **Medium** | Turn on off-site backups to a real bucket in another account/region; yearly DR test with record (`exercises/DR-TEST.md`) | Month 2 |
| R-11 | Loss of key shares means funds become unrecoverable | Eng lead | 2×5 | Recovery from 2 of 3 parties drilled with parties killed; `docs/deployment/KEY-RECOVERY.md` | 1×5 | **Low** | Include in DR test | Yearly |
| R-12 | An AI agent is manipulated (prompt injection) into paying an attacker | Product owner | 4×3 | Agent limits per payment and per 24 hours, recipient lists, token lists, expiry, revocation; policy, Travel Rule and 2-of-3 still apply; access review flags agents without recipient lists | 2×3 | **Medium** | Customer guidance: always use recipient lists; pentest of MCP server | Next pentest |
| R-13 | Sanctions or Travel Rule failure: a customer pays a sanctioned party or omits required data | Product owner | 3×4 | Sanctions screening fails closed; Travel Rule required above threshold (422); IVMS101 and TRISA | 2×4 | **Medium** | Keep sanctions lists current; document the customer's responsibilities | Ongoing |
| R-14 | Personal data breach (staff, prospects, any hosted customer data) not handled lawfully | Information Officer | 3×3 | Little personal data held; product stores KYC references only | 2×3 | **Medium** | Privacy pack; 72-hour and s22 steps in the incident plan; Information Officer registered | Month 2 |
| R-15 | Unlawful direct marketing (POPIA s69, ECTA) from outreach emails. POPIA's data subjects include juristic persons, so business addresses are still covered; s69 allows a non-customer to be approached once to ask for consent | CEO | 4×3 | Each email identifies FORGE and offers a way to decline | 3×3 | **Medium** | Counsel to confirm the s69 position; treat first emails as the single consent request; opt-out list (`privacy/OUTREACH-OPT-OUT.md`); no further sends until confirmed | Before the next send |
| R-16 | Loss of a key person (bus factor) stops development or incident response | CEO | 4×4 | Code, runbooks and evidence in the repository | 3×4 | **High** | Second person able to release and to respond; documented on-call; succession of signing roles | Month 3 |
| R-17 | Insider fraud or misuse of access | CEO | 2×5 | Product separation of duties; immutable records | 2×4 | **Medium** | Background checks; quarterly access reviews; split release-signing duties | Month 2 |
| R-18 | A vendor (AWS, GitHub, Google) has an outage or breach | ISMS owner | 3×3 | Off-site backups; code in git (distributed) | 2×3 | **Medium** | Vendor register with SOC reports; exit notes for critical vendors | Month 2 |
| R-19 | Conflicting roles held by one person (small team) | CEO | 4×3 | Product-level controls do not depend on staff separation | 4×2 | **Medium** | Accept, with compensating control: an external reviewer for internal audit; CEO reviews logs of privileged actions quarterly | Accept, review yearly |
| R-20 | Denial of service against a hosted endpoint | Eng lead | 3×2 | Rate limiting, HPA, WAF resources | 2×2 | **Low** | Attach WAF when an ALB is used | When hosting |
| R-21 | Source code is public while sales and compliance material says "source under NDA" | CEO | 5×3 | — | 5×3 | **High** | Decide: make the repository private, or change the wording everywhere. Public code is not a security weakness if no secrets are in it, but the contradiction misleads customers | **Now** |
| R-22 | Secrets committed to the repository | Eng lead | 3×4 | Secrets by reference; `.gitignore`; review | 2×4 | **Medium** | GitHub secret scanning and push protection; rotate anything ever exposed | Month 1 |
| R-23 | Regulatory change (FSCA CASP, FIC, MiCA, DORA) makes the product or the company non-compliant | CEO | 3×3 | Legal register; Travel Rule built in | 2×3 | **Medium** | Counsel review once a year | Yearly |
| R-24 | Chain-level events (reorganisation, fee spikes, RPC failure) cause failed or double transfers | Eng lead | 3×3 | Idempotency, one transfer at a time per key, reconciliation against the chain, confirmations | 2×3 | **Medium** | Run own nodes for production chains | Production |

## 3. Risk treatment plan summary

High residual risks that must be treated first: **R-21** (now), **R-06, R-07, R-08** (month 1,
mostly settings found by the check scripts and Sprinto), **R-16** (month 3).

## 4. Acceptance

| Risk IDs accepted | Residual level | Reason | Accepted by | Date | Review |
|---|---|---|---|---|---|
| R-19 | Medium | Team size; compensating controls listed | [CEO, to complete] | | Yearly |

## 5. Review log

| Date | Reviewer | Changes |
|---|---|---|
| 2026-10-11 | Prepared from repository evidence | First version, 24 risks |
