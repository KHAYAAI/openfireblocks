# Pre-Production / Bank-Readiness Audit Checklist

Status legend: ✅ done · 🟡 partial · ⬜ not started

## Cryptography & key management
- 🟡 Vault-backed signing key (KV v2) — single shared key in place
- ⬜ Real MPC threshold signing (Binance TSS-Lib DKG, k-of-n, key never reconstructed)
- ⬜ HSM-backed Vault auto-unseal
- ⬜ Documented key ceremony + rotation procedure
- ⬜ External cryptographic audit of the signing layer

## Application security
- ✅ API-key authentication on all tenant routes
- ✅ Admin endpoints behind a separate fail-closed admin token
- ✅ Strict request validation (reject unknown fields)
- ✅ Fail-closed policy enforcement before signing
- ✅ Per-tenant velocity limiting (Redis, tier-based hourly caps)
- ✅ API keys hashed at rest (SHA-256; plaintext shown once)
- 🟡 Rate limiting (per-IP throttler in-app; WAF/Kong at the edge still ⬜)
- ✅ Security headers (helmet)
- ⬜ External penetration test

## Tenant isolation & data
- ✅ All reads/writes scoped by `customer_id`
- 🟡 PostgreSQL RLS (scaffolded, not enforced)
- ✅ Dual audit trail (PostgreSQL + immudb)
- ⬜ Audit trail export + retention policy

## Infrastructure & operations
- ✅ Non-root, read-only-rootfs containers
- ✅ Resource limits, health probes, HPA, pod anti-affinity
- ✅ Prometheus metrics + SLO alert rules + Grafana dashboard
- ✅ CI builds + tests on every push
- ⬜ mTLS between services (service mesh)
- ⬜ Multi-region / DR with RTO/RPO < 1h
- ⬜ Secrets via external-secrets / sealed-secrets (no plaintext in cluster)

## Compliance
- ⬜ SOC 2 Type II
- ⬜ ISO 27001
- ⬜ AML/KYC + OFAC sanctions screening
- ⬜ Regulatory reporting (SAR/CTR; POPIA/SARS for ZA)

## Go/No-go

A mainnet deployment moving customer funds is **NO-GO** until every ⬜ in
Cryptography & key management and Application security is ✅, and an external
cryptographic audit + penetration test have passed.
