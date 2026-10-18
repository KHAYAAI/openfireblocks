# Cloud services policy

ISO/IEC 27001:2022 A.5.23, A.8.9, A.8.20–8.22. SOC 2 CC6.1, CC6.6, CC6.7, CC9.2.
Version 0.1, draft, 11 October 2026. Owner: Engineering lead. **Approver: CEO. Not yet approved.** Review: yearly.

## 1. Approved cloud services

Only services in the vendor register may hold company data. The main ones: AWS (staging and any
hosting), GitHub (code and CI), Google Workspace (email and documents), Sprinto (compliance). A
new service follows the supplier policy first.

## 2. Shared responsibility

FORGE is responsible for how it configures each service. The minimum configuration below is
checked by `scripts/compliance/aws-baseline-check.sh` and `scripts/compliance/github-controls-check.sh`
(and by Sprinto once connected).

### AWS

- Root user: MFA on, no access keys, used only for break-glass.
- People sign in through IAM Identity Center or IAM users with MFA; no long-lived access keys for
  people. Machine credentials use roles; any remaining keys rotate within 90 days.
- CloudTrail on in all regions, with log file validation, to a bucket people cannot alter.
- GuardDuty and AWS Config on in the regions in use.
- EBS encryption by default on; S3 Block Public Access on at account level; S3 buckets encrypted.
- Security groups: no SSH or RDP from 0.0.0.0/0 or ::/0; management access from named IPs or SSM.
- EC2 instances require IMDSv2.
- Infrastructure is created from Terraform/Helm in git; manual changes are recorded and folded
  back into code.
- Region: af-south-1 (Cape Town) preferred for South African personal information; any other
  region is a cross-border transfer (privacy policy §5).

### GitHub

- MFA required for the organisation.
- Default branch protected (see the secure development policy).
- Secret scanning and push protection on; Dependabot alerts on.
- Repository visibility decided deliberately (risk R-21) and checked monthly.
- Actions: third-party actions pinned by commit SHA or from verified creators; workflow token
  permissions read-only by default.

### Google Workspace

- 2-step verification enforced, security keys for super admins.
- External sharing of Drive files restricted to named people; no "anyone with the link" for
  Confidential documents.
- Login and admin audit logs retained.

## 3. Exit

For each Critical cloud service there is a way out: code is in git everywhere, infrastructure is
code, backups are portable (Postgres dumps), and email and documents can be exported.
