# Incident Response Plan

**Status**: Draft. The procedures below describe intended process; the
supporting tooling (persistent incident tracking, on-call paging, automated
detection) is partially implemented — see §8 for what actually exists today
versus what this plan assumes.

**Version**: 0.2 (11 October 2026): containment updated to the current
product (freeze, 2-of-3, token revocation); §10 regulatory notification
(POPIA s22, GDPR art. 33/34, Cybercrimes Act) and §11 contacts added. Part of
the ISMS in `docs/compliance/isms/`; exercised with
`docs/compliance/isms/exercises/TABLETOP.md`.

**Owner**: Incident lead [to complete: name] (see `docs/compliance/isms/02-information-security-policy.md` §4)
**Review cycle**: Semi-annually, and after every incident (post-incident
review must feed back into this document).

---

## 1. Purpose

Defines how OpenFireblocks detects, responds to, communicates about, and
learns from security incidents. Satisfies SOC 2 CC7.3/CC7.4 (incident
response) and gives the team a plan to execute under pressure rather than
inventing one during an active incident.

## 2. Definitions

**Security incident**: any confirmed or suspected event that compromises the
confidentiality, integrity, or availability of customer data, funds, or
platform systems. Includes but is not limited to: unauthorized access,
key material compromise, data breach, denial of service, and critical
production outages with security implications.

**Near-miss**: an event that could have become an incident but was
prevented or self-resolved (e.g., a blocked intrusion attempt). Logged but
does not trigger the full response process below.

## 3. Severity Classification

| Severity | Definition | Example | Initial response time |
|---|---|---|---|
| **P1 — Critical** | Active fund loss, confirmed key material compromise, active data breach | An MPC key share is confirmed exfiltrated; funds are moving to an unauthorized address | Immediate (page on-call) |
| **P2 — High** | Potential fund risk, suspected compromise, platform-wide outage | Sanctions screening silently disabled; anomalous signing volume from one tenant | < 15 minutes |
| **P3 — Medium** | Contained security issue, no immediate fund/data risk | A dependency with a known CVE is in production but not exploited | < 4 hours |
| **P4 — Low** | Policy violation, hygiene issue | An engineer used a shared credential instead of their own | < 24 hours |

## 4. Roles

| Role | Responsibility |
|---|---|
| **Incident Commander (IC)** | Owns the response; makes the call/no-call on containment actions; single point of coordination |
| **Technical Lead** | Drives investigation and remediation; reports findings to the IC |
| **Communications Lead** | Owns customer and internal communications; drafts status page updates |
| **Scribe** | Maintains the incident timeline in real time — who did what, when |

For P3/P4, one person may hold multiple roles. For P1/P2, roles should be
separate people where headcount allows — the IC should not also be doing
hands-on remediation, since that splits their attention from coordination.

## 5. Response Process

### 5.1 Detection
Sources: CloudWatch alarms, Prometheus/Grafana alerts, customer report,
internal discovery during code review or testing, third-party disclosure
(bug bounty, responsible disclosure).

### 5.2 Triage (target: within response-time SLA in §3)
1. Confirm the event is real (not a false positive).
2. Assign severity per §3.
3. Declare an incident: create a tracking record (see §8 for current
   tooling gap), notify the IC.
4. IC assembles the response team appropriate to severity.

### 5.3 Containment
Immediate actions to stop the incident from getting worse, before full root
cause is understood. Examples specific to this platform:
- **Funds at risk, any cause**: **freeze the organisation** (console:
  Controls > Safety controls, or `POST /organisations/:id/controls/freeze`).
  An approver or admin can freeze; only an admin can lift it. Freezing stops
  every signing path at once and fails closed: if the platform cannot tell
  whether an organisation is frozen, it refuses to sign. Freeze first, then
  investigate. In a customer-operated installation the customer presses it;
  FORGE advises.
