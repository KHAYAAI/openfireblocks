# Architecture (Phase 0)

OpenFireblocks is a sovereign, open-source settlement layer. Phase 0 is a
minimal but complete proof of concept that signs and broadcasts a single-chain
(Ethereum) transaction while recording an immutable audit trail.

## Components

| Component | Tech | Role |
|-----------|------|------|
| API gateway | NestJS (TypeScript) | Validates requests, orchestrates sign → broadcast, records the PostgreSQL audit trail |
| MPC signer | Go + go-ethereum | Signs Ethereum transactions, records the immudb audit trail |
| PostgreSQL | postgres:15 | Transaction metadata + audit trail |
| immudb | codenotary/immudb | Cryptographically verifiable immutable ledger |
| Redis | redis:7 | Reserved for Phase 1 (caching / queues) |

## Request flow

```
client
  │  POST /sign  (chainId, to, value, gasLimit, gasPrice, nonce)
  ▼
API gateway (NestJS)
  ├─ validate (class-validator)
  ├─ audit: SIGN_REQUEST_RECEIVED  ──────────────► PostgreSQL audit.events
  │
  ├─ POST /sign ──► MPC signer (Go)
  │                   ├─ audit: SIGN_REQUEST_RECEIVED ─► immudb
  │                   ├─ build + EIP-155 sign tx (single shared key)
  │                   ├─ audit: SIGN_SUCCESS ──────────► immudb
  │                   └─ return { signedTx, txHash, from }
  │
  ├─ persist tx (status=signed) ─────────────────► PostgreSQL signing.transactions
  ├─ audit: SIGN_SUCCESS ────────────────────────► PostgreSQL audit.events
  │
  ├─ if RPC configured: broadcast via ethers.js ─► Ethereum (Sepolia)
  │     └─ audit: BROADCAST_SUCCESS/FAILED ──────► PostgreSQL audit.events
  ▼
response { requestId, signedTx, txHash, from, status, broadcasted }
```

## Phase 0 deliberate simplifications

- **Single shared signing key** instead of real MPC threshold signing. The Go
  service is structured so Binance TSS-Lib (distributed key generation +
  threshold signing) drops into `signer.go` in Phase 1.
- **No auth / multi-tenancy / policies.** Added in Phase 1.
- **Legacy (EIP-155) transactions.** EIP-1559 fee market support comes later.
- **Best-effort immudb.** If immudb is unreachable the signer keeps serving and
  the PostgreSQL audit trail remains authoritative.

## Dual audit trail

Every event is written to PostgreSQL (queryable) and, from the signer, to immudb
(tamper-evident). This gives both convenient querying and a cryptographically
verifiable ledger without a single point of failure.

## Phase 1 additions

Phase 1 turns the PoC into a multi-tenant MVP. New components and cross-cutting
concerns:

| Component | Tech | Role |
|-----------|------|------|
| Customers + auth | NestJS guards | API-key tenant auth, admin token, tenant scoping on every row |
| policy-service | Go + OPA/Rego | Amount/whitelist/approval/geo policies; fail-closed |
| temporal-worker | Go + Temporal | Durable settlement workflow with retries + approval gate |
| Vault | HashiCorp Vault | Signing-key storage (KV v2) |
| Prometheus/Grafana | — | Metrics, SLO alerts, dashboards |

Two settlement paths exist:

1. **Synchronous** `POST /sign` — gateway runs policy → MPC sign → persist →
   optional broadcast. Low latency; used for the p99 < 500ms SLO.
2. **Durable** Temporal workflow — `policy → sign → broadcast → monitor` with
   retries, timeouts, confirmation tracking and a human-approval signal for
   high-value transactions. Used when settlement finality must survive crashes.

Both call the same policy-service and mpc-signer, so policy enforcement is
identical regardless of path.

### Phase 1 simplifications still in place

- Single shared signing key (real MPC threshold signing is Phase 2).
- API keys stored as-is (hashing is on the audit checklist).
- Rate limiting / mTLS not yet enabled by default.
