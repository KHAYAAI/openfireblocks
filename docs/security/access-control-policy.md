# Access Control Policy

**Status**: Draft — reflects controls actually implemented in this codebase as
of this commit, not aspirational controls. Update this document whenever the
underlying implementation changes; a policy that no longer matches what the
system does is worse than no policy, since an auditor or incident responder
will trust it.

**Owner**: [assign — typically CTO or Head of Security]
**Review cycle**: Quarterly, or on any material change to authentication/authorization code.

---

## 1. Purpose

This policy defines how access to OpenFireblocks systems, data, and customer
funds is granted, reviewed, and revoked. It exists to satisfy SOC 2 CC6.1
(logical access controls) and CC6.2 (authentication) and to give incident
responders and auditors a single source of truth for "who can do what, and
why."

## 2. Scope

Covers:
- Human access to production systems (AWS console, databases, Vault, CI/CD)
- Human access to the customer-facing dashboard (`services/api-gateway`
  `IdentityModule`)
- Machine-to-machine access between services and to the tenant API
  (`ApiKeyGuard`)
- Access to cryptographic key material (Vault, MPC party shares)

Does not cover: physical security (no owned data centers; inherited from
AWS), which is addressed in the Vendor Risk Assessment for AWS.

## 3. Principles

1. **Least privilege.** Every credential grants the minimum access needed
   for its holder's actual job. Default deny.
2. **Named accountability.** No shared logins. Every credential, API key,
   and IAM principal traces to exactly one person or one service.
3. **Time-bounded access.** Elevated access is granted for a defined
   duration, not indefinitely, where the tooling supports it.
4. **Separation of duties.** No single person can both approve and execute
   a change to production key material or customer funds without a second
   party (see §7, Signing Operations).

## 4. Identity and Authentication

### 4.1 Human users (dashboard)

Implemented in `services/api-gateway/src/identity/`:

- Password: bcrypt, cost factor 12. Minimum 12 characters, must include
  upper, lower, digit, and special character (`register.dto.ts`).
- MFA: TOTP (RFC 6238) via `otplib`. **Not currently mandatory** — a user
  can operate without enrolling. This is a gap; see §9.
- Account lockout: 5 consecutive failed attempts locks the account for 15
  minutes (`users.service.ts`).
- Sessions: JWT access tokens, 1-hour expiry, HMAC-signed. No refresh token
  rotation implemented yet — a leaked JWT is valid for its full lifetime.
- SSO/SAML: not implemented. Every account is a local username/password
  account today.

### 4.2 Machine/tenant access (API)

Implemented in `services/api-gateway/src/auth/`:

- API keys: 32-byte random values, SHA-256 hashed at rest, plaintext shown
  once at creation (`api-key.util.ts`). Comparison is constant-time.
- Admin API: a single static bearer token from `ADMIN_API_KEY`, fail-closed
  if unset (`admin.guard.ts`). **This is a known weak point** — one shared
  static token for all admin operations, no per-admin accountability. See §9.

### 4.3 Infrastructure access (AWS, Vault, databases)

- AWS: IAM roles per service (Terraform `modules/*`), least-privilege
  policies scoped to what each service actually calls (e.g. Vault's IAM role
  is scoped to its own S3 backend bucket and its own KMS key — see
  `infrastructure/terraform/security.tf`).
- Vault: AppRole authentication intended for services; human break-glass
  access procedure not yet documented (see §9).
- Database: application connects via a single shared `app` role today
  (`DATABASE_URL`). **No per-human database credentials exist.** Anyone with
  the connection string has full read/write access to all tenant data. This
  is acceptable only because RLS is not yet enforced either — both are
  tracked together in §9.

## 5. Authorization (Roles)

Defined in `db/migrations/001_initial_schema.sql` (`roles` table) and
enforced via JWT claims:

| Role | Permissions |
|---|---|
| `admin` | Full access: users, keys, signings, policies, compliance, audit, webhooks, billing |
| `billing_admin` | Users (read), billing (full), reports (read), audit (read) |
| `user` | Keys, signings, policies (read), webhooks |
| `viewer` | Keys (read), signings (read), reports (read) |

Role assignment happens at user creation; there is no self-service role
escalation path, and no approval workflow for role changes yet (a direct
database update is the only mechanism today — tracked in §9).

## 6. Provisioning and Deprovisioning

### 6.1 Onboarding
1. Account created via `POST /v1/auth/register` (dashboard users) or by an
   admin via the customer-provisioning endpoint (tenant API keys).
2. Role assigned by an admin at creation time.
3. MFA enrollment offered but not enforced (see §9).

### 6.2 Offboarding
- User account: set `status = 'suspended'` — login is rejected
  (`AuthService.login` checks `user.status !== 'active'`).
- API keys: revoked via key deletion; hash comparison fails immediately on
  next use since the row no longer exists.
- Sessions: **not currently invalidated on suspension.** A JWT issued before
  suspension remains valid until it expires (up to 1 hour). Tracked in §9.

### 6.3 Access reviews
- **Not yet automated.** No scheduled job produces "who has access to what"
  reports. Until automated, access reviews must be performed manually on
  the cadence in §8.

## 7. Signing Operations (Customer Funds)

The highest-sensitivity access in this system is anything that can move
customer funds:

- Threshold signing requires 4-of-7 key shares (`mpc-party`/`mpc-signer`) —
  no single party, including OpenFireblocks operations staff, can
  unilaterally sign a transaction with a properly-ceremony'd key.
- Policy engine (`services/policy`) enforces amount limits, address
  whitelisting, and multi-approver requirements before a signing request is
  even routed to the threshold signers — this is a defense-in-depth layer
  in front of the cryptographic threshold, not a replacement for it.
- **Caveat**: the threshold signing integration itself has known
  unresolved build/correctness issues as of this document (see the
  temporal-worker and mpc-party sections of the engineering changelog) —
  this policy describes the intended control, not a control that has been
  independently verified end-to-end against production infrastructure.

## 8. Review Cadence

| Activity | Frequency | Owner |
|---|---|---|
| Access review (who has admin role) | Quarterly | Security lead |
| API key audit (unused/stale keys) | Quarterly | Engineering lead |
| IAM policy review (AWS) | Quarterly | Infrastructure lead |
| This policy document | Quarterly or on material change | Security lead |

## 9. Known Gaps (tracked, not hidden)

An access control policy that omits its own known weaknesses is misleading
to an auditor. As of this document:

- [ ] MFA is available but not mandatory for any role, including admin.
- [ ] Admin API access is a single shared static token, not per-admin.
- [ ] No per-human database credentials; a single shared application role
      has full read/write access to all tenant data.
- [ ] Row-level security (RLS) is scaffolded in the schema but not
      consistently enforced across all services.
- [ ] Sessions/JWTs are not invalidated on account suspension.
- [ ] No automated access review tooling; reviews are manual.
- [ ] No documented Vault break-glass procedure for human emergency access.
- [ ] No role-change approval workflow; role changes are direct database
      writes.

These gaps should be closed, in roughly this priority order, before this
policy can be represented to an auditor as "fully implemented" rather than
"partially implemented, in progress."
