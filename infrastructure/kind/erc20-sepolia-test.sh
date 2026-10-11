#!/usr/bin/env bash
#
# Public-chain proof for stablecoins: a registered, verified ERC-20 (Circle's test USDC on Sepolia) moves
# out of the platform's 2-of-3 threshold key, and Sepolia mines the transfer.
#
# Why it matters: the stablecoin controls (token registry, on-chain verification of symbol/decimals,
# calldata decoding before policy) have only been exercised on a private chain. This shows the same
# path against the real token contract.
#
# Prerequisites (on the server):
#  1. sepolia-test.sh has been run, so ~/.sepolia-test-state holds a funded Sepolia key (it pays the gas).
#  2. The gateway knows a Sepolia RPC, for token verification and balance reads:
#       helm upgrade ofb infrastructure/helm/openfireblocks -n openfireblocks --reuse-values \
#         --set 'external.evmRpc.11155111=https://ethereum-sepolia-rpc.publicnode.com' --wait
#       kubectl -n openfireblocks rollout status deployment/ofb-openfireblocks-api-gateway
#       sudo systemctl restart ofb-forward
#  3. Test USDC in the key. Request it for the key's address at https://faucet.circle.com (choose
#     Ethereum Sepolia, USDC). The script prints the address and waits.
#
#   set -a; source ~/ofb-secrets.txt; set +a
#   export SEPOLIA_RPC='https://ethereum-sepolia-rpc.publicnode.com'
#   infrastructure/kind/erc20-sepolia-test.sh
#
# Optional: TOKEN_ADDRESS (default: Circle's Sepolia USDC; verify it at developers.circle.com), DEST,
# AMOUNT (default 1.00, in whole tokens).
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
: "${ADMIN_API_KEY:?export ADMIN_API_KEY first (source ~/ofb-secrets.txt with set -a)}"
: "${SEPOLIA_RPC:?export SEPOLIA_RPC first}"
SEPOLIA_RPC="$(printf '%s' "${SEPOLIA_RPC}" | tr -d '[:space:]')"
TOKEN_ADDRESS="${TOKEN_ADDRESS:-0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238}"
DEST="${DEST:-0x000000000000000000000000000000000000dEaD}"
AMOUNT="${AMOUNT:-1.00}"
DECIMALS=6
CHAIN_ID=11155111
STATE="${HOME}/.sepolia-test-state"
EVIDENCE="${HOME}/erc20-evidence.txt"
GAS_LIMIT=120000
if [[ -z "${TRAVEL_RULE:-}" ]]; then
  TRAVEL_RULE='{"originator":{"legalPerson":{"name":"FORGE CUSTODY testnet treasury","customerIdentification":"forge-testnet-001"}},"beneficiary":{"legalPerson":{"name":"Testnet recipient"}},"beneficiaryUnhosted":true}'
fi

fail() { echo "FAIL: $*" >&2; exit 1; }
api() { curl -sS --noproxy '*' "$@"; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }
hex2int() { python3 -c "print(int('$1', 16))"; }

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

