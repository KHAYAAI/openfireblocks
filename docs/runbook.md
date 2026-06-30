# Operations Runbook

Operational reference for running OpenFireblocks in production. Pair with
[deployment.md](deployment.md) and [troubleshooting.md](troubleshooting.md).

## Service map

| Service | Port | Stateless? | Scale |
|---------|------|-----------|-------|
| api-gateway | 3000 | yes | HPA 3–10 |
| mpc-signer | 8080 | holds key material | fixed (3); scale deliberately |
| policy-service | 8081 | yes (policies embedded) | 2+ |
| temporal-worker | — | yes | 2+ |

Dependencies: PostgreSQL, immudb, Redis, Vault, Temporal, an Ethereum RPC.

## Key SLOs & where to watch

- Sign latency p99 < 500ms — Grafana "OpenFireblocks Platform" dashboard.
- MPC error rate < 5%, broadcast failure < 2%, policy denials < 10% — Prometheus
  alerts (`infrastructure/monitoring/alerts.yml`).

## Common operations

### Provision a tenant
`POST /admin/customers` with the admin token. Record the returned `api_key` — it
is shown once and stored only as a hash.

### Suspend a compromised tenant
`PUT /admin/customers/:id/status {"status":"suspended"}` — takes effect on the
next request (auth lookup filters on `status='active'`).

### Update the sanctions list
Sync `services/policy-service/sanctions.json` from the OFAC SDN feed and redeploy
policy-service (the list is embedded at build time).

### Rotate the admin token
Update the `admin-api-key` secret and restart the gateway.

## Incident response

### Suspected key compromise (SEV-1)
1. Suspend signing: scale `mpc-signer` to 0 (`kubectl scale deploy/...-mpc-signer --replicas=0`).
2. Revoke the Vault token / seal Vault.
3. Preserve the audit trail (PostgreSQL `audit.events` + immudb) — do not prune.
4. Begin key-ceremony rotation (Phase 2 MPC: re-share; MVP: new key + re-fund).

### Policy-service down
Signing fails closed (all requests denied) by design. Restore policy-service;
check `curl :8081/health`. No bad transactions can slip through during the outage.

### Database unreachable
`/health/ready` returns 503 and Kubernetes stops routing to the pod. Restore
PostgreSQL; audit writes resume. immudb continues to hold the signer-side ledger.

### Runaway tenant (abuse)
Velocity limits (Redis) cap per-tenant throughput automatically. To hard-stop,
suspend the tenant (above).

## Backups
- PostgreSQL: standard PITR backups (audit + tx metadata + usage).
- immudb: replicate per its HA guide; the ledger is the tamper-evident record.
- Vault: follow Vault's backup/DR (key shares are the crown jewels).
