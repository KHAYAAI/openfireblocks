# Evidence: full-profile threshold key generation and signing, on a real staging VM

| | |
|---|---|
| Date | 2026-10-08 |
| Run by | Project owner, on an AWS EC2 instance (output pasted verbatim below) |
| Machine | AWS EC2 `m6i.2xlarge` (8 vCPU, 32 GiB), Ubuntu 24.04 LTS, eu-west-1 (Ireland) |
| Cluster | kind, Kubernetes 1.29.14, one control plane and three workers |
| Code | Branch `claude/platform-explanation-h0st5y` |
| Profile | **Full profile** (no `LITE=1`): api-gateway (1), mpc-signer (3), policy-service (2), temporal-worker (2), plus billing, compliance, marketplace, policy-api, settlement, webhooks, and three mpc-party pods, each on its own node |
| Secrets | Real generated `ADMIN_API_KEY` / `JWT_SECRET` (openssl rand -hex), not the published dev defaults |
| Test | `infrastructure/kind/smoke-test.sh`, run with `ADMIN_KEY` set to the real generated value |
| Result | **PASS** |

## What this shows

- This is the first time the **full chart** (not the laptop-constrained lite profile) has run
  cleanly end to end from this branch, on a machine sized to actually hold it: 23 pods, all
  `Running`/`Completed`, 0 restarts, stable for 57+ minutes before the test ran.
- A real 2-of-3 distributed key generation ran across three separate pods, each on its own
  node, and the key became active in 35 seconds.
- All three parties sealed their own share in Vault.
- Raw-digest signing was refused until explicitly granted, then a threshold signature was
  produced through the public API by parties 1 and 2.
- An over-limit request was denied (403) and another tenant could not use the key (404,
  enforced by Postgres row-level security).
- The signer recovered from the signature matches the key's address exactly.
- The install used real, randomly generated admin/JWT secrets rather than the published
  development defaults, consistent with this box becoming internet-reachable.

## What this does not show

- No public blockchain, real Stripe account, live TRISA network, custodian, hardware HSM, or
  isolated-host deployment was involved.
- This is a `kind` cluster on a single VM: multiple nodes inside one host, not genuinely
  separate machines. `docs/deployment/PARTY-ISOLATION.md` and the isolation measurement there
  still apply.
- No public URL or HTTPS was configured yet (the gateway was reached via `kubectl
  port-forward` to localhost on the VM, not externally).
- No audit, penetration test, or SOC 2 report exists.

## Raw output (build/install truncated to the deploy summary; full smoke test included verbatim)

```
Built 14 images at openfireblocks/*:latest
...
==> installing chart
Release "ofb" does not exist. Installing it now.
NAME: ofb
LAST DEPLOYED: Thu Oct  8 16:31:15 2026
NAMESPACE: openfireblocks
STATUS: deployed
REVISION: 1
Components:
  - api-gateway     (1 replicas, autoscaling=false)
  - mpc-signer      (3 replicas)
  - policy-service  (2 replicas)
  - temporal-worker (2 replicas)
```

```
$ kubectl -n openfireblocks get pods
NAME                                                  READY   STATUS      RESTARTS   AGE
ofb-migrate-wwth4                                     0/1     Completed   0          59m
ofb-openfireblocks-api-gateway-7cd56498d6-bcw7b       1/1     Running     0          57m
ofb-openfireblocks-billing-74b6f85b46-w2qkn           1/1     Running     0          57m
ofb-openfireblocks-compliance-6cf78ff5d9-6bq2h        1/1     Running     0          57m
ofb-openfireblocks-marketplace-7cb4948854-d7z72       1/1     Running     0          57m
ofb-openfireblocks-mpc-signer-868b468957-65dlk        1/1     Running     0          57m
ofb-openfireblocks-mpc-signer-868b468957-8tkmh        1/1     Running     0          57m
ofb-openfireblocks-mpc-signer-868b468957-m95tc        1/1     Running     0          57m
ofb-openfireblocks-policy-api-8468459fb7-cprfq        1/1     Running     0          57m
ofb-openfireblocks-policy-service-66c4597996-4pm5q    1/1     Running     0          57m
ofb-openfireblocks-policy-service-66c4597996-mjnd4    1/1     Running     0          57m
ofb-openfireblocks-settlement-7f9f6c7848-xfw46        1/1     Running     0          57m
ofb-openfireblocks-temporal-worker-5447f679f8-4w77l   2/2     Running     0          57m
ofb-openfireblocks-temporal-worker-5447f679f8-7mtnp   2/2     Running     0          57m
ofb-openfireblocks-webhooks-58c76dc4c7-8dxrw          1/1     Running     0          57m
party-1-569b76c887-5rv5w                              2/2     Running     0          57m
party-2-668dd456df-7sv9s                              2/2     Running     0          57m
party-3-6fc5d97895-zq8xv                              2/2     Running     0          57m
postgres-7cb7ff478f-ndrkt                             1/1     Running     0          59m
postgres-standby-58f8f6849d-nkkhn                     1/1     Running     0          59m
temporal-frontend-597686c457-lx2lp                    1/1     Running     0          59m
vault-0                                               2/2     Running     0          58m
vault-1                                               2/2     Running     0          58m
vault-2                                               2/2     Running     0          58m
vault-pki-bootstrap-m6hlz                             0/1     Completed   0          59m
```

