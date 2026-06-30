# Deployment

## Local (Docker Compose)

```bash
cd infrastructure
docker compose up -d --build
```

Brings up the full stack: PostgreSQL, immudb, Redis, Vault (dev), Temporal
(+ UI on :8088), policy-service, mpc-signer, temporal-worker, api-gateway,
Prometheus (:9090) and Grafana (:3001, anonymous viewer enabled).

Provision a tenant and sign (the demo tenant `dev-demo-key` is seeded):

```bash
curl -X POST http://localhost:3000/sign \
  -H "Authorization: Bearer dev-demo-key" \
  -H "Content-Type: application/json" \
  -d '{"chainId":11155111,"to":"0x742d35Cc6634C0532925a3b844Bc9e7595f42bE","value":"0","gasLimit":21000,"gasPrice":"20000000000","nonce":0}'
```

Create a new tenant (admin token defaults to `dev-admin-key`):

```bash
curl -X POST http://localhost:3000/admin/customers \
  -H "Authorization: Bearer dev-admin-key" \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@bank.example","tier":"enterprise"}'
```

## Kubernetes (Helm)

Prerequisites: a cluster, managed PostgreSQL and Temporal, and a secret holding
credentials.

```bash
kubectl create namespace openfireblocks

kubectl -n openfireblocks create secret generic openfireblocks-secrets \
  --from-literal=database-url='postgresql://USER:PASS@HOST:5432/openfireblocks' \
  --from-literal=admin-api-key='REPLACE_ME' \
  --from-literal=vault-token='REPLACE_ME'   # optional

helm -n openfireblocks upgrade --install ofb infrastructure/helm/openfireblocks \
  --set imageRegistry=ghcr.io/yourorg \
  --set external.temporalHostPort=YOUR_TEMPORAL:7233 \
  --set external.ethereumRpcSepolia=https://sepolia.infura.io/v3/KEY \
  --set mpcSigner.vault.addr=http://vault.vault:8200
```

Key values (see `infrastructure/helm/openfireblocks/values.yaml`):
- `apiGateway.autoscaling` — HPA (default 3–10 replicas @ 70% CPU)
- `*.resources` — requests/limits per component
- `serviceMonitor.enabled` — set true with the Prometheus Operator installed
- `ingress.enabled` — front the gateway with an ingress

### Raw manifests

A standalone, non-Helm set lives in `infrastructure/kubernetes/`:

```bash
kubectl apply -f infrastructure/kubernetes/namespace.yaml
kubectl apply -f infrastructure/kubernetes/secrets.example.yaml   # EDIT FIRST
kubectl apply -f infrastructure/kubernetes/configmap.yaml
kubectl apply -f infrastructure/kubernetes/deployments.yaml
kubectl apply -f infrastructure/kubernetes/services.yaml
```

## Images

Each service has a multi-stage Dockerfile producing a small, non-root image
(distroless for Go, node:22-alpine for the gateway). Build + push, e.g.:

```bash
docker build -t ghcr.io/yourorg/mpc-signer:latest services/mpc-signer
docker build -t ghcr.io/yourorg/api-gateway:latest services/api-gateway
docker build -t ghcr.io/yourorg/policy-service:latest services/policy-service
docker build -t ghcr.io/yourorg/temporal-worker:latest services/temporal-worker
```
