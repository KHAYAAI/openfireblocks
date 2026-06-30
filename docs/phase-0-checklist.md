# Phase 0 Checklist

## Deliverables

- [x] Proof-of-concept monorepo with MPC signer + API gateway
- [x] Go MPC signer service (`services/mpc-signer/`)
  - [x] `/sign` endpoint
  - [x] Ethereum signing logic (EIP-155, single shared key)
  - [x] immudb immutable audit logging (best-effort)
  - [x] `/address` and `/health` endpoints
- [x] NestJS API gateway (`services/api-gateway/`)
  - [x] `sign.controller.ts` + `sign.service.ts`
  - [x] PostgreSQL transaction + audit persistence
  - [x] Optional on-chain broadcast via ethers.js
  - [x] Strict request validation (class-validator)
  - [x] Unit tests (mocked, no infra required)
- [x] Docker Compose for local dev (`infrastructure/docker-compose.yml`)
- [x] PostgreSQL schema (`infrastructure/init-db.sql`)
  - [x] `audit.events`
  - [x] `signing.transactions`
  - [x] `signing.keys`
- [x] Documentation (`docs/architecture.md`, `docs/api.md`, this checklist)

## Success criteria

- [x] `POST /sign` accepts a valid request and returns a signed transaction
- [x] Signed transaction returned in well under 1 second
- [x] Audit trail logged to PostgreSQL (and immudb when reachable)
- [ ] Broadcast to Sepolia (requires a funded signer address + `ETHEREUM_RPC_SEPOLIA`)
- [x] 0 critical errors in the happy path

## How to verify

```bash
cd infrastructure && docker compose up -d --build
curl -s http://localhost:3000/health
curl -X POST http://localhost:3000/sign -H "Content-Type: application/json" -d '{
  "chainId": 11155111, "to": "0x742d35Cc6634C0532925a3b844Bc9e7595f42bE",
  "data": "0x", "value": "0", "gasLimit": 21000, "gasPrice": "20000000000", "nonce": 0
}'
docker compose exec postgres psql -U app -d openfireblocks \
  -c "SELECT event_type, status FROM audit.events ORDER BY id;"
```

## Out of scope (Phase 1)

Multi-tenancy, auth, OPA policy engine, Temporal workflows, HashiCorp Vault,
real MPC threshold signing, Prometheus/Grafana, Kubernetes/Helm/Terraform.