# balanceOf(address) on the token, in base units.
token_balance() {
  local data
  data="0x70a08231$(python3 -c "print('$1'.lower().replace('0x','').rjust(64,'0'))")"
  hex2int "$(rpc eth_call "[{\"to\":\"${TOKEN_ADDRESS}\",\"data\":\"${data}\"},\"latest\"]")"
}

[[ -f "${STATE}" ]] || fail "run sepolia-test.sh first: ${STATE} holds the funded Sepolia key"
# shellcheck disable=SC1090
source "${STATE}"

echo "==> checking the Sepolia RPC and the gateway"
[[ "$(hex2int "$(rpc eth_chainId)")" == "${CHAIN_ID}" ]] || fail "the RPC is not Sepolia"
api "${API}/health/ready" | grep -q '"postgres":"ok"' || fail "gateway is not ready"
ADDRESS=$(api -H "x-api-key: ${API_KEY}" "${API}/keys/${KEY_ID}/addresses" | jqp 'd["addresses"]["preferred"]')

echo "==> registering USDC on Sepolia in the token registry (operator action) and verifying it on-chain"
existing=$(api -H "x-admin-key: ${ADMIN_API_KEY}" "${API}/admin/tokens?chainId=${CHAIN_ID}")
TOKEN_ID=$(echo "${existing}" | jqp '(lambda t: t[0]["tokenId"] if t else "")([x for x in d["tokens"] if x["symbol"]=="USDC" and (x.get("contractAddress") or "").lower()=="'"${TOKEN_ADDRESS}"'".lower()])')
if [[ -z "${TOKEN_ID}" ]]; then
  reg=$(api -X POST "${API}/admin/tokens" -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_API_KEY}" \
    -d "{\"chainId\":${CHAIN_ID},\"contractAddress\":\"${TOKEN_ADDRESS}\",\"symbol\":\"USDC\",\"name\":\"USD Coin\",\"decimals\":${DECIMALS},\"pegCurrency\":\"USD\",\"issuer\":\"Circle\"}")
  TOKEN_ID=$(echo "${reg}" | jqp 'd["token"]["tokenId"]') || fail "could not register the token: ${reg}"
fi
ver=$(api -X POST "${API}/admin/tokens/${TOKEN_ID}/verify" -H "x-admin-key: ${ADMIN_API_KEY}")
tstatus=$(echo "${ver}" | jqp 'd["token"]["status"]') || fail "verification call failed: ${ver}"
[[ "${tstatus}" == "verified" ]] || fail "the contract did not verify (status ${tstatus}): ${ver}"
echo "    verified on-chain: symbol and decimals read from ${TOKEN_ADDRESS}"

BASE_UNITS=$(python3 -c "from decimal import Decimal;print(int(Decimal('${AMOUNT}')*10**${DECIMALS}))")
echo
echo "    The key's Sepolia address (it needs test USDC, and ETH for gas, which it already has):"
echo "        ${ADDRESS}"
echo "    Request USDC for it at https://faucet.circle.com (Ethereum Sepolia)."
echo
echo "==> waiting for at least ${BASE_UNITS} base units of USDC"
for i in $(seq 1 90); do
  bal=$(token_balance "${ADDRESS}")
  (( bal >= BASE_UNITS )) && break
  (( i % 3 == 1 )) && echo "    USDC balance ${bal} ... still waiting"
  sleep 20
done
(( bal >= BASE_UNITS )) || fail "no USDC within 30 minutes; fund ${ADDRESS} and re-run"
echo "    USDC balance: ${bal}"
dest_before=$(token_balance "${DEST}")

latest=$(rpc eth_getBlockByNumber '["latest", false]')
base=$(echo "${latest}" | jqp 'int(d["baseFeePerGas"], 16)')
tip=2000000000
max_fee=$(( base * 2 + tip ))
nonce=$(hex2int "$(rpc eth_getTransactionCount "[\"${ADDRESS}\", \"pending\"]")")

echo "==> sending ${AMOUNT} USDC to ${DEST}: the platform encodes the ERC-20 call, then two of three parties sign"
signed=$(api -X POST "${API}/keys/${KEY_ID}/token-transfers" -H 'Content-Type: application/json' -H "x-api-key: ${API_KEY}" \
  -d "{\"token\":\"USDC\",\"recipient\":\"${DEST}\",\"amount\":\"${AMOUNT}\",\"chainId\":${CHAIN_ID},\"nonce\":${nonce},\"gasLimit\":${GAS_LIMIT},\"maxFeePerGas\":\"${max_fee}\",\"maxPriorityFeePerGas\":\"${tip}\",\"travelRule\":${TRAVEL_RULE}}")
raw=$(echo "${signed}" | jqp 'd["raw_transaction"]') || fail "signing failed: ${signed}"
local_hash=$(echo "${signed}" | jqp 'd["transaction_hash"]')
parties=$(echo "${signed}" | jqp 'd.get("parties")')
echo "    signed by parties ${parties}; transaction hash ${local_hash}"

echo "==> broadcasting to Sepolia"
sent=$(rpc eth_sendRawTransaction "[\"${raw}\"]")
echo "    accepted by the network: ${sent}"

echo "==> waiting for the transaction to be mined"
receipt=""
for _ in $(seq 1 60); do
  receipt=$(rpc eth_getTransactionReceipt "[\"${sent}\"]")
  [[ "${receipt}" != "null" && -n "${receipt}" ]] && break
  sleep 6
done
[[ "${receipt}" != "null" && -n "${receipt}" ]] || fail "not mined within 6 minutes: https://sepolia.etherscan.io/tx/${sent}"
[[ "$(echo "${receipt}" | jqp 'd["status"]')" == "0x1" ]] || fail "mined but reverted: https://sepolia.etherscan.io/tx/${sent}"
block=$(hex2int "$(echo "${receipt}" | jqp 'd["blockNumber"]')")

# The Transfer(address,address,uint256) event the token contract itself emitted.
transfer=$(echo "${receipt}" | python3 -c "
import sys,json
d=json.load(sys.stdin)
T='0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef'
tok='${TOKEN_ADDRESS}'.lower()
for l in d['logs']:
    if l['address'].lower()==tok and l['topics'][0]==T:
        print(int(l['data'],16)); break
else:
    print('')")
[[ "${transfer}" == "${BASE_UNITS}" ]] || fail "the token contract did not emit a Transfer of ${BASE_UNITS} (got '${transfer}')"
dest_after=$(token_balance "${DEST}")
(( dest_after - dest_before == BASE_UNITS )) || fail "the recipient balance did not rise by ${BASE_UNITS}"

{
  echo "ERC-20 stablecoin transfer accepted by a public chain"
  echo "date:        $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "network:     Ethereum Sepolia (${CHAIN_ID})"
  echo "token:       USDC ${TOKEN_ADDRESS} (registered and verified on-chain by the platform)"
  echo "from:        ${ADDRESS} (2-of-3 threshold key)"
  echo "to:          ${DEST}"
  echo "amount:      ${AMOUNT} USDC (${BASE_UNITS} base units); the contract's Transfer event and the recipient balance both match"
  echo "signed by:   parties ${parties}"
  echo "tx hash:     ${sent}"
  echo "block:       ${block}"
  echo "explorer:    https://sepolia.etherscan.io/tx/${sent}"
} | tee "${EVIDENCE}"
echo
echo "PASS: Sepolia mined block ${block}; the USDC contract emitted the Transfer and the recipient's balance rose."
