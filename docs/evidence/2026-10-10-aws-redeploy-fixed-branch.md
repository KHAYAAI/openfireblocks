# AWS staging redeploy of the fixed branch (2026-10-10)

Deployment: the single-VM kind cluster (full profile) behind Caddy at
`staging.52.213.16.179.sslip.io`, redeployed from `claude/platform-explanation-h0st5y` at `744013a`
with the existing generated secrets (not the dev defaults). Cluster state and data were preserved
(34 migrations already applied, 0 new).

## What was done
1. Updated the checkout (fast-forward `f4ddd80` to `744013a`) and re-ran `infrastructure/kind/up.sh`
   with `ADMIN_API_KEY` and `JWT_SECRET` exported from the secrets file (plain `source` does not
   export them, and `up.sh` would otherwise fall back to the published dev values).
2. Added an unauthenticated, in-cluster Redis (`redis:7-alpine`) and set `external.redisUrl` with
   `external.riskAllowDisabledVelocityLimiting=false`. The kind profile has no Redis by default, and
   without it token/session revocation (AUTH-03, AUTH-04) is a no-op and velocity limiting (MISC-02)
   is not enforced. Staging only: this Redis has no password and no persistence.
3. `rollout restart` of `api-gateway` and `webhooks`, because the images are tagged `:latest` and a
   Helm upgrade alone does not replace running pods.
4. Restarted the `ofb-forward` systemd port-forward, which had pinned the replaced gateway pod
   (Caddy returned 502 until then).

## Results
- Pods: all Running, 0 restarts on the rebuilt services; Redis Running.
- Gateway log: no "REDIS_URL not set" warning.
- `infrastructure/kind/smoke-test.sh`: **PASS**. Real DKG across three pods (active after 45 s), all
  three shares sealed in Vault, raw-digest signing refused until granted, 2-of-3 signature from the
  public API recovers to the key's address, over-limit request denied (403), cross-tenant use
  refused (404).
- From an allowed external IP: `/health/ready` returns `ready`; `/docs` returns 404 (Swagger off,
  MISC-03); `/admin/customers` returns 404 (blocked at the edge).

## Not yet done
- Re-run of the Shannon pentest against this deployment (the real test of all 11 fixes).
- The firewall allows only the operator's current IP (ports 22, 80, 443); port 80 must be opened
  before certificate renewal.
