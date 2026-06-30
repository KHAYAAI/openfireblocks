# Phase 1 Checklist (MVP)

Status: ✅ done · 🟡 partial · ⬜ not started

## Delivered
- ✅ Multi-tenancy: customers table, API-key auth guard, admin guard, tenant-scoped
  audit/transactions and read endpoints
- ✅ OPA policy-service (Go, embedded Rego): amount/tier limits, whitelist,
  approval flagging, geographic blocking; fail-closed integration
- ✅ Temporal settlement worker: policy → sign → broadcast → monitor, with
  retries, timeouts and a high-value human-approval signal gate
- ✅ Vault-backed signing key (KV v2) with env/ephemeral fallback
- ✅ Prometheus metrics (gateway + signer), SLO alert rules, Grafana dashboard
- ✅ Kubernetes manifests + Helm chart (probes, limits, non-root, HPA, anti-affinity)
- ✅ Docker Compose full stack (incl. Temporal, Vault, Prometheus, Grafana)
- ✅ CI (GitHub Actions): build + vet + test all Go services, gateway, SDK
- ✅ JS/TS SDK with tests
- ✅ Security docs: threat model, audit checklist, SECURITY.md

## Success criteria (spec)
- ✅ Multi-tenant (100+ customers supported by design; RLS scaffolded)
- ✅ Temporal workflows orchestrate the full flow
- ✅ OPA policies enforce transaction rules
- ✅ Kubernetes deployment with ≥3 replicas (HPA 3–10)
- ✅ Prometheus metrics + Grafana dashboards + alerts
- ✅ Audit trail for 100% of transactions (PostgreSQL + immudb)
- 🟡 API latency p99 < 500ms — instrumented + alerted; needs a load test to confirm
- ⬜ Load test: 1000 concurrent signing requests (k6 script — Phase 2)
- ⬜ Kill Bill billing integration (deferred)

## Deliberately deferred to Phase 2/3
Real MPC threshold signing, multi-chain, risk engine (velocity/OFAC), billing,
rate limiting/WAF, mTLS, external audits, compliance certifications.
