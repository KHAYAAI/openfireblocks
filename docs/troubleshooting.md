# Troubleshooting

## `401 Unauthorized` on /sign
Missing or invalid API key. Pass `Authorization: Bearer <key>` (or
`X-API-Key`). The seeded local tenant key is `dev-demo-key`. Suspended tenants
also return 401.

## `403` with `{ "denials": [...] }`
A policy rejected the transaction (e.g. amount over the tier limit, recipient not
whitelisted, blocked country). Inspect the `denials` array. Adjust the tenant via
`PUT /admin/customers/:id/policies` or lower the amount.

## `500 Signing failed`
The gateway could not reach the MPC signer, or the request was structurally
invalid for signing. Check:
- `mpc-signer` is up: `curl localhost:8080/health`
- the `requestId` in the response, then `GET /transactions/:id/audit`

## Policy service "denying by default"
The gateway logs this when `policy-service` is unreachable — it fails closed.
Verify `curl localhost:8081/health` and `POLICY_SERVICE_URL`.

## immudb warnings in mpc-signer logs
`immudb audit logger unavailable ... continuing without immutable ledger` means
the signer started before immudb was ready. Signing still works and PostgreSQL
remains the authoritative audit trail; the signer does not retry the immudb
session in the MVP — restart the signer once immudb is healthy to re-enable it.

## Broadcasting does nothing
`ETHEREUM_RPC_SEPOLIA` is unset, so the gateway signs and audits but does not
broadcast (`broadcasted: false`). Set a real RPC URL and fund the signer address
(`curl localhost:8080/address`) from a faucet.

## Temporal worker not picking up workflows
- Worker connected? Check its logs for "polling task queue".
- Temporal reachable at `TEMPORAL_HOSTPORT` (default `temporal:7233`)?
- Inspect workflows in the Temporal UI at http://localhost:8088.

## Vault: signer fails to start
When `VAULT_ADDR` is set the signer is fail-closed on Vault errors. Verify the
token and that the KV v2 mount (`VAULT_KV_MOUNT`, default `secret`) exists. Unset
`VAULT_ADDR` to fall back to `MPC_SIGNER_PRIVATE_KEY` / an ephemeral key.
