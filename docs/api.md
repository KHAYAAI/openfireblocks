# OpenFireblocks API (Phase 0)

Base URL (local): `http://localhost:3000`

## POST /sign

Sign an Ethereum transaction and, if an RPC endpoint is configured, broadcast it
to Sepolia.

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

## GET /health (gateway)

```json
{ "status": "ok", "service": "api-gateway" }
```

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
