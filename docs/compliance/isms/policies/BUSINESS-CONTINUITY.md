# Business continuity and disaster recovery policy

ISO/IEC 27001:2022 A.5.29, A.5.30, A.8.13, A.8.14. SOC 2 A1.2, A1.3, CC9.1.
Version 0.1, draft, 11 October 2026. Owner: Engineering lead. **Approver: CEO. Not yet approved.** Review: yearly.

## 1. What must keep working

| Process | Why it matters | Max tolerable outage |
|---|---|---|
| Ability to **freeze** signing at a customer (product feature, customer-operated) | Stops theft | Must work whenever the gateway runs; fails closed |
| Recovery of signing keys from 2 of 3 parties | Without it, funds are lost | Must never be lost; time to recover is secondary |
| Releasing a security fix to customers | Customers depend on fixes | 2 working days |
| Customer support and incident communication | Trust, contractual duties | 1 working day |
| Staging environment | Demos and testing only | 5 working days |

## 2. Recovery objectives (staging and any FORGE-hosted environment)

| Objective | Target | Basis |
|---|---|---|
| RPO (data loss) | 24 hours | The chart's backup job runs daily (`backup.schedule.cron`, 02:17). Off-site encrypted copies are built and tested against an S3-compatible stand-in, **not yet against a real bucket**; until they are, a lost cluster loses its backups |
| RTO (time to restore service) | 8 hours | Infrastructure as code, Helm, documented restore |
| Key recovery | Any 2 of 3 parties | Drilled (`docs/evidence/`) |

These are targets until the DR test (`exercises/DR-TEST.md`) has measured them. Only measured
figures are given to customers. `docs/PHASE3-BACKUP-RECOVERY-PROCEDURES.md` quotes RTO 4h/RPO
1h as a design goal; it is not a commitment until tested.

## 3. Scenarios and responses

| Scenario | Response |
|---|---|
| AWS region or account unavailable | Rebuild staging from Terraform and Helm in another region or account; restore from off-site backup |
| GitHub unavailable | Every developer has a full clone; releases wait or are built locally from a verified tag with the same signing process |
| Loss of one signing party | Continue with 2 of 3; replace the third through a re-share or new key and sweep |
| Loss of a key person | Runbooks in the repository; a second person can release and respond (R-16) |
| Google Workspace unavailable | Customer contact list kept in the password manager vault; temporary contact address on the website |
| Ransomware on a laptop | Wipe; restore from git and cloud; rotate every credential on that device |

## 4. Testing

The DR test runs at least yearly and after major infrastructure changes, using
`exercises/DR-TEST.md`. It includes a database restore and a key recovery, and measures the RTO
and RPO actually achieved.

## 5. Security during disruption

Controls do not relax during an outage. Approvals, step-up and policy still apply. Emergency
access is used only through the break-glass procedure in the access control policy.
