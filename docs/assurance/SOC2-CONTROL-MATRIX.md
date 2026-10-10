# SOC 2: where each control stands, with its evidence

`docs/PHASE3-SOC2-COMPLIANCE.md` is the framework and roadmap; `scripts/collect-soc2-evidence.sh`
collects operational snapshots. This is the missing middle: for each Trust Services Criterion,
**what the platform already does, where the evidence is, and what is still a person's job.**

Read it as a readiness assessment by the people who built the system, not as an auditor's view.
It will be wrong in places; an auditor's first conversation is the place to find out where.

**No auditor is engaged. No report exists. SOC 2 Type II needs a 3 to 12 month observation
window that cannot start until controls are operating and evidence is being collected.**

Legend: **A** = the platform enforces it and a test or script shows it; **P** = needs a policy
or process owned by a person; **X** = needs something outside the code (a vendor, hardware, a
hire).

| Criterion | What exists | Evidence | Remaining |
|---|---|---|---|
| **CC1 Control environment** | Roles with separated duties are enforced in the database, not by convention. | Migration 023; `approvals_db_test.go` | **P** Board/management oversight, code of conduct, hiring checks. |
| **CC2 Communication** | Incident and customer-communication plans are written. | `docs/security/incident-response-plan.md` | **P** Named owners, a status page, a tested contact tree. |
| **CC3 Risk assessment** | Threat model, tss-lib advisory review, vendor risk assessment, a known-issues list. | `docs/security/threat-model.md`, `TSS-LIB-ADVISORY-REVIEW.md`, `vendor-risk-assessment.md` | **P** A dated risk register reviewed on a schedule. |
| **CC4 Monitoring** | CI on every change; alerting on freeze, failed transfers and approvals; evidence collector. | `.github/workflows/ci.yml`, `controls/alerts.service.ts`, `scripts/collect-soc2-evidence.sh` | **A/P** Run the collector on a schedule and keep the output somewhere durable and access-controlled. |
| **CC5 Control activities** | Fail-closed guards: every route guarded or recorded as public; a role requirement is mandatory under the tenant guard. | `scripts/route-inventory.js --check`, `tenant-role.guard.ts` | — |
| **CC6.1 Logical access** | JWT people, API keys, agent keys, admin key; OIDC SSO with PKCE; TOTP with step-up for approvals; row-level security per tenant. | `docs/assurance/ROUTE-INVENTORY.md`, OIDC e2e (8 checks), RLS tests | **P** Periodic access reviews with records. **X** Real IdP (Keycloak/Entra/Okta) acceptance. |
| **CC6.1 Key management** | 2-of-3 threshold keys; shares sealed in Vault or an encrypted file store; optional PKCS#11 HSM signing; recovery drills. | `docs/deployment/KEY-RECOVERY.md`, `SHARE-STORE.md`, HSM CI job | **X** Parties on genuinely separate hosts and owners; production HSM hardware. |
| **CC6.2 / 6.3 Provisioning and removal** | Roles per organisation, revocable; a removed role takes effect on the next request (not carried in the token). | `tenant-role.guard.ts` | **P** Joiner/mover/leaver procedure and its records. |
| **CC6.6 Boundary protection** | Opt-in NetworkPolicies, mTLS between services, TRISA port isolated in its own policy. | chart `networkpolicy.yaml`, 41 chart invariants | **X** Applied and tested on a real cluster (never done). |
| **CC6.7 Transmission** | TLS everywhere; mTLS between parties; client-side encrypted off-site backups. | `OFFSITE-BACKUPS.md`, mTLS invariants | — |
| **CC6.8 Malicious software** | Signed images (cosign keyless), SBOM, dependency audit gate, `govulncheck`. | `SUPPLY-CHAIN.md`, `deploy.yaml`, CI `dependencies` job | **X** Signing has never run (needs a registry and CI on a real runner). |
| **CC7.1 / 7.2 Detection** | Alerts on freeze, pending approvals, failed transfers, sweeps, received Travel Rule data. | `alerts.service.ts` | **P** On-call, alert routing, response times. |
| **CC7.3 – 7.5 Incident response** | Written plan; freeze control stops all signing in one act; webhooks for lifecycle events. | `incident-response-plan.md`, `controls` | **P** Tabletop exercise with a record. |
| **CC8.1 Change management** | PR + CI; migrations applied in order and tested against real Postgres; chart rendered and validated strictly. | CI | **P** Review requirements and branch protection (settings, not code). |
| **CC9 Risk mitigation / vendors** | Vendor risk assessment written. | `vendor-risk-assessment.md` | **P** Reviews and contracts on record. |
| **A1 Availability** | PodDisruptionBudgets, rolling updates, backups and drills (restore with the parties killed, with and without Vault). | chart, `recovery-drill-local.sh` | **X** An availability SLO measured over time; multi-region has never been applied. |
| **C1 Confidentiality** | Per-tenant RLS; secrets by reference only (custodian tokens, TRISA keys); shares sealed. | RLS tests, `custody` | **P** Data classification and retention policy. |
| **PI1 Processing integrity** | Immutable records for approvals, transfers, whitelist, sweeps, Travel Rule, received Travel Rule data, security-token acts; idempotency keys; one transfer at a time per key. | database triggers (migrations 023 to 034) | — |
| **P (Privacy)** | KYC *references* only for token holders; Travel Rule data is retained as evidence. | `034_security_tokens.sql`, `024_travel_rule.sql` | **P** Privacy notice, retention and deletion policy, DPA. Travel Rule retention versus erasure rights needs counsel. |

## What this means for the order of work

1. Start the evidence collector on a schedule now. It is the only item whose delay directly
   delays the observation window.
2. Name an owner for every **P** row. Most of the audit's duration is waiting on these, not on
   the code.
3. Choose the firm and settle scope and boundary. Consider a Type I first: it needs no
   observation window and often unblocks a commercial conversation while the Type II runs.
4. The **X** rows are real dependencies, mostly shared with the cryptographic review and
   penetration test, and they gate "production ready" as much as the paperwork does.

A SOC 2 report says controls operated as described. It does **not** say the threshold signing
is sound, and it should not be offered to a customer as if it did.
