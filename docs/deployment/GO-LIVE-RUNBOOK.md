# Go-Live Runbook

The concrete sequence for taking OpenFireblocks from "verified locally" to
"running against a real cluster and a real chain."

This is deliberately narrow: it covers only what has to happen *at cutover*,
in order, with the check that proves each step worked. It does not repeat the
architecture (`docs/architecture.md`), the incident procedures
(`docs/security/incident-response-plan.md`), or the non-engineering blockers
that gate a launch date regardless of engineering readiness
(`docs/security/what-claude-cannot-build.md` — read that one first if you are
setting a date).

Every step below has a **verify** line. If the verify fails, stop; do not
proceed to the next step. A custody platform that is half-cut-over is worse
than one that has not started.

---

## 0. What you must have before starting

None of these can be produced by the codebase. Every one of them has been a
hard blocker on getting past local verification:

| Requirement | Why | Consumed by |
|---|---|---|
| AWS account + credentials with admin on the target account | Terraform has only ever run `validate`/`plan`; it has never been applied | `infrastructure/terraform` |
| A Kubernetes cluster (EKS or otherwise) + `kubectl` context | The Helm chart renders clean but has never been applied to a live cluster | `infrastructure/helm/openfireblocks` |
| A container registry, and images pushed to it | `values.yaml`'s `imageRegistry` must point somewhere real | all 14 services |
| An Ethereum RPC endpoint + API key (Alchemy/Infura/self-hosted) | `ETHEREUM_RPC_SEPOLIA` is unset; broadcast and balance queries need it | api-gateway, temporal-worker, backup |
| A funded testnet address | Nothing on-chain has ever been broadcast — sweep, settlement and monitoring are all unexercised against a live chain | balance migration, settlement |
| A Temporal deployment (Cloud or self-hosted) | Workflows are proven against a local dev server only | api-gateway, temporal-worker |
| A DNS name + TLS certificate for the public API | Ingress is disabled by default | `values.yaml` `ingress` |
| WorkOS project + API key (only if using SSO) | The integration is complete in code but has no project behind it | api-gateway |

---

## 1. Provision infrastructure

```bash
cd infrastructure/terraform
cp terraform.tfvars.example terraform.tfvars   # then fill it in for real
terraform init
terraform plan -out=go-live.plan               # READ THIS. Do not skip.
terraform apply go-live.plan
```

**Verify:** `terraform output` returns real ARNs/endpoints, and the RDS
instance and Vault nodes are reachable from the cluster's subnets.

> Vault PKI and Kubernetes auth are **off by default** (`vault_pki_enabled`,
> `vault_kubernetes_auth_enabled`). They must stay off until Vault is
> initialized and unsealed — a manual step Terraform cannot perform. Come
> back and enable them at step 4.

---

## 2. Initialize and unseal Vault

Auto-unseal via AWS KMS is configured (`seal "awskms"` in the rendered
`vault.hcl`), so this is a one-time initialization, not a per-restart step.

```bash
vault operator init      # store the recovery keys per your key-ceremony policy
vault status
```

**Verify:** `vault status` reports `Sealed: false` and `Seal Type: awskms`.
If it reports `shamir`, the KMS seal did not take effect — stop and fix that
before any key material exists.

---

## 3. Apply database migrations

Migrations run in filename order and are **not** applied by the chart.

```bash
for f in infrastructure/database/migrations/*.sql; do
  psql "$DATABASE_ADMIN_URL" -v ON_ERROR_STOP=1 -f "$f"
done
```

**Verify:**

```sql
-- RLS must be forced, not merely enabled
SELECT relname, relrowsecurity, relforcerowsecurity
FROM pg_class WHERE relname IN ('customers','key_pairs','signing_requests');

-- the app role must NOT be able to bypass it
SELECT rolname, rolbypassrls FROM pg_roles WHERE rolname IN ('app','app_admin');
```

`app` must have `rolbypassrls = false`. If it is true, tenant isolation is
not being enforced no matter what the policies say.

---

## 4. Enable Vault PKI and Kubernetes auth

Now that Vault is unsealed:

```bash
cd infrastructure/terraform
terraform apply \
  -var 'vault_pki_enabled=true' \
  -var 'vault_kubernetes_auth_enabled=true' \
  -var 'vault_kubernetes_host=https://<cluster-api-endpoint>' \
  -var "vault_kubernetes_ca_cert=$(cat <cluster-ca.pem)"
```

**Verify:** `vault read pki/<env>/roles/internal-service` returns the role,
and a manual issue works:

```bash
vault write pki/<env>/issue/internal-service common_name=party-1.internal ttl=1h
```

This is the exact call `services/vault-pki-init` makes as an init container.
If it fails here, every mTLS-enabled pod will fail to start.

