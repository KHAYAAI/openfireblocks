#!/usr/bin/env bash
#
# Public-testnet proof for EVM: a 2-of-3 threshold key signs an ETH transfer
# through the platform's API, and Sepolia (a real public network) accepts it.
#
# Why this exists: the platform's EVM and Bitcoin paths have only ever been driven
# against private test networks. A transaction a public network has mined is the
# first evidence that the signature and wire format are accepted by a real chain.
#
# Run it ON THE SERVER (where the gateway is reachable on :3000), with:
#
#   set -a; source ~/ofb-secrets.txt; set +a        # exports ADMIN_API_KEY
#   export SEPOLIA_RPC='https://...'                 # your Sepolia RPC URL (a secret if it has a key)
#   infrastructure/kind/sepolia-test.sh
#
# First run: creates a throwaway tenant and a 2-of-3 key, prints the key's address and
# waits for you to fund it from a Sepolia faucet (it polls; Ctrl-C and re-run is safe --
# state is kept in ~/.sepolia-test-state, so the same key is reused and no second DKG runs).
# Then it builds a transfer, signs it with two of three parties, broadcasts it and waits for
# the receipt. Evidence goes to ~/sepolia-evidence.txt (the RPC URL is never written).
#
# Optional: DEST (recipient, default: a burn address), AMOUNT_WEI (default 0.0001 ETH).
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
: "${ADMIN_API_KEY:?export ADMIN_API_KEY first (source ~/ofb-secrets.txt with set -a)}"
: "${SEPOLIA_RPC:?export SEPOLIA_RPC=<your Sepolia RPC URL> first}"
DEST="${DEST:-0x000000000000000000000000000000000000dEaD}"
AMOUNT_WEI="${AMOUNT_WEI:-100000000000000}"
CHAIN_ID=11155111
STATE="${HOME}/.sepolia-test-state"
EVIDENCE="${HOME}/sepolia-evidence.txt"
GAS_LIMIT=21000

