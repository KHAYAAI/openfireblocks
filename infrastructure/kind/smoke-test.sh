#!/usr/bin/env bash
#
# Exercises the whole customer path against a deployed cluster and fails if
# any step does not do the real thing:
#
#   create a tenant -> provision a 2-of-3 threshold key (real DKG across
#   three party pods) -> confirm all three sealed a share in Vault ->
#   threshold-sign with two of them through the public API -> confirm the
#   policy gate denies an over-limit request and another tenant cannot use
#   the key -> confirm the signature recovers to the DKG-derived address.
#
# The last step is the one that matters. Everything before it can appear to
# succeed while producing a key nobody can sign with; recovering the
# signer's address from the signature is what proves the key is real and
# that the shares belong to it.
#
# Requires the API to be reachable -- run
#   kubectl -n openfireblocks port-forward svc/ofb-openfireblocks-api-gateway 3000:3000
# first, or set API.
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
# --noproxy so an HTTP(S)_PROXY set for outbound internet access does not
# capture a request to a local port-forward.
CURL=(curl -sS --noproxy '*')

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

echo "==> health"
"${CURL[@]}" "${API}/health/ready" | grep -q '"postgres":"ok"' \
  || fail "gateway is not ready"

echo "==> creating tenant"
suffix=$(date +%s)
customer=$("${CURL[@]}" -X POST "${API}/admin/customers" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"email\":\"smoke-${suffix}@example.com\",\"name\":\"smoke-${suffix}\",\"tier\":\"enterprise\"}")
api_key=$(echo "${customer}" | jqp 'd["api_key"]') || fail "no api_key: ${customer}"
echo "    customer $(echo "${customer}" | jqp 'd["customer_id"]')"

echo "==> provisioning a 2-of-3 threshold key (real DKG)"
created=$("${CURL[@]}" -X POST "${API}/keys" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"blockchain\":\"ethereum\",\"threshold\":2,\"total_parties\":3,\"name\":\"smoke-${suffix}\"}")
key_id=$(echo "${created}" | jqp 'd["id"]') || fail "key not created: ${created}"
ceremony_id=$(echo "${created}" | jqp 'd["ceremony_id"]')

# Safe-prime generation dominates and is a randomised search, so this is
# minutes, not seconds, on a cold pool.
started=$(date +%s)
for _ in $(seq 1 120); do
  key=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}")
  status=$(echo "${key}" | jqp 'd.get("status")')
  [[ "${status}" != "pending_dkg" ]] && break
  sleep 5
done
[[ "${status}" == "active" ]] || fail "key did not activate (status=${status}): ${key}"
address=$(echo "${key}" | jqp 'd["address"]')
echo "    active after $(( $(date +%s) - started ))s, address ${address}"

echo "==> confirming all three parties sealed a share in Vault"
vpod=$(kubectl -n "${NS}" get pod -l app=vault --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')
for n in 1 2 3; do
  kubectl -n "${NS}" exec "${vpod}" -- env \
    VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN=dev-root-token \
    vault kv list "secret/openfireblocks/mpc-party/party-${n}" 2>/dev/null \
    | grep -qx "${ceremony_id}" || fail "party ${n} did not seal a share for ${ceremony_id}"
  echo "    party-${n} sealed a share"
done

echo "==> threshold-signing through the API with 2 of the 3 parties"
message=$(python3 -c "import hashlib,sys;print(hashlib.sha256(b'smoke ${suffix}').hexdigest())")
signed=$("${CURL[@]}" -X POST "${API}/keys/${key_id}/sign" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"message\":\"${message}\",\"to\":\"0x1111111111111111111111111111111111111111\",\"value\":\"1000\",\"chainId\":11155111}")
signature=$(echo "${signed}" | jqp 'd.get("signature","")') || fail "sign failed: ${signed}"
[[ -n "${signature}" ]] || fail "no signature: ${signed}"
parties=$(echo "${signed}" | jqp 'd["parties"]')
# 2 of 3, not 3 of 3: signing with the whole committee would make an n-of-n
# key out of a k-of-n one.
[[ "${parties}" == "[1, 2]" ]] || fail "expected parties [1, 2], got ${parties}"
echo "    signature ${signature:0:32}... from parties ${parties}"

echo "==> policy gate denies an over-limit request"
denied_code=$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST "${API}/keys/${key_id}/sign" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"message\":\"${message}\",\"to\":\"0x1111111111111111111111111111111111111111\",\"value\":\"100000000000000000000000000\",\"chainId\":11155111}")
[[ "${denied_code}" == "403" ]] || fail "over-limit request returned ${denied_code}, expected 403"
echo "    403"

echo "==> another tenant cannot sign with this key"
other=$("${CURL[@]}" -X POST "${API}/admin/customers" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"email\":\"smoke-other-${suffix}@example.com\",\"name\":\"smoke-other-${suffix}\",\"tier\":\"pro\"}")
other_key=$(echo "${other}" | jqp 'd["api_key"]')
cross_code=$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST "${API}/keys/${key_id}/sign" \
  -H 'Content-Type: application/json' -H "x-api-key: ${other_key}" \
  -d "{\"message\":\"${message}\",\"to\":\"0x1111111111111111111111111111111111111111\",\"value\":\"1000\",\"chainId\":11155111}")
[[ "${cross_code}" == "404" ]] || fail "cross-tenant sign returned ${cross_code}, expected 404"
echo "    404 (row-level security, all the way through the stack)"

echo "==> recovering the signer from the signature"
recovered=$(cd "$(dirname "${BASH_SOURCE[0]}")/recover" && go run . "${message}" "${signature}")
echo "    ${recovered}"
echo "${recovered}" | grep -qi "${address}" \
  || fail "signature recovered to a different address than the key: ${recovered} != ${address}"

echo
echo "PASS: threshold key ${address} provisioned by real DKG across three pods,"
echo "      shares sealed by all three, policy and tenant isolation enforced,"
echo "      and a 2-of-3 signature from the public API recovers to it."
