# Disaster-recovery rebuild runbook (Part B of the DR test)

Rebuild the platform on a **fresh server** from the **off-site backup alone**, restore a key, sign with it, and
time the whole thing. This is step 6 of `docs/compliance/isms/exercises/DR-TEST.md`. Part A (restore into scratch
copies on the live cluster) is recorded in `docs/evidence/2026-10-17-dr-offsite-restore-scratch.md`.

**Test keys only.** The key shares are decrypted onto the new server. That is acceptable on staging, never on
production hardware.

## What counts as "off-site"

The old server is treated as lost. The new server may use only what a real operator would still hold:

| Item | Where it lives |
|---|---|
| The backup encryption key (64 hex characters) | Password manager, and a second copy kept apart |
| The backup writer's AWS access key | Password manager |
| The bucket name and region | This repository's evidence, the AWS console |
| The **checksums** (`tags`) of the backup being fetched | **A file saved off the server** (see the finding below) |
| The source code | GitHub |
| A test tenant's API key and key ids | `dr-proof.env`, copied off the server |

The old server stays running only so its numbers can be compared; nothing is read from it by the new server
except the files copied through your laptop.

## Finding to confirm: the checksums are only on the cluster

`POST /offsite/fetch` refuses to fetch anything without the SHA-256 of all three files, and for a nightly backup
those values are recorded only in the backup volume **inside the cluster** (`services/backup/backup_manager.go`,
`FetchOffsite`). If the whole cluster is lost, the bucket's contents can't be fetched through the service without
a copy of those checksums made earlier. This runbook uses a manual backup whose checksums you save off-server. Record
the gap in the evidence and fix it (for example by keeping the checksums beside the objects, or by saving them off
the cluster after every run).

## Phase 1: prepare, on the OLD server (about 20 minutes)

Run inside tmux. Everything stays in shell variables or files with mode 600; never paste any of it.

```bash
set -a; source ~/ofb-secrets.txt; set +a
API=http://127.0.0.1:3000
cd ~/openfireblocks && git rev-parse HEAD | tee ~/dr-commit.txt
```

A test tenant with an Ethereum key (the drill signs a raw digest and recovers an Ethereum address):

```bash
S=$(date +%s)
CUST=$(curl -sS -X POST $API/admin/customers -H 'Content-Type: application/json' -H "x-admin-key: $ADMIN_API_KEY" \
  -d "{\"email\":\"dr-rebuild-$S@example.com\",\"name\":\"dr-rebuild-$S\",\"tier\":\"enterprise\"}")
DR_API_KEY=$(echo "$CUST" | jq -r .api_key); DR_CUST=$(echo "$CUST" | jq -r .customer_id)
KEY=$(curl -sS -X POST $API/keys -H 'Content-Type: application/json' -H "x-api-key: $DR_API_KEY" \
  -d "{\"blockchain\":\"ethereum\",\"threshold\":2,\"total_parties\":3,\"name\":\"dr-rebuild-$S\"}")
DR_KEY_ID=$(echo "$KEY" | jq -r .id); DR_CEREMONY=$(echo "$KEY" | jq -r .ceremony_id)
echo "$DR_KEY_ID $DR_CEREMONY"
```

Key generation takes minutes. Wait until it is `active`:

```bash
until [ "$(curl -sS -H "x-api-key: $DR_API_KEY" $API/keys/$DR_KEY_ID | jq -r .status)" != "pending_dkg" ]; do sleep 5; done
curl -sS -H "x-api-key: $DR_API_KEY" $API/keys/$DR_KEY_ID | jq '{status, address}'
curl -sS -o /dev/null -w '%{http_code}\n' -X PUT $API/admin/customers/$DR_CUST/raw-digest-signing \
  -H 'Content-Type: application/json' -H "x-admin-key: $ADMIN_API_KEY" -d '{"enabled":true}'
```

You want `active`, an address, and `200`. Save the proof file (keep mode 600):

```bash
DR_ADDRESS=$(curl -sS -H "x-api-key: $DR_API_KEY" $API/keys/$DR_KEY_ID | jq -r .address)
( umask 077; printf 'DR_API_KEY=%s\nDR_KEY_ID=%s\nDR_CEREMONY=%s\nDR_ADDRESS=%s\n' "$DR_API_KEY" "$DR_KEY_ID" "$DR_CEREMONY" "$DR_ADDRESS" > ~/dr-proof.env )
```

Take the backup (this is the "disaster" backup), and save its checksums:

```bash
kubectl -n openfireblocks port-forward svc/ofb-openfireblocks-backup 8088:8088 >/dev/null 2>&1 &
sleep 3; TOKEN=$(kubectl -n openfireblocks get secret backup-api -o jsonpath='{.data.backup-api-token}' | base64 -d)
curl -sS -m 900 -X POST -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8088/backup/full > ~/dr-backup-B.json
jq '{id, status}' ~/dr-backup-B.json; unset TOKEN; kill %1
```

You want `completed`. Record the row counts at backup time, then create a marker that is **not** in the backup:

```bash
for db in openfireblocks; do kubectl -n openfireblocks exec deploy/postgres -- psql -U postgres -d $db -At -c "SELECT 'customers', count(*) FROM customers UNION ALL SELECT 'key_pairs', count(*) FROM key_pairs UNION ALL SELECT 'user_wallets', count(*) FROM user_wallets UNION ALL SELECT 'travel_rule_records', count(*) FROM travel_rule_records UNION ALL SELECT 'retention_purges', count(*) FROM retention_purges UNION ALL SELECT 'tokens', count(*) FROM tokens UNION ALL SELECT 'triggers', count(*) FROM pg_trigger WHERE NOT tgisinternal UNION ALL SELECT 'rls_policies', count(*) FROM pg_policies"; done | tee ~/dr-counts-old.txt
curl -s -o /dev/null -X POST $API/admin/customers -H 'Content-Type: application/json' -H "x-admin-key: $ADMIN_API_KEY" -d "{\"email\":\"dr-after-$(date +%s)@example.com\",\"name\":\"dr-after-$(date +%s)\",\"tier\":\"enterprise\"}"
unset DR_API_KEY ADMIN_API_KEY JWT_SECRET
```

Copy five files to your laptop (run on the **laptop**, not the server):

```bash
mkdir -p ~/dr-transfer && chmod 700 ~/dr-transfer
for f in dr-backup-B.json dr-proof.env dr-counts-old.txt dr-commit.txt reapply.sh; do scp -i ~/Downloads/ofb-staging-key.pem ubuntu@52.213.16.179:$f ~/dr-transfer/; done
ls -la ~/dr-transfer
```

## Phase 2: the disaster begins (AWS console)

**Start the clock now** (write the time down; this is the start of the recovery time).

EC2 → Launch instance: Ubuntu Server 24.04 LTS x86_64, `m6i.2xlarge`, 100 GiB gp3, the existing `ofb-staging`
security group and key pair. No Elastic IP is needed. Copy the new public IP.

## Phase 3: tools, code and the platform, on the NEW server (about 45 minutes)

From the laptop:

```bash
NEW=<new public ip>
scp -i ~/Downloads/ofb-staging-key.pem ~/dr-transfer/* ubuntu@$NEW:
ssh -i ~/Downloads/ofb-staging-key.pem ubuntu@$NEW
```

On the new server:

```bash
sudo apt update && sudo apt install -y docker.io docker-buildx git curl jq tmux && sudo usermod -aG docker $USER
exit
```

Reconnect, then:

```bash
curl -Lo kind https://kind.sigs.k8s.io/dl/v0.24.0/kind-linux-amd64 && sudo install kind /usr/local/bin/kind
curl -LO "https://dl.k8s.io/release/v1.29.14/bin/linux/amd64/kubectl" && sudo install kubectl /usr/local/bin/kubectl
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
sudo snap install go --classic
tmux new -s work
```

```bash
git clone https://github.com/KHAYAAI/openfireblocks && cd openfireblocks
git checkout claude/platform-explanation-h0st5y && git checkout "$(cat ~/dr-commit.txt)"
umask 077
export ADMIN_API_KEY=$(openssl rand -hex 24)
export JWT_SECRET=$(openssl rand -hex 32)
printf 'ADMIN_API_KEY=%s\nJWT_SECRET=%s\n' "$ADMIN_API_KEY" "$JWT_SECRET" > ~/ofb-secrets.txt
infrastructure/kind/up.sh 2>&1 | tee ~/up.log
```

The new admin key and JWT secret are **new**: they are not in the backup, and tenants' API keys do not depend on them.
`up.sh` takes 15 to 30 minutes because it builds the images; a production rebuild would pull the signed images
from the registry instead, so this overstates that part of the recovery time.

## Phase 4: give the new cluster the backup settings (10 minutes)

The three secrets must exist before the settings are applied. Hidden prompts keep them out of history:

```bash
read -rsp "Backup encryption key (64 hex): " BK; echo
printf '%s' "$BK" | kubectl -n openfireblocks create secret generic backup-offsite --from-file=backup-offsite-key=/dev/stdin; unset BK
read -rsp "Access key id: " AKID; echo
read -rsp "Secret access key: " SAK; echo
kubectl -n openfireblocks create secret generic backup-offsite-creds --from-literal=aws-access-key-id="$AKID" --from-literal=aws-secret-access-key="$SAK"; unset AKID SAK
kubectl -n openfireblocks create secret generic backup-api --from-literal=backup-api-token="$(openssl rand -hex 32)"
```