fail() { echo "FAIL: $*" >&2; exit 1; }
api() { curl -sS --noproxy '*' "$@"; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

# JSON-RPC to Sepolia. Prints the "result" field; fails with the error otherwise.
rpc() {
  local method="$1" params="${2:-[]}" out
  out=$(curl -sS -m 30 -H 'Content-Type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"${method}\",\"params\":${params}}" "${SEPOLIA_RPC}") \
    || fail "could not reach the Sepolia RPC"
  python3 - "$out" "$method" <<'PY'
import sys, json
raw, method = sys.argv[1], sys.argv[2]
try:
    d = json.loads(raw)
except Exception:
    sys.exit(f"FAIL: {method}: the RPC did not return JSON")
if d.get("error"):
    sys.exit(f"FAIL: {method}: {d['error']}")
r = d.get("result")
print(json.dumps(r) if not isinstance(r, str) else r)
PY
}
hex2int() { python3 -c "print(int('$1', 16))"; }

echo "==> checking the Sepolia RPC"
chain=$(rpc eth_chainId)
[[ "$(hex2int "$chain")" == "${CHAIN_ID}" ]] || fail "the RPC is chain $(hex2int "$chain"), not Sepolia (${CHAIN_ID})"
echo "    chain id ${CHAIN_ID}"

echo "==> health"
api "${API}/health/ready" | grep -q '"postgres":"ok"' || fail "gateway is not ready"

# One tenant and one key, reused across runs so funding can take as long as it takes.
if [[ -f "${STATE}" ]]; then
  # shellcheck disable=SC1090
  source "${STATE}"
  echo "==> reusing key ${KEY_ID}"
else
  echo "==> creating a tenant"
  suffix=$(date +%s)
  customer=$(api -X POST "${API}/admin/customers" -H 'Content-Type: application/json' \
    -H "x-admin-key: ${ADMIN_API_KEY}" \
    -d "{\"email\":\"sepolia-${suffix}@example.com\",\"name\":\"sepolia-${suffix}\",\"tier\":\"enterprise\"}")
  API_KEY=$(echo "${customer}" | jqp 'd["api_key"]') || fail "no api_key: ${customer}"
  echo "==> provisioning a 2-of-3 threshold key (real DKG, takes about a minute)"
  created=$(api -X POST "${API}/keys" -H 'Content-Type: application/json' -H "x-api-key: ${API_KEY}" \
    -d "{\"blockchain\":\"ethereum\",\"threshold\":2,\"total_parties\":3,\"name\":\"sepolia-${suffix}\"}")
  KEY_ID=$(echo "${created}" | jqp 'd["id"]') || fail "key not created: ${created}"
  umask 077
  printf 'API_KEY=%s\nKEY_ID=%s\n' "${API_KEY}" "${KEY_ID}" > "${STATE}"
fi

for _ in $(seq 1 120); do
  key=$(api -H "x-api-key: ${API_KEY}" "${API}/keys/${KEY_ID}")
  status=$(echo "${key}" | jqp 'd.get("status")')
  [[ "${status}" != "pending_dkg" ]] && break
  sleep 5
done
[[ "${status}" == "active" ]] || fail "key did not activate (status=${status}): ${key}"
ADDRESS=$(echo "${key}" | jqp 'd["address"]')

echo
echo "    The threshold key's Sepolia address is:"
echo "        ${ADDRESS}"
echo

# Fees: a 1559 transaction, capped at twice the current base fee plus a tip.
latest=$(rpc eth_getBlockByNumber '["latest", false]')
base=$(echo "${latest}" | jqp 'int(d["baseFeePerGas"], 16)')
tip=$(( 2000000000 ))
max_fee=$(( base * 2 + tip ))
need=$(( AMOUNT_WEI + GAS_LIMIT * max_fee ))

echo "==> waiting for funds (needs at least ${need} wei); fund the address above from a Sepolia faucet"
for i in $(seq 1 90); do
  bal=$(hex2int "$(rpc eth_getBalance "[\"${ADDRESS}\", \"latest\"]")")
  if (( bal >= need )); then break; fi
  (( i % 3 == 1 )) && echo "    balance ${bal} wei ... still waiting"
  sleep 20
done
(( bal >= need )) || fail "no funds arrived within 30 minutes; fund ${ADDRESS} and re-run (the key is kept)"
echo "    funded: ${bal} wei"

nonce=$(hex2int "$(rpc eth_getTransactionCount "[\"${ADDRESS}\", \"pending\"]")")

echo "==> signing ${AMOUNT_WEI} wei to ${DEST} with two of the three parties"
signed=$(api -X POST "${API}/keys/${KEY_ID}/transactions" -H 'Content-Type: application/json' \
  -H "x-api-key: ${API_KEY}" \
  -d "{\"to\":\"${DEST}\",\"value\":\"${AMOUNT_WEI}\",\"gasLimit\":${GAS_LIMIT},\"nonce\":${nonce},\"chainId\":${CHAIN_ID},\"maxFeePerGas\":\"${max_fee}\",\"maxPriorityFeePerGas\":\"${tip}\"}")
raw=$(echo "${signed}" | jqp 'd["raw_transaction"]') || fail "signing failed: ${signed}"
local_hash=$(echo "${signed}" | jqp 'd["transaction_hash"]')
parties=$(echo "${signed}" | jqp 'd.get("parties")')
echo "    signed by parties ${parties}; transaction hash ${local_hash}"

echo "==> broadcasting to Sepolia"
sent=$(rpc eth_sendRawTransaction "[\"${raw}\"]")
[[ "${sent}" == "${local_hash}" ]] || echo "    note: the network reports hash ${sent}"
echo "    accepted by the network: ${sent}"

echo "==> waiting for the transaction to be mined"
receipt=""
for _ in $(seq 1 60); do
  receipt=$(rpc eth_getTransactionReceipt "[\"${sent}\"]")
  [[ "${receipt}" != "null" && -n "${receipt}" ]] && break
  sleep 6
done
[[ "${receipt}" != "null" && -n "${receipt}" ]] || fail "not mined within 6 minutes; check https://sepolia.etherscan.io/tx/${sent}"
rstatus=$(echo "${receipt}" | jqp 'd["status"]')
block=$(hex2int "$(echo "${receipt}" | jqp 'd["blockNumber"]')")
[[ "${rstatus}" == "0x1" ]] || fail "mined but reverted (status ${rstatus}): https://sepolia.etherscan.io/tx/${sent}"

{
  echo "Sepolia acceptance of a threshold-signed transaction"
  echo "date:        $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "key address: ${ADDRESS}"
  echo "signed by:   parties ${parties} (2-of-3 threshold ECDSA)"
  echo "recipient:   ${DEST}"
  echo "amount:      ${AMOUNT_WEI} wei"
  echo "tx hash:     ${sent}"
  echo "block:       ${block}"
  echo "explorer:    https://sepolia.etherscan.io/tx/${sent}"
} | tee "${EVIDENCE}"
echo
echo "PASS: Sepolia mined block ${block} containing a transaction signed by the platform's 2-of-3 key."
