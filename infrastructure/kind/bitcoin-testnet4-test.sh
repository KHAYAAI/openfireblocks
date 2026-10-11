#!/usr/bin/env bash
#
# Public-network proof for Bitcoin: a 2-of-3 threshold key signs a spend through the platform's API and
# the real Bitcoin testnet4 network mines it.
#
# Until now Bitcoin was only driven against a private regtest node. Solana, Cosmos and Ethereum have
# public-network evidence; this is the same for Bitcoin.
#
# Prerequisites (on the server, in this order):
#   1. ./infrastructure/kind/bitcoin-testnet4-setup.sh          # starts the node, points the platform at it
#   2. wait until the node has synced:  ./infrastructure/kind/bitcoin-testnet4-setup.sh status
#   3. set -a; source ~/ofb-secrets.txt; set +a
#   4. ./infrastructure/kind/bitcoin-testnet4-test.sh
#
# It creates a throwaway tenant and two 2-of-3 Bitcoin keys (a sender, and a receiver unless DEST is set),
# prints the sender's testnet4 address (tb1...), waits for you to fund it from a faucet, sends, and waits
# for the network to mine the transaction. Funds are found by asking the node (scantxoutset), so this
# also checks what the platform itself will see. Ctrl-C and re-run is safe: state is in ~/.btc-testnet4-state.
#
# Optional: DEST (a testnet4 address), AMOUNT (sats, default 10000), NEED (sats, default 20000),
# FEE_RATE (sat/vB, default 2), TRAVEL_RULE (JSON).
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
: "${ADMIN_API_KEY:?export ADMIN_API_KEY first (source ~/ofb-secrets.txt with set -a)}"
STATE="${HOME}/.btc-testnet4-state"
EVIDENCE="${HOME}/btc-testnet4-evidence.txt"
AMOUNT="${AMOUNT:-10000}"
NEED="${NEED:-20000}"
FEE_RATE="${FEE_RATE:-2}"
if [[ -z "${TRAVEL_RULE:-}" ]]; then
  TRAVEL_RULE='{"originator":{"legalPerson":{"name":"FORGE CUSTODY testnet treasury","customerIdentification":"forge-testnet-001"}},"beneficiary":{"legalPerson":{"name":"Testnet recipient"}},"beneficiaryUnhosted":true}'
fi

fail() { echo "FAIL: $*" >&2; exit 1; }
api() { curl -sS --noproxy '*' "$@"; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }
cli() { kubectl -n "${NS}" exec deploy/bitcoind-testnet4 -- sh -c 'bitcoin-cli -testnet4 -rpcuser=ofb -rpcpassword="$RPC_PASSWORD" "$@"' sh "$@"; }

# Confirmed satoshis at an address, as the node sees them.
confirmed_sats() {
  cli scantxoutset start "[\"addr($1)\"]" 2>/dev/null \
    | python3 -c "import sys,json;d=json.load(sys.stdin);print(round(d.get('total_amount',0)*100000000) if d.get('success') else 0)" \
    2>/dev/null || echo 0
}

echo "==> health"
api "${API}/health/ready" | grep -q '"postgres":"ok"' || fail "gateway is not ready"

echo "==> checking the testnet4 node"
info=$(cli getblockchaininfo 2>/dev/null) || fail "no testnet4 node; run bitcoin-testnet4-setup.sh first"
[[ "$(echo "${info}" | jqp 'd["chain"]')" == "testnet4" ]] || fail "the node is not on testnet4: $(echo "${info}" | jqp 'd["chain"]')"
if [[ "$(echo "${info}" | jqp 'd["initialblockdownload"]')" != "False" ]]; then
  fail "the node is still syncing ($(echo "${info}" | jqp "'%s/%s' % (d['blocks'], d['headers'])")); wait for 'status' to show initial_download False"
fi
echo "    synced: block $(echo "${info}" | jqp 'd["blocks"]'), $(cli getconnectioncount) peers"

make_key() {
  local created
  created=$(api -X POST "${API}/keys" -H 'Content-Type: application/json' -H "x-api-key: ${API_KEY}" \
    -d "{\"blockchain\":\"bitcoin\",\"threshold\":2,\"total_parties\":3,\"name\":\"$1\"}")
  echo "${created}" | jqp 'd["id"]' || fail "key not created: ${created}"
}
wait_active() {
  local key status
  for _ in $(seq 1 120); do
    key=$(api -H "x-api-key: ${API_KEY}" "${API}/keys/$1")
    status=$(echo "${key}" | jqp 'd.get("status")')
    [[ "${status}" != "pending_dkg" ]] && break
    sleep 5
  done
  [[ "${status}" == "active" ]] || fail "key $1 did not activate (status=${status})"
}
addr_of() { api -H "x-api-key: ${API_KEY}" "${API}/keys/$1/addresses" | jqp 'd["addresses"]["preferred"]'; }

if [[ -f "${STATE}" ]]; then
  # shellcheck disable=SC1090
  source "${STATE}"
  echo "==> reusing keys from ${STATE}"
