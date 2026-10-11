# Compliance checks, first run: 2026-10-11

## GitHub (`scripts/compliance/github-controls-check.sh khayaai/openfireblocks`)

Run at 2026-10-11T19:21Z with an admin-capable token through the session's GitHub proxy (some
security endpoints are not reachable through it, so they show as UNKNOWN).

| Status | Check | Result |
|---|---|---|
| WARN | repo.visibility | Repository is **public** (risk R-21) |
| WARN | repo.default_branch | Default branch is `claude/openfireblocks-mvp-plan-hvr6p1` |
| **FAIL** | branch.protected | **No branch protection and no ruleset on the default branch** |
| **FAIL** | dependabot.alerts | **Dependabot alerts disabled** (version updates are configured in `.github/dependabot.yml`) |
| PASS | dependabot.config | `.github/dependabot.yml` present |
| UNKNOWN | secret_scanning | Not readable through this proxy |
| WARN | repo.codeowners | No CODEOWNERS |
| PASS | repo.security_policy | SECURITY.md present |
| WARN | actions.pinned | 39 third-party action references not pinned to a commit SHA, several on `@master` (`aquasecurity/trivy-action`, `securego/gosec`, `sonarsource/sonarqube-scan-action`, `snyk/actions/golang`, `bridgecrewio/checkov-action`) in `deploy.yaml`, `test-and-build.yml`, `terraform-deploy.yml` |
| WARN | actions.permissions | `deploy.yaml` and `test-and-build.yml` declare no top-level token permissions |

Summary: 2 PASS, 2 FAIL, 5 WARN, 1 UNKNOWN. Entered as CA-003 to CA-005 in
`docs/compliance/isms/08-corrective-actions.md`.

## Repository evidence (the weekly workflow's collect step, run locally)

| Check | Result |
|---|---|
| Route inventory: all 160 routes guarded or deliberately public | PASS |
| `docs/assurance/ROUTE-INVENTORY.md` current | PASS |
| npm audit, production, high + critical: api-gateway / mcp-server | 0 / 0 |
| govulncheck gate (every Go module) | PASS |

## AWS (`scripts/compliance/aws-baseline-check.sh`)

**Not run against the real account.** This environment has no AWS CLI credentials, and the AWS
connector needs re-authorising. The script was tested against a stub AWS CLI in two scenarios: a
misconfigured account (11 FAIL, 4 WARN, matching every planted fault) and a correct one (15 PASS).
Run it with a `SecurityAudit` role, or set `AWS_AUDIT_ROLE_ARN` for the weekly workflow.