```
$ curl -s http://127.0.0.1:3000/health/ready
{"status":"ready","checks":{"postgres":"ok"}}

$ ADMIN_KEY=$(grep ADMIN_API_KEY ~/ofb-secrets.txt | cut -d= -f2)
$ ADMIN_KEY="$ADMIN_KEY" infrastructure/kind/smoke-test.sh
==> health
==> creating tenant
    customer 65acae77-5386-4f28-886d-0526ede63bbb
==> provisioning a 2-of-3 threshold key (real DKG)
    active after 35s, address 0xDb56a11eF060Bab77C2423Cb041BB229599fCa9b
==> confirming all three parties sealed a share in Vault
    party-1 sealed a share
    party-2 sealed a share
    party-3 sealed a share
==> raw-digest signing is refused until it is granted
    403 (default: the route policy cannot fully verify is closed)
==> granting it explicitly
==> threshold-signing through the API with 2 of the 3 parties
    signature 2f05310d8382dbb0df1fedd2c136556f... from parties [1, 2]
==> policy gate denies an over-limit request
    403
==> another tenant cannot sign with this key
    404 (row-level security, all the way through the stack)
==> recovering the signer from the signature
    0xDb56a11eF060Bab77C2423Cb041BB229599fCa9b

PASS: threshold key 0xDb56a11eF060Bab77C2423Cb041BB229599fCa9b provisioned by real DKG across three pods,
      shares sealed by all three, policy and tenant isolation enforced,
      and a 2-of-3 signature from the public API recovers to it.
```

## Issues found and fixed along the way (this run)

- Ubuntu's `docker.io` apt package uses the legacy Docker builder with no buildx plugin,
  which the Dockerfiles' `--mount=type=secret` requires. Fixed by replacing it with Docker's
  official engine (`get.docker.com`), which bundles buildx.
- The Docker daemon needed one manual `systemctl restart docker` after that install — the
  first automatic start attempt failed transiently.

## Addendum: public HTTPS (same day)

The gateway is also reachable over real HTTPS, via Caddy reverse-proxying to a systemd-managed
`kubectl port-forward`, with a Let's Encrypt certificate on a free `sslip.io` hostname
(`staging.<elastic-ip>.sslip.io` — no domain purchase needed):

```
$ curl -sv https://staging.52.213.16.179.sslip.io/health/ready
...
* Server certificate:
*  subject: CN=staging.52.213.16.179.sslip.io
*  issuer: C=US; O=Let's Encrypt; CN=YE1
*  SSL certificate verify ok.
* using HTTP/2
< HTTP/2 200
{"status":"ready","checks":{"postgres":"ok"}}
```

`/admin*` and `/metrics*` are blocked at the Caddy layer and return 404 externally; they remain
reachable only via `127.0.0.1:3000` on the VM itself.

Found and fixed along the way: the VM's `fs.inotify.max_user_instances` was the Linux default
of 128, too low for a 4-node kind cluster's kubelet/containerd watches, which made `systemctl
enable` on the port-forward service fail with "Too many open files". Raised to 512 via sysctl.
