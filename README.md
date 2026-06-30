# OpenFireblocks

A sovereign, open-source settlement infrastructure platform for agents and
financial institutions — built on proven OSS (go-ethereum, Binance TSS-Lib,
Temporal, OPA, immudb, HashiCorp Vault).

> **Status: Phase 0 (Proof of Concept).** Single-chain (Ethereum), single shared
> signing key, no auth. Proves the end-to-end flow: request → sign → broadcast →
> immutable audit trail.

## Repository layout

```
openfireblocks/
├── services/
│   ├── mpc-signer/     # Go service: Ethereum signing + immudb audit trail
│   └── api-gateway/    # NestJS API: validation, orchestration, PostgreSQL audit, broadcast
├── infrastructure/     # docker-compose.yml, init-db.sql, local setup guide
├── contracts/          # Solidity (Phase 1+; placeholder in Phase 0)
└── docs/               # architecture, API reference, phase-0 checklist
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
- [Phase 0 checklist](docs/phase-0-checklist.md)

## Roadmap

- **Phase 0** — Ethereum signing PoC with audit trail *(this milestone)*.
- **Phase 1** — Multi-tenancy, OPA policy engine, Temporal workflows, HashiCorp
  Vault, real MPC threshold signing, Prometheus/Grafana, Kubernetes.
- **Phase 2+** — Multi-chain (Bitcoin, Solana, Cosmos), risk engine, treasury,
  bank-grade hardening.

## License

Apache-2.0.
