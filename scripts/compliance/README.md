# Compliance checks

Read-only checks of the company's own accounts, run on demand, weekly by
`.github/workflows/compliance-evidence.yml`, and by Sprinto once it is connected.
Each check prints PASS / FAIL / WARN / UNKNOWN per item, can write JSON
(`--json out.json`), and exits 1 on any FAIL unless `--no-fail` is given.
UNKNOWN means the check could not see the setting. It is never counted as a pass.

| Script | Checks | Needs |
|---|---|---|
| `github-controls-check.sh [owner/repo]` | Visibility, default branch, branch protection and rulesets (reviews, force push, deletion, status checks, admins), Dependabot alerts and config, secret scanning and push protection, CODEOWNERS, SECURITY.md, third-party actions pinned to SHAs, workflow token permissions | `gh` or `GITHUB_TOKEN`, `jq`. An admin token sees more |
| `aws-baseline-check.sh [--regions "..."] [--all-regions]` | Root MFA and keys, IAM user MFA and key age (90 days), password policy, multi-region CloudTrail and log validation, S3 account and bucket public access, bucket encryption, EBS default encryption, GuardDuty, AWS Config, security groups open to the internet on admin and data ports, IMDSv2, RDS encryption and exposure | `aws` CLI with the AWS-managed `SecurityAudit` policy, `jq` |

Policy that sets these requirements: `docs/compliance/isms/policies/CLOUD-SERVICES.md`.
A failed check goes into `docs/compliance/isms/08-corrective-actions.md`.

To run the AWS check weekly from GitHub Actions, create an IAM role with
`SecurityAudit`, trusting `token.actions.githubusercontent.com` for this
repository, and set the repository variable `AWS_AUDIT_ROLE_ARN` (and
optionally `AWS_CHECK_REGIONS`).