---

## 5. Create secrets

The chart expects a pre-existing Secret (see
`infrastructure/helm/openfireblocks/templates/secret.yaml` for the full key
list and the External Secrets Operator alternative, which is preferable to
the one-shot below):

```bash
kubectl create secret generic openfireblocks-secrets \
  --from-literal=database-url='postgresql://app:...' \
  --from-literal=database-admin-url='postgresql://app_admin:...' \
  --from-literal=admin-api-key='...' \
  --from-literal=jwt-secret="$(openssl rand -hex 32)" \
  --from-literal=vault-token='...' \
  --from-literal=workos-api-key='sk_...'   # only if workos.enabled
```

**Verify:** `JWT_SECRET` is genuinely random and at least 32 bytes. The
gateway refuses to boot in production without it, but it will happily accept
a weak one.

---

## 6. Deploy

```bash
helm upgrade --install openfireblocks infrastructure/helm/openfireblocks \
  --set imageRegistry=<your-registry> \
  --set external.temporalHostPort=<temporal>:7233 \
  --set external.ethereumRpcSepolia=<rpc-url> \
  --set mpcParty.mtls.enabled=true \
  --set mpcParty.mtls.autoIssue.enabled=true \
  --set mpcParty.mtls.autoIssue.vaultAddr=http://vault.vault:8200 \
  --set temporalWorker.mtls.enabled=true \
  --set temporalWorker.mtls.autoIssue.enabled=true \
  --set temporalWorker.mtls.autoIssue.vaultAddr=http://vault.vault:8200 \
  --set ingress.enabled=true --set ingress.host=<your-host>
```

**Verify:** all pods `Running`; specifically check an init container did its
job, since that is the newest moving part:

```bash
kubectl logs party-1 -c vault-pki-init     # "issued and wrote mTLS certificate"
kubectl exec party-1 -- ls /etc/openfireblocks/mtls   # tls.crt tls.key ca.crt
```

---

## 7. Prove the platform actually works end to end

Do not treat "pods are running" as done. Run the real customer path:

```bash
# 1. provision a tenant
curl -sX POST https://<host>/admin/customers \
  -H "Authorization: Bearer $ADMIN_API_KEY" \
  -d '{"email":"launch-test@yourdomain","tier":"pro"}'

# 2. create a key -- this starts a REAL DKG ceremony across the parties
curl -sX POST https://<host>/keys \
  -H "Authorization: Bearer $CUSTOMER_API_KEY" \
  -d '{"name":"launch-test","blockchain":"ethereum","threshold":2,"total_parties":3}'

# 3. poll until it activates and returns a real address
curl -s https://<host>/keys/<key-id> -H "Authorization: Bearer $CUSTOMER_API_KEY"
```

**Verify:** the key reaches `status: active` with a real `address`. Locally
this takes ~24s for 2-of-3. If it stays `pending_dkg`, check
`dkg_ceremonies.error_message` — the ceremony records why it failed.

This exact path is covered by
`services/api-gateway/src/keys/keys.provisioning.live.spec.ts`, which can be
pointed at the deployed stack rather than localhost.

---

## 8. First real on-chain transaction

**Nothing in this platform has ever broadcast to a live chain.** Treat the
first broadcast as an experiment, on testnet, with an amount you are willing
to lose entirely.

1. Fund the address from step 7 with a small amount of testnet ETH.
2. Submit a signing request for a fraction of it.
3. Watch it through `policy → sign → broadcast → monitor`.

**Verify:** the transaction appears on a block explorer and
`MonitorTransaction` reports the configured number of confirmations. Until
you have seen this, broadcast is unproven — every layer beneath it is
verified, but the final hop is not.

---

## 9. Post-cutover checks

- [ ] Take a backup and **restore it into a scratch database**. A backup you
      have not restored is a hypothesis. (`services/backup`, `/backup/full`
      then `/restore`.)
- [ ] Confirm the standby is streaming: `SELECT pg_is_in_recovery()` is true
      on the replica and `pg_stat_replication` shows it on the primary.
- [ ] Rehearse failover **in staging**, never first in production.
      Promotion is one-way; the standby must be rebuilt afterwards. Locally
      this takes 252ms, but that number says nothing about cross-region.
- [ ] Confirm alerts actually fire — trigger one deliberately.
- [ ] Confirm audit events are being written for real signing requests.

---

## Rollback

Deployment rollback is `helm rollback openfireblocks <revision>`.

**Data is different.** Once a key has been generated, its shares exist in
Vault and its address may hold funds. Rolling back the application does not
roll those back. If you need to abandon a deployment after keys exist,
follow the key-retirement path in `docs/security/key-rotation.md` (which
includes sweeping balances off the old address) rather than deleting
anything.
