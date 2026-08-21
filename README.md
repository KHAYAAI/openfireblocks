# OpenFireblocks

A sovereign, open-source settlement infrastructure platform for agents and
financial institutions — built on proven OSS (go-ethereum, Binance TSS-Lib,
Temporal, OPA, immudb, HashiCorp Vault).

> **Status: Phase 1 + Crypto.** Ethereum single-chain. Multi-tenant with API-key
> auth, OPA policy engine, Temporal settlement orchestration, Vault-backed key
> management, Prometheus/Grafana observability, Helm/K8s deploy. Real MPC
> threshold-ECDSA DKG now proven over real multi-process HTTP transport
> (Binance TSS-Lib); threshold signing over the same transport is the next
> increment. **Not yet audited for customer funds or licensed in any
> jurisdiction**; see [`docs/security`](docs/security) and the
> [launch-readiness checklist](#launch-readiness).

## Repository layout

```
openfireblocks/
├── services/
│   ├── mpc-signer/       # Go: Ethereum signing, Vault keys, immudb audit, metrics
│   ├── api-gateway/      # NestJS: auth, multi-tenancy, policy checks, orchestration, metrics
│   ├── policy-service/   # Go + OPA/Rego: amount/whitelist/approval/geo policies
│   └── temporal-worker/  # Go: durable settlement workflow (policy→sign→broadcast→monitor)
├── sdks/
│   ├── sdk-js/           # TypeScript client SDK
│   ├── sdk-go/           # Go client SDK
│   └── sdk-python/       # Python client SDK
├── infrastructure/       # docker-compose, init SQL, K8s manifests, Helm chart, monitoring
├── contracts/            # Solidity (Phase 2+; placeholder)
├── docs/                 # architecture, API, policies, deployment, security, checklists
└── .github/workflows/    # CI (build + test all services)
```

## Quick start

```bash
cd infrastructure
docker compose up -d --build

curl -X POST http://localhost:3000/sign \
  -H "Content-Type: application/json" \
  -d '{
    "chainId": 11155111,
    "to": "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
    "data": "0x",
    "value": "0",
    "gasLimit": 21000,
    "gasPrice": "20000000000",
    "nonce": 0
  }'
```

See [`infrastructure/README-local-setup.md`](infrastructure/README-local-setup.md)
for broadcasting on Sepolia and inspecting the audit trail.

## Developing without Docker

```bash
# MPC signer
cd services/mpc-signer && go build ./... && go run .

# API gateway
cd services/api-gateway && npm install && npm run start:dev && npm test
```

## Launch readiness

See the [**Launch Readiness** document](https://claude.ai/code/artifact/ce28fa83-64df-4fdc-9b7b-0ed5f11e8545)
for a complete breakdown of what this session built, everything between here and
live launch (split by code-buildable vs. regulatory/operational/legal), and an
honest comparison against Copper and Fireblocks.

## Documentation

- [Architecture](docs/architecture.md)
- [API reference](docs/api.md)
- [Policies (OPA)](docs/policies.md)
- [Deployment (Compose / K8s / Helm)](docs/deployment.md)
- [Troubleshooting](docs/troubleshooting.md) · [Operations runbook](docs/runbook.md)
- [Security: threat model](docs/security/threat-model.md) ·
  [audit checklist](docs/security/audit-checklist.md) ·
  [what Claude cannot build](docs/security/what-claude-cannot-build.md)
- Checklists: [Phase 0](docs/phase-0-checklist.md) · [Phase 1](docs/phase-1-checklist.md)

## Roadmap

- **Phase 0** ✅ — Ethereum signing PoC with audit trail.
- **Phase 1** ✅ — Multi-tenancy + API-key auth, OPA policy engine, Temporal
  settlement workflows, Vault key management, Prometheus/Grafana, Helm/K8s, CI, SDK.
- **Phase 2 (in progress)** — Real MPC threshold signing (Binance TSS-Lib):
  - ✅ Cryptographic core (k-of-n DKG) proven in `services/mpc-signer/tss` (in-process)
  - ✅ DKG proven over real multi-process HTTP transport (`services/mpc-party` → verified
    3 independent servers converging on identical key)
  - 🔄 Threshold **signing** over the same real network transport (DKG only so far)
  - 🔄 Production orchestration wired to the real crypto path (currently on placeholder)
  - Remaining: multi-chain (Bitcoin, Solana, Cosmos), risk engine (OFAC), treasury.
- **Phase 3** — Bank-grade hardening: external crypto + pen-test audits, SOC 2 /
  ISO 27001, regulatory reporting, multi-region HA, HSM-backed Vault, key rotation.

> **Not production-ready for customer funds yet.** Phase 2 DKG is proven over real
> HTTP transport but threshold signing isn't complete; the system hasn't cleared
> money-transmitter licensing, SOC 2 Type II, or independent cryptographic audit.
> See the [launch-readiness checklist](https://claude.ai/code/artifact/ce28fa83-64df-4fdc-9b7b-0ed5f11e8545)
> for the full go/no-go gate.

## License

Apache-2.0.
