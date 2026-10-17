# Access control policy (company systems)

ISO/IEC 27001:2022 A.5.15–5.18, A.8.2, A.8.3, A.8.5, A.8.18. SOC 2 CC6.1–CC6.3.
Version 0.1, draft, 11 October 2026. Owner: ISMS owner. **Approver: CEO. Not yet approved.** Review: yearly.

This policy covers FORGE's own systems. Access control **inside the product** (tenant roles,
approvals, API keys, agents) is described in `docs/security/access-control-policy.md`; the two
documents together make up the access control policy.

## 1. Principles

- One identity per person. No shared or generic accounts, except service accounts with a named owner.
- Least privilege: access is granted for a role, not a person's convenience.
- Multi-factor authentication on every company system that supports it. Phishing-resistant
  factors (security keys or passkeys) for administrators of Google Workspace, GitHub and AWS.
- Single sign-on through Google Workspace where the service supports it.

## 2. Systems and who approves access

| System | Standard access | Privileged access | Approver |
|---|---|---|---|
| Google Workspace | All staff | Super admin: at most 2 people | CEO |
| GitHub organisation | Members by team, write via pull request | Owner: at most 2 people; admin on a repository only when needed | Engineering lead |
| AWS | Read-only roles for engineers | Administrator via role, never IAM user keys for people; root account locked away, MFA on, no access keys | Engineering lead + CEO |
| Staging console (product) | Viewer/operator per need | Admin, approver | Engineering lead |
| Sprinto | All staff (as users) | Admin: ISMS owner | CEO |
| Stripe | Developers in test mode | Account owner | CEO |

## 3. Joiners, movers and leavers

Recorded in `records/JOINER-MOVER-LEAVER.md`.

- **Joiner:** access is requested by the manager, approved by the approver above, and given
  only after the person has signed the confidentiality terms and acknowledged the policies.
- **Mover:** access for the old role is removed when the new role starts.
- **Leaver:** all access is removed on the last working day, and in any case within 1 working
  day (objective O-3). Shared secrets the person knew are rotated. Devices are returned or wiped.

## 4. Access reviews

Every quarter the ISMS owner reviews the members, roles and privileged access of every system in
section 2, and records the result in `records/ACCESS-REVIEW-COMPANY.md`. Each organisation in the
staging product is reviewed in the console (Controls > Access review), which stores a signed,
immutable snapshot. Findings are fixed or recorded as a corrective action.

## 5. Privileged access and utilities

Administrative access (`kubectl` with cluster-admin, `psql` as `app_admin`, AWS administrator,
GitHub owner) is used only for the operation that needs it, from a company device, and never for
day-to-day work. Break-glass credentials (AWS root) are stored offline under two-person control
[to complete: where] and their use is reported to the CEO the same day.

## 6. Passwords and authentication information

Passwords are generated and stored by the password manager, at least 14 characters, unique per
service. Recovery codes are stored in the password manager. API keys and tokens have an expiry
wherever the service allows it.
