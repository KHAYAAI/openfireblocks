#!/usr/bin/env bash
#
# Proves a threshold-signed transaction produced by this platform is
# accepted and mined by a real Ethereum node.
#
# The smoke test proves the signing path produces bytes whose signature
# recovers to the right address. That is necessary and not sufficient: an
# encoding mistake -- a wrong EIP-155 v, a mis-serialised field, an
# off-by-one in what gets hashed -- still yields a signature that recovers
# correctly while being rejected by every node on the network. Only a node
# accepting the bytes and mining them settles that.
#
# Requires infrastructure/kind/geth-dev.yaml deployed and the API reachable:
#   kubectl -n openfireblocks port-forward svc/ofb-openfireblocks-api-gateway 3000:3000
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
CURL=(curl -sS --noproxy '*')

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

GPOD="$(kubectl -n "${NS}" get pod -l app=geth-dev --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}')"
[[ -n "${GPOD}" ]] || fail "geth-dev is not running; kubectl apply -f infrastructure/kind/geth-dev.yaml"

# geth's image has wget but no curl, and no shell utilities worth relying
# on, so JSON-RPC goes through wget inside the pod.
rpc() {
  local method="$1" params="$2"
  kubectl -n "${NS}" exec "${GPOD}" -- sh -c \
    "wget -qO- --post-data='{\"jsonrpc\":\"2.0\",\"method\":\"${method}\",\"params\":${params},\"id\":1}' \
     --header='Content-Type: application/json' http://127.0.0.1:8545"
}
rpc_result() { rpc "$1" "$2" | jqp 'd.get("result")'; }

CHAIN_ID_HEX="$(rpc_result eth_chainId '[]')"
CHAIN_ID=$((CHAIN_ID_HEX))
COINBASE="$(rpc eth_accounts '[]' | jqp 'd["result"][0]')"
echo "==> geth-dev chainId=${CHAIN_ID} funder=${COINBASE}"

echo "==> provisioning a 2-of-3 threshold key"
S=$(date +%s)
customer=$("${CURL[@]}" -X POST "${API}/admin/customers" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"email\":\"chain-${S}@example.com\",\"name\":\"chain-${S}\",\"tier\":\"enterprise\"}")
api_key=$(echo "${customer}" | jqp 'd["api_key"]')
created=$("${CURL[@]}" -X POST "${API}/keys" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"blockchain\":\"ethereum\",\"threshold\":2,\"total_parties\":3,\"name\":\"chain-${S}\"}")
key_id=$(echo "${created}" | jqp 'd["id"]')
for _ in $(seq 1 120); do
  key=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}")
  status=$(echo "${key}" | jqp 'd.get("status")')
  [[ "${status}" != "pending_dkg" ]] && break
  sleep 5
done
[[ "${status}" == "active" ]] || fail "key did not activate: ${key}"
FROM=$(echo "${key}" | jqp 'd["address"]')
echo "    ${FROM}"

echo "==> funding it from the dev account"
fund=$(rpc_result eth_sendTransaction \
  "[{\"from\":\"${COINBASE}\",\"to\":\"${FROM}\",\"value\":\"0xde0b6b3a7640000\"}]")
[[ "${fund}" == 0x* ]] || fail "funding transaction rejected: ${fund}"
for _ in $(seq 1 60); do
  bal=$(rpc_result eth_getBalance "[\"${FROM}\",\"latest\"]")
  [[ "${bal}" != "0x0" ]] && break
  sleep 1
done
[[ "${bal}" != "0x0" ]] || fail "funding never landed"
echo "    balance ${bal}"

echo "==> signing a transfer with the threshold key"
NONCE=$(rpc_result eth_getTransactionCount "[\"${FROM}\",\"pending\"]")
GAS_PRICE=$(rpc_result eth_gasPrice '[]')
TO=0x00000000000000000000000000000000000000aa
# Values are decimal strings on this API; the node speaks hex.
signed=$("${CURL[@]}" -X POST "${API}/keys/${key_id}/transactions" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"to\":\"${TO}\",\"value\":\"1000000000000000\",\"gasLimit\":21000,
       \"nonce\":$((NONCE)),\"chainId\":${CHAIN_ID},\"gasPrice\":\"$((GAS_PRICE))\"}")
RAW=$(echo "${signed}" | jqp 'd.get("raw_transaction","")')
[[ -n "${RAW}" ]] || fail "signing failed: ${signed}"
EXPECTED_HASH=$(echo "${signed}" | jqp 'd["transaction_hash"]')
echo "    raw ${RAW:0:40}..."

echo "==> broadcasting it to the node"
sent=$(rpc eth_sendRawTransaction "[\"${RAW}\"]")
hash=$(echo "${sent}" | jqp 'd.get("result","")')
if [[ -z "${hash}" ]]; then
  fail "node rejected the transaction: $(echo "${sent}" | jqp 'd.get("error")')"
fi
[[ "${hash}" == "${EXPECTED_HASH}" ]] \
  || fail "node computed hash ${hash}, service predicted ${EXPECTED_HASH}"
echo "    accepted as ${hash}"

echo "==> waiting for it to be mined"
for _ in $(seq 1 90); do
  receipt=$(rpc_result eth_getTransactionReceipt "[\"${hash}\"]")
  [[ "${receipt}" != "None" && -n "${receipt}" ]] && break
  sleep 1
done
[[ "${receipt}" != "None" && -n "${receipt}" ]] || fail "never mined"

status_hex=$(rpc eth_getTransactionReceipt "[\"${hash}\"]" | jqp 'd["result"]["status"]')
block_hex=$(rpc eth_getTransactionReceipt "[\"${hash}\"]" | jqp 'd["result"]["blockNumber"]')
[[ "${status_hex}" == "0x1" ]] || fail "mined but reverted (status ${status_hex})"
echo "    mined in block $((block_hex)), status success"

echo "==> confirming the recipient actually received the value"
to_bal=$(rpc_result eth_getBalance "[\"${TO}\",\"latest\"]")
[[ $((to_bal)) -eq 1000000000000000 ]] \
  || fail "recipient balance is $((to_bal)), expected 1000000000000000"

# The node's own view of who sent it, not ours.
sender=$(rpc eth_getTransactionByHash "[\"${hash}\"]" | jqp 'd["result"]["from"]')
[[ "$(echo "${sender}" | tr 'A-Z' 'a-z')" == "$(echo "${FROM}" | tr 'A-Z' 'a-z')" ]] \
  || fail "node says the sender was ${sender}, not ${FROM}"

echo
echo "PASS: a 2-of-3 threshold-signed transaction built by the gateway was"
echo "      accepted by a real Ethereum node, mined in block $((block_hex)),"
echo "      moved value, and the node attributes it to ${FROM}."
