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
  "to": "0x742d35Cc6634C0532925a3b844Bc9e7595f42bE",
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
| `gasPrice` | string | wei per gas as a base-10 string |
| `nonce` | number | sender account nonce |

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
- `GET /metrics` → Prometheus exposition (unauthenticated; restrict by network policy)

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