else
  echo "==> creating a tenant and the sending key (real DKG)"
  suffix=$(date +%s)
  customer=$(api -X POST "${API}/admin/customers" -H 'Content-Type: application/json' \
    -H "x-admin-key: ${ADMIN_API_KEY}" \
    -d "{\"email\":\"btc4-${suffix}@example.com\",\"name\":\"btc4-${suffix}\",\"tier\":\"enterprise\"}")
  API_KEY=$(echo "${customer}" | jqp 'd["api_key"]') || fail "no api_key: ${customer}"
  KEY_ID=$(make_key "btc4-send-${suffix}")
  DEST_KEY_ID=""
  umask 077
  printf 'API_KEY=%s\nKEY_ID=%s\nDEST_KEY_ID=%s\n' "${API_KEY}" "${KEY_ID}" "${DEST_KEY_ID}" > "${STATE}"
fi
wait_active "${KEY_ID}"
ADDRESS=$(addr_of "${KEY_ID}")
[[ "${ADDRESS}" == tb1* ]] || fail "the sender address is ${ADDRESS}, not a testnet address; the platform is not on testnet (run bitcoin-testnet4-setup.sh)"

if [[ -z "${DEST:-}" ]]; then
  if [[ -z "${DEST_KEY_ID:-}" ]]; then
    echo "==> provisioning a second key to receive the transfer"
    DEST_KEY_ID=$(make_key "btc4-recv-$(date +%s)")
    sed -i "s/^DEST_KEY_ID=.*/DEST_KEY_ID=${DEST_KEY_ID}/" "${STATE}"
  fi
  wait_active "${DEST_KEY_ID}"
  DEST=$(addr_of "${DEST_KEY_ID}")
fi
dest_before=$(confirmed_sats "${DEST}")

echo
echo "    The sending key's testnet4 address (fund this from a faucet, about 0.0002 tBTC is plenty):"
echo "        ${ADDRESS}"
echo "    Recipient:"
echo "        ${DEST}"
echo

echo "==> waiting for at least ${NEED} sats, confirmed on testnet4 (checked on the node)"
bal=0
for i in $(seq 1 120); do
  bal=$(confirmed_sats "${ADDRESS}")
  (( bal >= NEED )) && break
  (( i % 3 == 1 )) && echo "    confirmed balance ${bal} ... still waiting; fund ${ADDRESS}"
  sleep 30
done
(( bal >= NEED )) || fail "no confirmed funds within 60 minutes; fund ${ADDRESS} and re-run (the keys are kept)"
echo "    funded: ${bal} sats"

echo "==> signing ${AMOUNT} sats to ${DEST}: the platform picks the coins, sets the fee, two of three parties sign, it broadcasts"
sent=$(api -X POST "${API}/keys/${KEY_ID}/bitcoin-transactions" -H 'Content-Type: application/json' \
  -H "x-api-key: ${API_KEY}" \
  -d "{\"destination\":\"${DEST}\",\"amount\":\"${AMOUNT}\",\"feeRate\":${FEE_RATE},\"travelRule\":${TRAVEL_RULE}}")
txid=$(echo "${sent}" | jqp 'd["txid"]') || fail "send failed: ${sent}"
[[ "$(echo "${sent}" | jqp 'd.get("broadcast")')" == "True" ]] || fail "signed but not broadcast: ${sent}"
parties=$(echo "${sent}" | jqp 'd.get("parties")')
fee=$(echo "${sent}" | jqp 'd["fee"]')
echo "    signed by parties ${parties}; fee ${fee} sats; txid ${txid}"

echo "==> the node's view: is it in the mempool"
cli getmempoolentry "${txid}" >/dev/null 2>&1 || echo "    (not in this node's mempool; it may already be mined)"

echo "==> waiting for testnet4 to mine it (blocks on testnet4 can be slow and irregular; up to 90 minutes)"
dest_after=0
for _ in $(seq 1 180); do
  dest_after=$(confirmed_sats "${DEST}")
  (( dest_after - dest_before >= AMOUNT )) && break
  sleep 30
done
(( dest_after - dest_before >= AMOUNT )) || fail "not mined within 90 minutes: https://mempool.space/testnet4/tx/${txid}"

tip=$(cli getblockchaininfo | jqp "'%s %s' % (d['blocks'], d['bestblockhash'])")
{
  echo "Bitcoin testnet4 acceptance of a threshold-signed transaction"
  echo "date:        $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "network:     Bitcoin testnet4 (node-confirmed; tip ${tip})"
  echo "from:        ${ADDRESS} (2-of-3 threshold ECDSA key)"
  echo "to:          ${DEST}"
  echo "amount:      ${AMOUNT} sats; fee ${fee} sats at ${FEE_RATE} sat/vB"
  echo "signed by:   parties ${parties}"
  echo "txid:        ${txid}"
  echo "recipient confirmed balance: ${dest_before} -> ${dest_after} sats"
  echo "explorer:    https://mempool.space/testnet4/tx/${txid}"
} | tee "${EVIDENCE}"
echo
echo "PASS: Bitcoin testnet4 mined a transaction signed by the platform's 2-of-3 key; the recipient's confirmed balance rose."
