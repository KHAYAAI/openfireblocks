# OpenFireblocks API

Base URL (local): `http://localhost:3000`

## Authentication

All tenant routes require an API key, sent as `Authorization: Bearer <key>` or
`X-API-Key: <key>`. The local stack seeds a demo tenant with key `dev-demo-key`.

Admin routes (`/admin/*`) require the admin token (`ADMIN_API_KEY`, default
`dev-admin-key` locally) via `Authorization: Bearer <admin-token>`.

## POST /sign

Sign an Ethereum transaction and, if an RPC endpoint is configured, broadcast it
to Sepolia. Runs a fail-closed policy check first. Requires a tenant API key.

### Request

```json
{
  "chainId": 11155111,
  "to": "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
  "data": "0x",
  "value": "0",
  "gasLimit": 21000,
  "gasPrice": "20000000000",
  "nonce": 0
}
```

| Field | Type | Notes |
|-------|------|-------|
| `chainId` | number | 11155111 for Sepolia, 1 for mainnet |
| `to` | string | 0x-prefixed 20-byte address |
| `data` | string? | 0x-prefixed call data; omit or `"0x"` for a plain transfer |
| `value` | string? | wei as a base-10 string; defaults to `"0"` |
| `gasLimit` | number | ≥ 21000 |
| `gasPrice` | string? | legacy fee, wei per gas (base-10). Omit when using EIP-1559 |
| `maxFeePerGas` | string? | EIP-1559 fee cap (wei). Requires `maxPriorityFeePerGas` |
| `maxPriorityFeePerGas` | string? | EIP-1559 priority tip (wei). Requires `maxFeePerGas` |
| `nonce` | number | sender account nonce |
| `country` | string? | ISO country code for geographic policy checks |

Supply **either** `gasPrice` (legacy) **or** both `maxFeePerGas` +
`maxPriorityFeePerGas` (EIP-1559 dynamic fee).

### Response `200 OK`

```json
{
  "requestId": "uuid",
  "signedTx": "0xf8a6...",
  "txHash": "0x1234...",
  "from": "0xabc...",
  "status": "signed",
  "broadcasted": false
}
```

`status` is `"broadcasted"` (and `broadcasted: true`) when the transaction was
pushed on-chain, otherwise `"signed"`.

### Response `400 Bad Request`

Returned for validation failures (bad address, gasLimit < 21000, unknown fields).

### Response `500 Internal Server Error`

```json
{
  "error": "Signing failed",
  "detail": "…",
  "requestId": "uuid"
}
```

### Response `403 Forbidden` (policy denied)

```json
{ "error": "policy denied", "denials": ["pro tier limited to 50 ETH per transaction"], "requiresApproval": false, "requestId": "uuid" }
```

## GET /prepare

Suggest `nonce`, `gasLimit` and fee fields (from live network data) for building
a sign request. Requires an RPC endpoint. Query params: `to` (required),
`data`, `value`.

```json
{
  "from": "0x...",
  "chainId": 11155111,
  "nonce": 4,
  "gasLimit": 21000,
  "maxFeePerGas": "30000000000",
  "maxPriorityFeePerGas": "2000000000",
  "gasPrice": "25000000000"
}
```

Returns `503` when no RPC endpoint is configured.

## GET /transactions

List the authenticated tenant's transactions (most recent first).

## GET /transactions/:requestId

Fetch one of the tenant's transactions (404 if it isn't theirs).

## GET /transactions/:requestId/audit

The audit trail for one of the tenant's transactions.

## Admin (require admin token)

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/admin/customers` | Create a tenant `{ email, tier?, customerId? }` → returns `api_key` |
| GET | `/admin/customers` | List tenants |
| GET | `/admin/customers/:id` | Get a tenant |
| PUT | `/admin/customers/:id/policies` | Set per-tenant policy overrides `{ policies: { whitelist?, blockedCountries? } }` |
| PUT | `/admin/customers/:id/status` | `{ status: "active"\|"suspended"\|"deleted" }` |

## Observability

- `GET /health` → `{ "status": "ok", "service": "api-gateway" }`
- `GET /health/ready` → readiness (checks PostgreSQL; 503 when not ready)
- `GET /metrics` → Prometheus exposition (unauthenticated; restrict by network policy)
- `GET /docs` → Swagger UI (OpenAPI JSON at `/docs-json`)

---

## MPC signer (internal, port 8080)

Not exposed to end users in production, but useful locally.

### POST /sign

Same request body as above; returns `{ requestId, signedTx, txHash, from, status, auditLogId }`.

### GET /address

```json
{ "address": "0x..." }
```

The shared signer address — fund it from a Sepolia faucet before broadcasting.

### GET /health

```json
{ "status": "ok" }
```