- **Suspected key compromise**: freeze, then generate a new key and sweep
  funds to it (`docs/security/key-rotation.md`). Keys are 2-of-3: one
  compromised share does not on its own allow signing, but two do, so
  confirm which parties' hosts and administrators could be affected before
  treating it as contained.
- **Suspected account compromise**: suspend the account
  (`status = 'suspended'`), which blocks new logins, and revoke its
  sessions: tokens carry a `jti` and are denylisted on sign-out (AUTH-03,
  AUTH-04 fixes). Remove the person's roles in affected organisations and
  check their recent approvals and agent creations.
- **AI agent misuse**: revoke the agent (console: Agents, or the agents
  API). Revocation takes effect on the next request. Review its spend history.
- **Suspected API abuse**: rotate the organisation API key; rate limiting
  and velocity limits engage automatically (velocity fails closed).
- **Platform-wide**: be prepared to take services offline (health-check
  failure / scale to zero) rather than serve requests under active
  compromise.

### 5.4 Eradication
Remove the root cause: patch the vulnerability, rotate all potentially
exposed credentials (not just the confirmed-compromised one), close the
access path.

### 5.5 Recovery
Restore normal operation. Verify the fix holds under load before declaring
recovery complete. For fund-related incidents, verify no further
unauthorized signing/settlement activity occurred by reviewing
`signing_requests` and `settlements` for the affected key(s)/customer(s).

### 5.6 Post-Incident Review
Required for all P1/P2 within 5 business days of resolution. Blameless —
the goal is process/system improvement, not individual fault. Must produce:
- Timeline of detection through resolution
- Root cause
- What worked / what didn't in the response itself
- Concrete action items with owners and due dates
- Updates to this plan, if the response revealed a gap in it

## 6. Communication

### 6.1 Internal
IC keeps the response team and leadership updated at a cadence matching
severity (continuous for P1, hourly for P2, daily for P3).

### 6.2 Customer
- P1 affecting customer funds or data: notify affected customers as soon as
  containment is confirmed and communication won't interfere with an active
  investigation. Do not wait for full root cause.
- Regulatory breach notification timelines (e.g. GDPR's 72-hour requirement
  for personal data breaches) take precedence over internal comfort with
  disclosure timing — legal/compliance must be looped in immediately for
  any incident involving customer PII.

### 6.3 Public
Status page and/or public disclosure for incidents with customer-visible
impact, coordinated by the Communications Lead. No incident details are
posted publicly until the Communications Lead and IC agree the information
won't aid an ongoing attack.

## 7. Evidence Preservation

For any incident that may involve unauthorized access or fund movement:
- Do not modify or delete logs, database rows, or Vault audit trails
  related to the incident, even ones that look like attacker-created noise.
- Preserve `audit_logs`, `compliance_checks`, and relevant `signing_requests`
  /`settlements` rows for the affected time window before any remediation
  that might alter them.
- If law enforcement or legal involvement becomes likely, stop ad hoc
  investigation and follow legal counsel's guidance on evidence handling.

## 8. Current Tooling — What Actually Exists

Honest inventory, so responders know what to rely on versus what to build
under pressure:

- **Audit logging**: `audit_logs` table (PostgreSQL) captures action/actor/
  resource/changes with timestamps. Real, queryable.
- **Compliance checks**: `compliance_checks` table records KYC/AML/risk
  assessment outcomes.
- **Incident tracking**: `services/compliance/incident_response.go`'s
  `SecurityIncident`/`IncidentManager` is now backed by PostgreSQL
  (`security_incidents`, `incident_response_plans` — migration
  `010_compliance_audits_incidents_metrics.sql`), not an in-memory map. It
  survives a process restart and is shared across service instances. HTTP
  endpoints exist for reporting, acknowledging, updating status, and
  querying incidents (`/v1/incidents/*` on the compliance service) —
  verified end-to-end against a live PostgreSQL instance, including that a
  reported incident's timeline persists across separate requests. What this
  does *not* give you: paging/on-call integration (see below), automated
  detection, or a UI — it's a durable record, not an alerting system.
