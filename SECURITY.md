# Security Policy

OpenFireblocks handles signing key material and moves value on-chain. Security is
treated as a first-class, blocking concern.

## Reporting a vulnerability

Email **security@openfireblocks.example** with details and reproduction steps.
Do not open public issues for security reports. We aim to acknowledge within
24 hours and provide a remediation timeline within 72 hours.

## Scope highlights

- MPC signing key material (Vault-backed; never logged or returned by the API).
- Tenant isolation (every data path is scoped by `customer_id`).
- Policy engine (fail-closed: an unreachable policy service denies the request).
- Audit trail integrity (PostgreSQL + immudb cryptographic ledger).

## Pre-production requirements

Before any mainnet / customer-funds deployment, the items in
[`docs/security/audit-checklist.md`](docs/security/audit-checklist.md) must be
satisfied, including an external cryptographic review of the MPC layer and a
penetration test of the API surface. The current build is an MVP and has **not**
yet undergone external audit.
