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
| CA-002 | 2026-10-11 | Self-assessment | Repository is public while outreach says "source under NDA" (R-21) | Wording written for a private repository was kept after the repository was public | — | **Decision 2026-10-12: keep the repository public and change the wording.** Every "source under NDA" claim in website copy, outreach templates, form texts and the 16 Oct follow-ups replaced with "the source code is open for review"; sent-email records annotated. Secret scan of all tracked files: no credentials (only well-known public dev-chain keys) | CEO | 2026-10-12 | 2026-10-12 | `grep` for NDA claims in `docs/pilot`, `docs/marketing`: none left; Gmail drafts to be checked before sending |
| CA-003 | 2026-10-11 | `github-controls-check.sh` | Default branch has no branch protection or ruleset | Never configured; the default branch is a working branch name | — | Owner, in GitHub settings (the session's GitHub access cannot change repository settings): rename the default branch to `main`; add a ruleset: require a pull request, require the CI checks, block force pushes and deletion. Required approvals = 1 only once a second reviewer exists (GitHub never lets authors approve their own PR); until then R-19's compensating control applies. `.github/CODEOWNERS` added 2026-10-12. **Before the rename:** `deploy.yaml` and `test-and-build.yml` trigger on `main` and will start running | Eng lead | [to complete] | | Re-run the check: `branch.*` PASS |
| CA-004 | 2026-10-11 | `github-controls-check.sh` | Dependabot alerts disabled; secret scanning status unknown | Repository security features never turned on | — | Settings > Code security: Dependabot alerts, secret scanning, push protection on | Eng lead | [to complete] | | Re-run: `dependabot.alerts`, `secret_scanning` PASS |
| CA-005 | 2026-10-11 | `github-controls-check.sh` | 39 third-party actions not pinned to a commit (several on `@master`); two workflows without token permissions | Older workflows (`deploy.yaml`, `test-and-build.yml`, `terraform-deploy.yml`) predate the policy | Pinned all 39 to the exact commit each tag or branch pointed to on 2026-10-12 (no behaviour change; Dependabot's github-actions updates keep them current); top-level `permissions: contents: read` plus per-job grants only where a step needs them (OIDC `id-token`, SARIF upload, release, gh-pages, incident issue) | Same, plus `.github/CODEOWNERS` | Eng lead | 2026-10-12 | 2026-10-12 | Re-run 2026-10-12: `actions.pinned`, `actions.permissions`, `repo.codeowners` PASS |

## Pentest findings already closed (for reference)

The external pentest findings of 2026-10-08 and 2026-10-10 were all fixed. Each has a test, and
they are listed in `docs/LAUNCH-READINESS.md` (AUTH-01 to AUTH-05, AUTHZ-01 and -02, MISC-01 to
MISC-04) and `docs/security/PENTEST-ROUND-2-2026-10-10.md`. They predate this log, so they are
referenced rather than copied.