- **Alerting**: CloudWatch alarms and Prometheus alert rules are configured
  (`infrastructure/monitoring/`) for infrastructure metrics. No automated
  security-specific detection (e.g., anomalous signing pattern alerting) is
  wired up yet beyond the policy engine's synchronous checks.
- **On-call/paging**: not configured in this repository. PagerDuty/Opsgenie
  integration is referenced in documentation but not present as working
  code or configuration.

## 9. Action Items to Close the Gap

- [x] Persist `IncidentManager` state to PostgreSQL.
- [ ] Wire real on-call paging (PagerDuty/Opsgenie) to CloudWatch/Prometheus
      alerts.
- [ ] Build anomalous-signing-pattern detection beyond synchronous policy
      checks (e.g., a batch job flagging statistical outliers).
- [ ] Run a tabletop exercise against this plan with the real team before
      relying on it for a real incident.

## 10. Regulatory and legal notification

Decide **within 24 hours of confirming an incident** whether any of the
following apply. The Information Officer leads; counsel advises. Record the
decision and the reasons even when the answer is "not required".

| Duty | Applies when | Who notifies whom | Deadline |
|---|---|---|---|
| POPIA s22 | Reasonable grounds to believe personal information was accessed or acquired by an unauthorised person, and FORGE is the responsible party | FORGE → Information Regulator (prescribed form, eServices portal) **and** the data subjects (unless identity cannot be established, or a public body says notice would impede an investigation) | "As soon as reasonably possible" after discovery |
| POPIA s21(2) | FORGE is an **operator** for a customer (hosted environment) | FORGE → the customer (responsible party), who then notifies under s22 | Immediately |
| GDPR art. 33 | Personal data breach of EU/UK data, FORGE as controller, unless unlikely to result in a risk | FORGE → the lead supervisory authority | Within **72 hours** of becoming aware; a late notice must give reasons |
| GDPR art. 34 | High risk to individuals | FORGE → the data subjects | Without undue delay |
| GDPR art. 33(2) | FORGE as **processor** | FORGE → the controller (customer) | Without undue delay |
| Cybercrimes Act s54 | Applies to electronic communications service providers and financial institutions aware of their system being involved in certain offences. FORGE is likely neither, but **customers that are financial institutions are** | Customer → SAPS; FORGE supports | Within 72 hours (customer's duty) |
| Customer contracts | As each contract states | FORGE → customer | Per contract [to complete] |
| Law enforcement | Theft of funds or other crime | FORGE or the customer → SAPS (case number); on-chain theft: notify the stablecoin issuer to request a freeze where the token supports it | As soon as possible |

**Content of a notice** (POPIA s22(5), GDPR art. 33(3)): what happened and
when; the categories and approximate numbers of people and records; likely
consequences; measures taken or proposed; what affected people can do;
contact details of the Information Officer. If facts are not yet known,
notify with what is known and follow up.

**Log**: every personal-information incident, notified or not, is recorded
(GDPR art. 33(5)) in the incident record, with the decision.

## 11. Contacts

Keep current; checked at every tabletop exercise.

| Who | Contact |
|---|---|
| Incident lead | [to complete: name, phone] |
| Deputy / second responder | [to complete] |
| CEO | [to complete] |
| Information Officer | [to complete] |
| Counsel | [to complete] |
| Information Regulator (South Africa) | https://inforegulator.org.za, breach reporting via the eServices portal; [confirm current email for complaints and POPIA compliance] |
| EU lead supervisory authority | [to complete, only if EU data is processed] |
| SAPS (cybercrime / commercial crime) | Local station for a case number; [to complete] |
| AWS support / abuse | AWS console support case; [plan level, to complete] |
| GitHub security | https://github.com/contact (account takeover), support case |
| Customers' security contacts | Kept in the customer contract records |
