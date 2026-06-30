# OpenFireblocks

A sovereign, open-source settlement infrastructure platform for agents and
financial institutions — built on proven OSS (go-ethereum, Binance TSS-Lib,
Temporal, OPA, immudb, HashiCorp Vault).

> **Status: Phase 1 (MVP).** Ethereum single-chain. Multi-tenant with API-key
> auth, OPA policy engine, Temporal settlement orchestration, Vault-backed key
> management, Prometheus/Grafana observability, Helm/K8s deploy. Signing still
> uses a single shared key (real MPC threshold signing is Phase 2) — **not yet
> audited for customer funds**; see [`docs/security`](docs/security).

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
    "to": "0x742d35Cc6634C0532925a3b844Bc9e7595f42bE",
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

## Documentation

- [Architecture](docs/architecture.md)
- [API reference](docs/api.md)
- [Policies (OPA)](docs/policies.md)
- [Deployment (Compose / K8s / Helm)](docs/deployment.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Security: threat model](docs/security/threat-model.md) ·
  [audit checklist](docs/security/audit-checklist.md)
- Checklists: [Phase 0](docs/phase-0-checklist.md) · [Phase 1](docs/phase-1-checklist.md)

## Roadmap

- **Phase 0** ✅ — Ethereum signing PoC with audit trail.
- **Phase 1** ✅ — Multi-tenancy + API-key auth, OPA policy engine, Temporal
  settlement workflows, Vault key management, Prometheus/Grafana, Helm/K8s, CI, SDK.
- **Phase 2** — Real MPC threshold signing (Binance TSS-Lib): cryptographic core
  (k-of-n DKG + signing) proven in `services/mpc-signer/tss`; remaining work is
  distributing parties + per-customer ceremonies. Plus multi-chain (Bitcoin,
  Solana, Cosmos), risk engine (OFAC), treasury.
- **Phase 3** — Bank-grade hardening: external crypto + pen-test audits, SOC 2 /
  ISO 27001, regulatory reporting, multi-region HA.

> **Not production-ready for customer funds yet.** The MVP still uses a single
> shared signing key and has not been externally audited. See the
> [audit checklist](docs/security/audit-checklist.md) for the go/no-go gate.

## License

Apache-2.0.
