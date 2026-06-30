# Changelog

All notable changes to OpenFireblocks. This project is pre-1.0; the MVP is not
yet audited for customer funds (see `docs/security/audit-checklist.md`).

## [Unreleased]

### Phase 1 — MVP

Added
- Multi-tenancy: customers, API-key auth (hashed at rest), admin token, tenant
  scoping, admin CRUD, rate limiting, security headers, readiness probe.
- OPA policy-service: amount/tier limits, whitelist, approval, geographic
  blocking and OFAC-style sanctions screening (fail-closed).
- Per-tenant velocity/risk limits (Redis).
- Temporal settlement worker + gateway `/settlements` API (durable
  policy→sign→broadcast→monitor with a human-approval gate).
- Real threshold-ECDSA MPC core (Binance tss-lib), Ethereum-verified.
- EIP-1559 + legacy signing; Vault-backed keys; `/prepare` gas endpoint.
- Usage metering (billing foundation) + admin usage endpoint.
- Prometheus metrics (gateway + signer), SLO alerts, Grafana dashboard.
- Helm chart + raw K8s manifests; full Docker Compose stack.
- CI for all modules; Dependabot; Makefile; JS/Go/Python SDKs; Swagger UI.
- Docs: architecture, API, policies, deployment, runbook, troubleshooting,
  threat model, audit checklist, readiness brief.

### Phase 0 — Proof of concept

Added
- Go MPC signer (single shared key) with immudb audit logging.
- NestJS gateway: validated `/sign`, PostgreSQL audit + transaction metadata,
  optional Sepolia broadcast.
- Docker Compose (postgres, immudb, redis), schema, docs.
