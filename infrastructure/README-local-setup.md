# Local Setup (Phase 0)

## Prerequisites

- Docker + Docker Compose v2
- (Optional) A Sepolia JSON-RPC URL (Infura/Alchemy/etc.) to broadcast on-chain
- (Optional) Go 1.24+ and Node 22+ to run services outside Docker

## Start the stack

```bash
cd infrastructure
docker compose up -d --build
```

This starts PostgreSQL, immudb, Redis, the MPC signer and the API gateway.

### Optional environment variables

Create `infrastructure/.env` (read automatically by Docker Compose):

```dotenv
# Enable on-chain broadcasting. Without it, the gateway signs + audits only.
ETHEREUM_RPC_SEPOLIA=https://sepolia.infura.io/v3/YOUR_KEY

# Pin the shared signer address across restarts (TESTNET ONLY, never reuse a real key).
MPC_SIGNER_PRIVATE_KEY=0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d
```

## Find the signer address (to fund it on testnet)

```bash
curl -s http://localhost:8080/address
# {"address":"0x..."}
```

Fund that address from a Sepolia faucet before broadcasting real transactions.

## Test signing

```bash
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

## Inspect the audit trail

```bash
# PostgreSQL audit trail
docker compose exec postgres \
  psql -U app -d openfireblocks -c "SELECT event_type, status, request_id FROM audit.events ORDER BY id;"

# Transaction metadata
docker compose exec postgres \
  psql -U app -d openfireblocks -c "SELECT request_id, status, tx_hash FROM signing.transactions;"
```

## Tear down

```bash
docker compose down        # keep data
docker compose down -v      # also delete the postgres volume
```