```bash
~/reapply.sh
kubectl -n openfireblocks get pods | grep backup
kubectl -n openfireblocks port-forward svc/ofb-openfireblocks-api-gateway 3000:3000 >/dev/null 2>&1 &
```

`reapply.sh` is the one copied from the old server. The backup pod should be `1/1 Running`. **Do not run the smoke
test or create any key yet:** the cluster must hold only what comes from the backup.

## Phase 5: fetch, restore the database, restore Vault (20 minutes)

```bash
kubectl -n openfireblocks port-forward svc/ofb-openfireblocks-backup 8088:8088 >/dev/null 2>&1 &
sleep 3; TOKEN=$(kubectl -n openfireblocks get secret backup-api -o jsonpath='{.data.backup-api-token}' | base64 -d)
jq '{backup_id: .id, tags: .tags}' ~/dr-backup-B.json | curl -sS -m 600 -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d @- http://127.0.0.1:8088/offsite/fetch | jq
unset TOKEN
```

You want `"status": "fetched"`. This is the proof that the encryption key, the bucket login and the checksums were enough on their own.

```bash
BP=$(kubectl -n openfireblocks get pod -l app.kubernetes.io/component=backup -o jsonpath='{.items[0].metadata.name}'); BID=$(jq -r .id ~/dr-backup-B.json)
mkdir -p ~/dr && chmod 700 ~/dr
for f in postgres vault manifest; do kubectl -n openfireblocks cp $BP:/var/lib/openfireblocks-backup/offsite-restore/$BID/$f ~/dr/$f; done; ls -la ~/dr
```

**Database.** Restore as the `app` role, not as `postgres`: migration 011 forces row-level security on the table
owner too, and `app` only reads the tables if it owns them, so a restore by another role would leave the
gateway unable to read anything.

```bash
APPPW=$(kubectl -n openfireblocks get secret dev-dependency-credentials -o jsonpath='{.data.app-password}' | base64 -d)
kubectl -n openfireblocks exec -i deploy/postgres -- env PGPASSWORD="$APPPW" pg_restore -h 127.0.0.1 -U app -d openfireblocks --clean --if-exists --no-owner --no-privileges < ~/dr/postgres 2>&1 | tail -20; unset APPPW
```

Warnings about roles or privileges are expected. An error naming a table or a failed `COPY` is not. Then compare with
the counts saved from the old server (they should be identical):

```bash
kubectl -n openfireblocks exec deploy/postgres -- psql -U postgres -d openfireblocks -At -c "SELECT 'customers', count(*) FROM customers UNION ALL SELECT 'key_pairs', count(*) FROM key_pairs UNION ALL SELECT 'user_wallets', count(*) FROM user_wallets UNION ALL SELECT 'travel_rule_records', count(*) FROM travel_rule_records UNION ALL SELECT 'retention_purges', count(*) FROM retention_purges UNION ALL SELECT 'tokens', count(*) FROM tokens UNION ALL SELECT 'triggers', count(*) FROM pg_trigger WHERE NOT tgisinternal UNION ALL SELECT 'rls_policies', count(*) FROM pg_policies" > ~/dr-counts-new.txt; diff ~/dr-counts-old.txt ~/dr-counts-new.txt && echo COUNTS-IDENTICAL
kubectl -n openfireblocks exec deploy/postgres -- psql -U postgres -d openfireblocks -At -c "SELECT count(*) FROM customers WHERE name LIKE 'dr-after-%'"
```

The last line should be `0` (the marker created after the backup is lost, which is the data-loss window).

**Vault.** Write every backed-up key share into the new Vault and check each one:

```bash
cat > ~/dr-vault-restore.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
FILE="$HOME/dr/vault"
VT=$(kubectl -n openfireblocks get secret openfireblocks-secrets -o jsonpath='{.data.vault-token}' | base64 -d)
kubectl -n openfireblocks port-forward svc/vault 8200:8200 >/dev/null 2>&1 &
PF=$!
trap 'kill $PF 2>/dev/null || true' EXIT
sleep 3
V=http://127.0.0.1:8200
H="X-Vault-Token: $VT"
echo "paths in the backup: $(jq '.paths | length' "$FILE")"
ok=0; bad=0
for p in $(jq -r '.paths | keys[]' "$FILE"); do
  jq -c --arg p "$p" '{data: .paths[$p]}' "$FILE" | curl -s -o /dev/null -X POST -H "$H" -d @- "$V/v1/secret/data/$p"
  a=$(jq -cS --arg p "$p" '.paths[$p]' "$FILE" | sha256sum | cut -d' ' -f1)
  b=$(curl -s -H "$H" "$V/v1/secret/data/$p" | jq -cS '.data.data' | sha256sum | cut -d' ' -f1)
  if [ "$a" = "$b" ]; then ok=$((ok+1)); else bad=$((bad+1)); echo "DIFFERENT: $p"; fi
done
echo "restored and verified: $ok   different: $bad"
EOF
chmod +x ~/dr-vault-restore.sh && ~/dr-vault-restore.sh
```

