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
# Prefers the multi-node proof-of-authority network (geth-poa.yaml), where
# the node addressed here seals nothing and the transaction has to propagate
# to the signer to be mined -- that additionally covers peer-to-peer
# propagation and real pending state, which a single --dev node cannot.
# Falls back to geth-dev.yaml.
#
# Requires a chain deployed and the API reachable:
#   kubectl -n openfireblocks port-forward svc/ofb-openfireblocks-api-gateway 3000:3000
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
CURL=(curl -sS --noproxy '*')

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

# Prefer the multi-node proof-of-authority network when it is deployed.
#
# It is the stronger target: the pod addressed here seals nothing, so a
# transaction sent to it only ever gets mined by crossing a peer-to-peer
# link to the signer. geth-dev proves encoding; this proves propagation as
# well. Falls back to geth-dev so the test still runs against either.
CHAIN="poa"
GPOD="$(kubectl -n "${NS}" get pod -l app=geth-peer --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
if [[ -z "${GPOD}" ]]; then
  CHAIN="dev"
  GPOD="$(kubectl -n "${NS}" get pod -l app=geth-dev --field-selector=status.phase=Running \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
fi
[[ -n "${GPOD}" ]] || fail "no chain is running; kubectl apply -f infrastructure/kind/geth-poa.yaml"
echo "==> chain: ${CHAIN} (${GPOD})"

# geth's image has wget but no curl, and no shell utilities worth relying
# on, so JSON-RPC goes through wget inside the pod.
rpc() {
  local method="$1" params="$2"
  kubectl -n "${NS}" exec "${GPOD}" -c geth -- sh -c \
    "wget -qO- --post-data='{\"jsonrpc\":\"2.0\",\"method\":\"${method}\",\"params\":${params},\"id\":1}' \
     --header='Content-Type: application/json' http://127.0.0.1:8545"
}
rpc_result() { rpc "$1" "$2" | jqp 'd.get("result")'; }

CHAIN_ID_HEX="$(rpc_result eth_chainId '[]')"
CHAIN_ID=$((CHAIN_ID_HEX))

if [[ "${CHAIN}" == "poa" ]]; then
  # The prefunded signer lives on the signer node, and only that node holds
  # its key -- this peer has no accounts at all, which is the property that
  # makes it a useful target.
  SPOD="$(kubectl -n "${NS}" get pod -l app=geth-signer --field-selector=status.phase=Running \
    -o jsonpath='{.items[0].metadata.name}')"
  [[ -n "${SPOD}" ]] || fail "the proof-of-authority signer is not running"
  signer_rpc() {
    kubectl -n "${NS}" exec "${SPOD}" -c geth -- sh -c \
      "wget -qO- --post-data='{\"jsonrpc\":\"2.0\",\"method\":\"$1\",\"params\":$2,\"id\":1}' \
       --header='Content-Type: application/json' http://127.0.0.1:8545"
  }
  COINBASE="$(signer_rpc eth_accounts '[]' | jqp 'd["result"][0]')"

  echo "==> waiting for the peer to link to the signer and sync"
  for _ in $(seq 1 90); do
    peers=$(rpc_result net_peerCount '[]' 2>/dev/null || echo 0x0)
    [[ "${peers}" != "0x0" && -n "${peers}" ]] && break
    sleep 2
  done
  [[ "${peers}" != "0x0" ]] && echo "    peers: $((peers))" \
    || fail "the peer never connected to the signer; nothing would propagate"
  # A peer that is connected but still at genesis would accept a
  # transaction and never see it mined.
  for _ in $(seq 1 90); do
    bn=$(rpc_result eth_blockNumber '[]')
    [[ $((bn)) -gt 0 ]] && break
    sleep 2
  done
  [[ $((bn)) -gt 0 ]] || fail "the peer is connected but has no blocks"
  echo "    peer is at block $((bn)), signer is sealing"
else
  COINBASE="$(rpc eth_accounts '[]' | jqp 'd["result"][0]')"
  signer_rpc() { rpc "$@"; }
fi
echo "==> chainId=${CHAIN_ID} funder=${COINBASE}"

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

echo "==> funding it from the prefunded account"
# Submitted on the signer, which is the only node holding that key.
fund=$(signer_rpc eth_sendTransaction \
  "[{\"from\":\"${COINBASE}\",\"to\":\"${FROM}\",\"value\":\"0xde0b6b3a7640000\"}]" | jqp 'd.get("result","")')
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

echo "==> broadcasting it to a node that cannot mine it"
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
if [[ "${CHAIN}" == "poa" ]]; then
  echo "PASS: a 2-of-3 threshold-signed transaction built by the gateway was"
  echo "      accepted by a node that seals nothing, propagated over the"
  echo "      peer-to-peer link, was mined by the signer in block $((block_hex)),"
  echo "      moved value, and is attributed by the receiving node to ${FROM}."
else
  echo "PASS: a 2-of-3 threshold-signed transaction built by the gateway was"
  echo "      accepted by a real Ethereum node, mined in block $((block_hex)),"
  echo "      moved value, and the node attributes it to ${FROM}."
fi
