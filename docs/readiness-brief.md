# OpenFireblocks — Capability & Readiness Brief

A one-page, honest summary for evaluating OpenFireblocks as a sovereign,
open-source settlement platform. Pair with the [architecture](architecture.md)
and the [bank-readiness audit checklist](security/audit-checklist.md).

## What it is

A self-hostable alternative to a custodial signing/settlement platform for
agents and financial institutions: MPC signing, policy-enforced transactions,
durable settlement orchestration, and a tamper-evident audit trail — built
entirely on proven open-source components, deployable on your own infrastructure.

## Built and verified today (MVP, Ethereum)

| Capability | Status | Evidence |
|------------|--------|----------|
| Threshold-ECDSA MPC (k-of-n, key never reconstructed) | Core proven | `services/mpc-signer/tss` — 2-of-3 keygen+sign recovers the on-chain address (test) |
| Sign + broadcast (legacy + EIP-1559) | Done | `mpc-signer` Go tests |
| Multi-tenant API with key auth (hashed at rest) | Done | gateway guard tests |
| Policy engine (limits, whitelist, approval, geo) — fail-closed | Done | `policy-service` Rego tests |
| OFAC-style sanctions screening | Done | `policy-service` test |
| Velocity / abuse limits (per-tenant) | Done | `risk.service` tests |
| Durable settlement (policy→sign→broadcast→monitor, approval gate) | Done | Temporal worker tests + gateway `/settlements` |
| Dual audit trail (PostgreSQL + immudb) | Done | every lifecycle event recorded |
| Usage metering (billing foundation) | Done | `billing.service` tests |
| Observability (Prometheus metrics, SLO alerts, Grafana) | Done | `infrastructure/monitoring` |
| Deploy (Helm + K8s, HPA, non-root, probes) | Done | `infrastructure/helm`, `kubernetes` |
| CI across all 8 modules; JS/Go/Python SDKs | Done | `.github/workflows/ci.yml`, `sdks/` |

## Open-source foundation

Binance tss-lib (MPC) · go-ethereum · Temporal (orchestration) · Open Policy
Agent (policy) · HashiCorp Vault (keys) · immudb (immutable ledger) ·
PostgreSQL · Redis · Prometheus/Grafana · Kubernetes/Helm. No proprietary
lock-in; everything runs in your environment.

## What remains before moving customer funds on mainnet

These need external parties or Phase 2/3 engineering and are **prerequisites**,
tracked in the [audit checklist](security/audit-checklist.md):

1. **Distribute the MPC parties** across isolated hosts + per-customer key
   ceremony (the cryptography is proven; the multi-host topology is not built).
2. **External cryptographic audit** of the signing layer + **penetration test**.
3. SOC 2 Type II / ISO 27001; AML/KYC onboarding + automated OFAC feed sync.
4. Edge WAF + mTLS service mesh; HSM-backed Vault auto-unseal.
5. Bank settlement connectors + reconciliation; billing-engine wiring.

## Honest positioning

This is a **credible, demonstrable MVP** — real threshold signing, fail-closed
controls, durable orchestration, full audit trail, production-shaped deployment
and observability, all tested. It is suitable for **testnet pilots, design
partnerships and co-funded hardening**. It is **not yet cleared to custody or
move customer funds on mainnet** until items 1–2 above are complete.
