#!/usr/bin/env bash
# One-time: initialise this party's Vault and give the party a token.
#
# Run ON the party host, by that party's operator, through SSM:
#
#   aws ssm start-session --target <instance_id>
#   sudo bash party-bootstrap.sh <vault_token_secret_arn> <recovery-pgp-keys>
#
# Vault auto-unseals with this account's KMS key, so there are no unseal
# keys to hand out. `vault operator init` still produces *recovery* keys --
# needed to generate a new root token or to migrate the seal -- and those
# are encrypted to the PGP keys given here, so they are never printed in
# the clear on a shared terminal. Keep them offline, per party, held by
# people who do not hold another party's.
#
# The root token is used once, below, and revoked.
set -euo pipefail

SECRET_ARN="${1:?usage: party-bootstrap.sh <vault_token_secret_arn> <comma-separated recovery PGP key files>}"
PGP_KEYS="${2:?recovery PGP public key files, comma-separated (one per recovery key holder)}"
SHARES=$(tr ',' '\n' <<<"$PGP_KEYS" | sed '/^$/d' | wc -l)
THRESHOLD=$(( SHARES / 2 + 1 ))
REGION=$(curl -s -H "X-aws-ec2-metadata-token: $(curl -s -X PUT http://169.254.169.254/latest/api/token \
  -H 'X-aws-ec2-metadata-token-ttl-seconds: 60')" http://169.254.169.254/latest/meta-data/placement/region)

vault() { docker exec -e VAULT_ADDR=http://127.0.0.1:8200 -e VAULT_TOKEN="${VAULT_TOKEN:-}" vault vault "$@"; }

if vault status -format=json | jq -e '.initialized' >/dev/null; then
  echo "Vault is already initialised. This script only runs once per party." >&2
  exit 1
fi

# PGP key files are read inside the container, so copy them in.
IN_CONTAINER=()
IFS=',' read -r -a FILES <<<"$PGP_KEYS"
for f in "${FILES[@]}"; do
  docker cp "$f" "vault:/tmp/$(basename "$f")"
  IN_CONTAINER+=("/tmp/$(basename "$f")")
done
JOINED=$(IFS=','; echo "${IN_CONTAINER[*]}")

INIT=$(vault operator init -format=json -recovery-shares="$SHARES" -recovery-threshold="$THRESHOLD" \
  -recovery-pgp-keys="$JOINED")
echo "$INIT" | jq '{recovery_keys_b64, recovery_keys_shares, recovery_keys_threshold}' > /root/vault-recovery-keys.pgp.json
chmod 600 /root/vault-recovery-keys.pgp.json
echo "Recovery keys (PGP-encrypted) written to /root/vault-recovery-keys.pgp.json -- copy them off this host and delete the file."

VAULT_TOKEN=$(echo "$INIT" | jq -r .root_token)
export VAULT_TOKEN
for _ in $(seq 1 30); do vault status >/dev/null 2>&1 && break; sleep 2; done

vault secrets enable -path=secret kv-v2
# The party may read and write its own shares and nothing else.
vault policy write mpc-party - <<'POLICY'
path "secret/data/openfireblocks/mpc-party/*"     { capabilities = ["create", "read", "update"] }
path "secret/metadata/openfireblocks/mpc-party/*" { capabilities = ["read", "list"] }
POLICY
PARTY_TOKEN=$(vault token create -policy=mpc-party -period=768h -orphan -format=json | jq -r .auth.client_token)

aws secretsmanager put-secret-value --region "$REGION" --secret-id "$SECRET_ARN" --secret-string "$PARTY_TOKEN" >/dev/null
vault token revoke -self
unset VAULT_TOKEN PARTY_TOKEN INIT

systemctl restart ofb-party.service
echo "Party token stored; the party is starting. Check: systemctl status ofb-party; curl -s localhost:7001/health"
