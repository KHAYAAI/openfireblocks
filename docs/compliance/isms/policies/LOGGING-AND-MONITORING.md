# Logging and monitoring policy

ISO/IEC 27001:2022 A.8.15, A.8.16, A.8.17, A.5.28. SOC 2 CC7.2, CC7.3.
Version 0.1, draft, 11 October 2026. Owner: Engineering lead. **Approver: CEO. Not yet approved.** Review: yearly.

## 1. What is logged

| Source | Events | Retention |
|---|---|---|
| Product (gateway, signer, workflows) | Authentication, approvals and decisions, transfers, policy decisions, freezes, whitelist changes, agent payments, access reviews, admin actions | Immutable tables per the retention policy; `audit.events` |
| AWS | CloudTrail management events in all regions; GuardDuty findings; VPC flow logs where hosted | 1 year at least |
| GitHub | Organisation audit log; Actions logs | GitHub's retention; exported monthly if Sprinto does not collect it |
| Google Workspace | Admin and login audit logs | Google's retention |
| Laptops | Sprinto agent posture checks | Sprinto |

Logs never contain secrets, key material, full API keys or passwords. Personal information in logs
is kept to what is needed (an email address or user ID).

## 2. Protection

Product records that matter for money and compliance are append-only, enforced by database
triggers; they cannot be changed through the application. CloudTrail log files have integrity
validation on, and the bucket is not writable by the people whose actions it records.

## 3. Clocks

All servers use NTP (AWS Time Sync Service). One-time codes depend on correct time, so clock
drift is visible quickly.

## 4. Monitoring and alerting

The product raises alerts for: an organisation being frozen or unfrozen, pending approvals near
expiry, failed transfers, sweeps, Travel Rule failures. Alerts go to the configured alert webhook
and, in staging, to [to complete: channel and on-call person]. GuardDuty findings of Medium and
above go to the same place.

Someone looks at alerts every working day. The monthly ISMS cycle reviews the check-script output
and any trends.

## 5. Use of logs as evidence

When an incident may lead to legal action or a regulator report, the relevant logs are exported,
hashed (SHA-256) and stored read-only, with a record of who exported them and when (A.5.28).