You want `different: 0` and the verified count equal to the paths in the backup.

Restart what holds database connections, then check the gateway sees the restored data:

```bash
kubectl -n openfireblocks rollout restart deployment api-gateway webhooks temporal-worker
kubectl -n openfireblocks rollout status deployment api-gateway
sudo systemctl restart ofb-forward 2>/dev/null || { pkill -f 'port-forward svc/ofb-openfireblocks-api-gateway' ; kubectl -n openfireblocks port-forward svc/ofb-openfireblocks-api-gateway 3000:3000 >/dev/null 2>&1 & }
curl -sS http://127.0.0.1:3000/health/ready
```

## Phase 6: stand the parties back up and sign (10 minutes)

```bash
set -a; source ~/ofb-secrets.txt; source ~/dr-proof.env; set +a
curl -sS -H "x-api-key: $DR_API_KEY" http://127.0.0.1:3000/keys/$DR_KEY_ID | jq '{status, address}'; echo "old address: $DR_ADDRESS"
```

The two addresses must be identical. This also shows a tenant API key from the old cluster working on the new one,
because only its hash was stored. The checkout on the new server is at the old server's commit, which may predate the `--restore-only` option and two
fixes to the drill (a client certificate when the worker has no `cat`, and full peer names). Take the current script from
the branch first, then run it:

```bash
git fetch -q origin claude/platform-explanation-h0st5y
git show origin/claude/platform-explanation-h0st5y:infrastructure/kind/recovery-drill.sh > infrastructure/kind/recovery-drill-restore.sh && chmod +x infrastructure/kind/recovery-drill-restore.sh
```

Now the restore, using the drill's restore half
(`--restore-only`, which is KEY-RECOVERY.md Route A):

```bash
cd ~/openfireblocks
ADMIN_KEY="$ADMIN_API_KEY" ./infrastructure/kind/recovery-drill-restore.sh --restore-only \
  --ceremony-id "$DR_CEREMONY" --key-id "$DR_KEY_ID" --api-key "$DR_API_KEY" 2>&1 | tee ~/dr-restore-drill.log
```

It must print, in order: the shares found in Vault with their
ceremony context; every party answering 404 for the ceremony; **signing failing**; each party restored at the original
address; a signature that recovers to the original address; and `PASS`. **Stop the clock when it prints `PASS`.**

Write down: the time from Phase 2 to `PASS` (the **recovery time**), and the age of the backup at the time of the
"disaster" (the **data-loss window**, bounded by the backup interval).

## Phase 7: clean up

Copy `~/dr-restore-drill.log`, `~/up.log` and `~/dr-counts-new.txt` to your laptop, then **terminate the new
instance** in the console (this destroys the decrypted shares on its disk). On the laptop and the old server:

```bash
shred -u ~/dr-transfer/dr-proof.env; rm -rf ~/dr-transfer          # laptop
shred -u ~/dr-proof.env; rm -f ~/dr-backup-B.json ~/dr-counts-old.txt ~/dr-commit.txt   # old server
```

The test tenant can stay; it holds no funds.

## What this does not prove

- Only **one** key was restored. A rebuilt platform needs every active ceremony restored on every party
  (`POST /tss/keygen/restore` per ceremony, with an authorisation signature when the ceremony authoriser is on).
  There is no single command for all keys yet. Count them: `SELECT count(*) FROM key_pairs WHERE status = 'active'`.
- The authorising key (`ceremonyAuthorizer`) on this staging profile is the development key in
  `infrastructure/kind/dependencies.yaml`, so the new cluster had it automatically. In production it is held
  outside the cluster and must be one of the things you can still reach in a disaster.
- The Vault tree restored is the key-share KV export, not a Raft snapshot. Policies, auth methods and the PKI
  mounts are not in the backup; the new cluster built its own.
- Images were rebuilt from source. A production rebuild would pull the signed images from the registry.

## Measured result

First run, 2026-10-18: 41 minutes 42 seconds from launching the instance to a restored signature, including the faults
found. See `docs/evidence/2026-10-18-dr-rebuild-fresh-server.md`.
