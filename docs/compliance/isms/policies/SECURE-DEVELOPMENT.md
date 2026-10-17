# Secure development and change management policy

ISO/IEC 27001:2022 A.5.8, A.8.4, A.8.25–8.29, A.8.31–8.33. SOC 2 CC8.1, CC7.1.
Version 0.1, draft, 11 October 2026. Owner: Engineering lead. **Approver: CEO. Not yet approved.** Review: yearly.

## 1. Source control

- All code, infrastructure definitions and database migrations are in git, on GitHub.
- The release branch is protected: changes arrive only by pull request, need at least one
  approving review by someone other than the author, and need CI to pass. Force pushes and
  deletions are blocked. Administrators are included in the rules. (Checked by
  `scripts/compliance/github-controls-check.sh`.)
- Secret scanning and push protection are enabled.

## 2. Design

Any change that touches signing, approvals, policy, authentication, tenant isolation, key
management or money movement states its security effect in the pull request: what could go
wrong, and which test shows it does not. The threat model is updated when a trust boundary
changes.

## 3. Coding rules

- Validate all input at the boundary (DTOs with whitelisting; unknown fields refused).
- Fail closed: an error in a control stops the action.
- Every new route is guarded or recorded as deliberately public; the route inventory check in CI
  enforces it.
- No secrets in code; no raw HTML from data in the console.
- Database rules that protect money or records are enforced in the database (triggers, RLS), not
  only in application code.

## 4. Testing

Unit tests, live-database tests and end-to-end tests run in CI on every change. Security fixes
come with a test that fails before the fix. Test data is generated; real personal data and real
keys are never used in tests.

## 5. Dependencies

Dependencies are pinned. CI runs `npm audit` and `govulncheck` gates; exceptions are recorded in
`docs/security/accepted-vulnerabilities.json` with a reason and an expiry.

## 6. Environments

Development (local, kind), staging (AWS) and customer production are separate. Development-only
switches (for example `RISK_ALLOW_DISABLED`) are set only in development profiles.

## 7. Release

Images are built by CI from the protected branch, signed, and published with an SBOM. Release
notes list security-relevant changes. Customers are told about security fixes as set out in
the vulnerability policy.

## 8. Emergency changes

An emergency fix may be merged with a single reviewer, or reviewed after merging, only during an
incident declared by the incident lead. It is reviewed fully within 2 working days and recorded in
the corrective-action log.

## 9. AI-assisted development

Code written with AI assistants is held to the same review, test and CI rules as any other code.
AI tools must not be given secrets or Restricted information.
