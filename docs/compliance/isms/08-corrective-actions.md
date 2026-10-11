# 08 Nonconformity and corrective action log

ISO/IEC 27001:2022 clauses 10.1 and 10.2. SOC 2 CC4.2, CC7.4, CC7.5.
Owner: ISMS owner.

Every nonconformity, audit finding, pentest finding of Medium or above, incident action, and
failed check (Sprinto, `aws-baseline-check.sh`, `github-controls-check.sh`) is entered here, or
in Sprinto with the same fields.

**For each entry:** react (contain and correct), find the root cause, decide whether it could
happen elsewhere, act to stop it recurring, then check that the action worked.

| ID | Date raised | Source | Description | Root cause | Correction (immediate) | Corrective action (prevent recurrence) | Owner | Due | Closed | Effective? (how checked) |
|---|---|---|---|---|---|---|---|---|---|---|
| CA-001 | 2026-10-11 | Self-assessment | `docs/PHASE3-ISO27001-ISMS.md` claimed appointments and meetings that had not happened | Draft written as a target state but worded as fact | Replaced with a pointer to this pack | Rule in `09-document-control.md`: nothing is marked done without a record | ISMS owner | 2026-10-11 | 2026-10-11 | Pack reviewed for unsupported claims [reviewer to confirm] |
| CA-002 | 2026-10-11 | Self-assessment | Repository is public while outreach says "source under NDA" (R-21) | [to complete: why the two diverged] | — | Decide visibility; align all wording | CEO | [to complete] | | |
| CA-003 | 2026-10-11 | `github-controls-check.sh` | Default branch has no branch protection or ruleset | Never configured; the default branch is a working branch name | — | Rename the default branch to `main`; protect it: pull request with 1 review, required CI checks, no force push or deletion, include admins; add CODEOWNERS for signing, approvals, migrations and workflows | Eng lead | [to complete] | | Re-run the check: `branch.*` PASS |
| CA-004 | 2026-10-11 | `github-controls-check.sh` | Dependabot alerts disabled; secret scanning status unknown | Repository security features never turned on | — | Settings > Code security: Dependabot alerts, secret scanning, push protection on | Eng lead | [to complete] | | Re-run: `dependabot.alerts`, `secret_scanning` PASS |
| CA-005 | 2026-10-11 | `github-controls-check.sh` | 39 third-party actions not pinned to a commit (several on `@master`); two workflows without token permissions | Older workflows (`deploy.yaml`, `test-and-build.yml`, `terraform-deploy.yml`) predate the policy | — | Pin each to a commit SHA with a version comment (Dependabot keeps them current); add `permissions: contents: read` at the top; retire workflows no longer used | Eng lead | [to complete] | | Re-run: `actions.*` PASS |

## Pentest findings already closed (for reference)

The external pentest findings of 2026-10-08 and 2026-10-10 were all fixed. Each has a test, and
they are listed in `docs/LAUNCH-READINESS.md` (AUTH-01 to AUTH-05, AUTHZ-01 and -02, MISC-01 to
MISC-04) and `docs/security/PENTEST-ROUND-2-2026-10-10.md`. They predate this log, so they are
referenced rather than copied.
