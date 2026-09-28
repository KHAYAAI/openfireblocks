# OpenFireblocks — Capability & Readiness Brief

A one-page summary for evaluating OpenFireblocks as a sovereign,
self-hostable settlement platform. Pair with the
[architecture](architecture.md) and the
[bank-readiness audit checklist](security/audit-checklist.md).

> **Superseded for anything load-bearing.** [LAUNCH-THESIS.md](LAUNCH-THESIS.md)
> carries the current readiness assessment, stage gates and evidence, and is
> the one to give a buyer or an investor. This page is kept as the short
> orientation and is updated less often.

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

Binance tss-lib (MPC) · Temporal (orchestration) · Open Policy
Agent (policy) · HashiCorp Vault (keys) · immudb (immutable ledger) ·
PostgreSQL · Redis · Prometheus/Grafana · Kubernetes/Helm. No proprietary
lock-in; everything runs in your environment.

go-ethereum was removed deliberately and a CI gate keeps it out — it is
LGPL-3.0, and static linking it into a distributed binary carries a
relinking obligation this product cannot meet. See
[LICENSING.md](LICENSING.md) and
[engineering/GO-ETHEREUM-REMOVAL.md](engineering/GO-ETHEREUM-REMOVAL.md).

## What remains before moving customer funds on mainnet

These need external parties or Phase 2/3 engineering and are **prerequisites**,
tracked in the [audit checklist](security/audit-checklist.md):

1. **Distribute the MPC parties** across isolated hosts. The chart already
   spreads them across nodes and `infrastructure/kind/party-isolation-check.sh`
   reports the level reached, but on any single-host cluster that level is
   `simulated` — which is a 1-of-1 key wearing a costume. This is the one
   item standing between the platform and real money.
2. **External cryptographic audit** of the signing layer + **penetration test**.
3. SOC 2 Type II / ISO 27001; AML/KYC onboarding + automated OFAC feed sync.
4. Edge WAF + mTLS service mesh; HSM-backed Vault auto-unseal.
5. Bank settlement connectors + reconciliation; billing-engine wiring.

Since this list was written, three of its assumptions have moved:
proactive key refresh, a drilled recovery procedure and ceremony
co-signing now exist. See [LAUNCH-THESIS.md](LAUNCH-THESIS.md) section 2
for what is proven and how.

## Honest positioning

This is a **credible, demonstrable MVP** — real threshold signing, fail-closed
controls, durable orchestration, full audit trail, production-shaped deployment
and observability, all tested. It is suitable for **testnet pilots, design
partnerships and co-funded hardening**. It is **not yet cleared to custody or
move customer funds on mainnet** until items 1–2 above are complete.
