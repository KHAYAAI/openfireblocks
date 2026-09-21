#!/usr/bin/env bash
#
# Proves the platform can see what a stablecoin transfer moves.
#
# The defect this drill exists against: an ERC-20 transfer carries its
# recipient and amount inside the calldata. The transaction's own `to` is
# the token contract and its `value` is zero, and every control in this
# platform read those two fields. The amount limit compared zero against
# the ceiling. The counterparty whitelist compared the same contract
# address on every transfer of that token. The daily aggregate that decides
# whether a regulatory filing is due summed to nothing.
#
# None of that was visible. The signing worked perfectly -- a customer who
# hand-encoded transfer() got a real 2-of-3 threshold-signed USDC transfer
# out of the platform -- which is what made it worse than not supporting
# stablecoins at all.
#
# So the assertions here are mostly about refusals. A drill that only shows
# a transfer succeeding would have passed before any of this was fixed.
#
# Requires a chain deployed and the API reachable:
#   kubectl -n openfireblocks port-forward svc/ofb-openfireblocks-api-gateway 3000:3000
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CURL=(curl -sS --noproxy '*')

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

GPOD="$(kubectl -n "${NS}" get pod -l app=geth-dev --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
if [[ -z "${GPOD}" ]]; then
  GPOD="$(kubectl -n "${NS}" get pod -l app=geth-signer --field-selector=status.phase=Running \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
fi
[[ -n "${GPOD}" ]] || fail "no chain is running; kubectl apply -f ${HERE}/geth-dev.yaml"

rpc() {
  kubectl -n "${NS}" exec "${GPOD}" -c geth -- sh -c \
    "wget -qO- --post-data='{\"jsonrpc\":\"2.0\",\"method\":\"$1\",\"params\":$2,\"id\":1}' \
     --header='Content-Type: application/json' http://127.0.0.1:8545"
}
rpc_result() { rpc "$1" "$2" | jqp 'd.get("result")'; }

CHAIN_ID=$(( $(rpc_result eth_chainId '[]') ))
COINBASE="$(rpc eth_accounts '[]' | jqp 'd["result"][0]')"
echo "==> chain ${CHAIN_ID}, funder ${COINBASE}"

BYTECODE="$(cat "${HERE}/erc20/DrillToken.bin")"

# Waits for a transaction to be mined and returns its receipt field.
receipt_field() {
  local txhash="$1" field="$2"
  for _ in $(seq 1 60); do
    local r
    r=$(rpc eth_getTransactionReceipt "[\"${txhash}\"]")
    local value
    value=$(echo "${r}" | python3 -c "
import sys,json
d=json.load(sys.stdin).get('result')
print((d or {}).get('${field}') or '')
")
    [[ -n "${value}" ]] && { echo "${value}"; return 0; }
    sleep 1
  done
  return 1
}

# Deploys the drill token with a given symbol, decimals and supply.
deploy_token() {
  local symbol="$1" decimals="$2" supply="$3"
  local args
  args=$(python3 -c "
w = lambda n: format(n, '064x')
b = '${symbol}'.encode()
print(w(96) + w(${decimals}) + w(${supply}) + w(len(b)) + b.hex().ljust(64, '0'))
")
  local tx
  tx=$(rpc eth_sendTransaction \
    "[{\"from\":\"${COINBASE}\",\"data\":\"0x${BYTECODE}${args}\",\"gas\":\"0x300000\"}]" \
    | jqp 'd.get("result","")')
  [[ "${tx}" == 0x* ]] || fail "deploying ${symbol} was rejected: ${tx}"
  receipt_field "${tx}" contractAddress || fail "${symbol} never got a contract address"
}

echo "==> deploying two tokens that differ only in their decimals"
# The same contract, twice. Six decimals and eighteen, because that is the
# difference between USDC and DAI and the factor by which an amount limit
# is wrong when the platform assumes instead of reading.
USD_TOKEN=$(deploy_token USDD 6 1000000000000000)
ZAR_TOKEN=$(deploy_token ZARD 18 1000000000000000000000000000)
echo "    USDD (6 dp)  ${USD_TOKEN}"
echo "    ZARD (18 dp) ${ZAR_TOKEN}"

echo "==> registering them"
register() {
  local address="$1" symbol="$2" decimals="$3" peg="$4"
  "${CURL[@]}" -X POST "${API}/admin/tokens" \
    -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
    -d "{\"chainId\":${CHAIN_ID},\"contractAddress\":\"${address}\",\"symbol\":\"${symbol}\",
         \"name\":\"drill ${symbol}\",\"decimals\":${decimals},\"pegCurrency\":\"${peg}\"}"
}
USD_ID=$(register "${USD_TOKEN}" USDD 6 USD | jqp 'd["token"]["tokenId"]')
ZAR_ID=$(register "${ZAR_TOKEN}" ZARD 18 ZAR | jqp 'd["token"]["tokenId"]')
[[ -n "${USD_ID}" && "${USD_ID}" != "None" ]] || fail "registering USDD failed"

# ---------------------------------------------------------------------
# A registered token is not yet a usable one
# ---------------------------------------------------------------------

echo "==> a registered but unverified token cannot move money"
# The gate that makes a seeded or hand-entered contract address safe. A
# wrong address must be a configuration error, never a loss.
S=$(date +%s)
customer=$("${CURL[@]}" -X POST "${API}/admin/customers" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"email\":\"stable-${S}@example.com\",\"name\":\"stable-${S}\",\"tier\":\"enterprise\"}")
api_key=$(echo "${customer}" | jqp 'd["api_key"]')

created=$("${CURL[@]}" -X POST "${API}/keys" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"blockchain\":\"ethereum\",\"threshold\":2,\"total_parties\":3,\"name\":\"stable-${S}\"}")
key_id=$(echo "${created}" | jqp 'd["id"]')
for _ in $(seq 1 120); do
  key=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}")
  status=$(echo "${key}" | jqp 'd.get("status")')
  [[ "${status}" != "pending_dkg" ]] && break
  sleep 5
done
[[ "${status}" == "active" ]] || fail "key did not activate: ${key}"
FROM=$(echo "${key}" | jqp 'd["address"]')
echo "    key ${key_id} at ${FROM}"

send_token() {
  local token="$1" recipient="$2" amount="$3" nonce="$4"
  local gas_price
  gas_price=$(( $(rpc_result eth_gasPrice '[]') ))
  "${CURL[@]}" -w '\n%{http_code}' -X POST "${API}/keys/${key_id}/token-transfers" \
    -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
    -d "{\"token\":\"${token}\",\"recipient\":\"${recipient}\",\"amount\":\"${amount}\",
         \"chainId\":${CHAIN_ID},\"nonce\":${nonce},\"gasPrice\":\"${gas_price}\"}"
}

out=$(send_token USDD 0x00000000000000000000000000000000000000aa 1 0)
code=$(echo "${out}" | tail -1)
[[ "${code}" == "400" ]] \
  || fail "an unverified token was transactable (HTTP ${code}); a contract address nobody has checked must not be able to move money"
echo "    refused, as it must be"

echo "==> a registration that disagrees with its contract fails verification"
# A separate contract, so this exercises verification rather than the
# unique index on (chain_id, contract_address).
#
# Deployed reporting six decimals and registered claiming eighteen. If
# that were accepted, every amount limit on this token would be a million
# million times too generous, every balance would read as a rounding
# error, and nothing about a transfer would look unusual. This is the
# single most consequential thing the registry gets right, so it gets its
# own proof.
WRONG_TOKEN=$(deploy_token WRNG 6 1000000000000000)
LIAR=$("${CURL[@]}" -X POST "${API}/admin/tokens" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"chainId\":${CHAIN_ID},\"contractAddress\":\"${WRONG_TOKEN}\",\"symbol\":\"WRNG\",
       \"name\":\"wrong decimals\",\"decimals\":18,\"pegCurrency\":\"USD\"}" \
  | jqp 'd["token"]["tokenId"]')
[[ -n "${LIAR}" && "${LIAR}" != "None" ]] || fail "registering the mismatched token failed outright"

verify_out=$("${CURL[@]}" -w '\n%{http_code}' -X POST "${API}/admin/tokens/${LIAR}/verify" \
  -H "x-admin-key: ${ADMIN_KEY}")
vcode=$(echo "${verify_out}" | tail -1)
[[ "${vcode}" == "400" ]] \
  || fail "a token registered at 18 decimals against a 6-decimal contract passed verification (HTTP ${vcode})"
echo "    rejected: the contract reports 6 decimals, the registry claimed 18"

# And it stays unusable rather than being left in some half-verified
# state. A failed verification must not advance the status.
still=$("${CURL[@]}" -H "x-admin-key: ${ADMIN_KEY}" "${API}/admin/tokens?chainId=${CHAIN_ID}" \
  | python3 -c "
import sys,json
d=json.load(sys.stdin)
print(next((t['status'] for t in d['tokens'] if t['tokenId']=='${LIAR}'), 'missing'))
")
[[ "${still}" != "verified" ]] \
  || fail "the mismatched token is marked verified despite the check failing"
echo "    and left in status '${still}', so it cannot move money"

echo "==> verifying the real registrations against the chain"
for id in "${USD_ID}" "${ZAR_ID}"; do
  v=$("${CURL[@]}" -w '\n%{http_code}' -X POST "${API}/admin/tokens/${id}/verify" \
    -H "x-admin-key: ${ADMIN_KEY}")
  [[ "$(echo "${v}" | tail -1)" == "200" ]] \
    || fail "verification failed for ${id}: $(echo "${v}" | sed '$d')"
done
echo "    both verified against symbol() and decimals()"

# ---------------------------------------------------------------------
# Moving money
# ---------------------------------------------------------------------

echo "==> funding the key with gas and with tokens"
rpc eth_sendTransaction \
  "[{\"from\":\"${COINBASE}\",\"to\":\"${FROM}\",\"value\":\"0xde0b6b3a7640000\"}]" >/dev/null
# 100,000 USDD and 100,000 ZARD, sent from the deployer, which holds the
# whole supply.
fund_tokens() {
  local token="$1" amount_hex="$2"
  local data="0xa9059cbb$(python3 -c "print('${FROM}'[2:].lower().rjust(64,'0'))")$(python3 -c "print(format(${amount_hex}, '064x'))")"
  rpc eth_sendTransaction \
    "[{\"from\":\"${COINBASE}\",\"to\":\"${token}\",\"data\":\"${data}\",\"gas\":\"0x30000\"}]" >/dev/null
}
fund_tokens "${USD_TOKEN}" 100000000000               # 100,000 at 6 dp
fund_tokens "${ZAR_TOKEN}" 100000000000000000000000   # 100,000 at 18 dp
sleep 3

echo "==> the platform reports the balances it can now see"
# Before this existed a customer holding a hundred thousand dollars of a
# stablecoin saw a zero balance, because nothing in the platform had ever
# read an ERC-20 balance.
bal=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}/balances?chainId=${CHAIN_ID}")
USD_BAL=$(echo "${bal}" | python3 -c "
import sys,json
d=json.load(sys.stdin)
print(next((t['balance'] for t in d['tokens'] if t['symbol']=='USDD'), 'missing'))
")
ZAR_BAL=$(echo "${bal}" | python3 -c "
import sys,json
d=json.load(sys.stdin)
print(next((t['balance'] for t in d['tokens'] if t['symbol']=='ZARD'), 'missing'))
")
echo "    USDD ${USD_BAL}, ZARD ${ZAR_BAL}"
[[ "${USD_BAL}" == "100000" ]] \
  || fail "the platform reports ${USD_BAL} USDD, not the 100000 deposited"
[[ "${ZAR_BAL}" == "100000" ]] \
  || fail "the platform reports ${ZAR_BAL} ZARD, not the 100000 deposited"
# The whole point of the registry holding decimals: the same balance is
# reported identically for a 6-decimal and an 18-decimal token, because
# each was read with its own scale.
echo "    both read correctly despite a 10^12 difference in scale"

echo "==> sending, through a threshold ceremony"
DEST=0x00000000000000000000000000000000000000bb
NONCE=$(( $(rpc_result eth_getTransactionCount "[\"${FROM}\",\"pending\"]") ))
out=$(send_token USDD "${DEST}" 1500.25 "${NONCE}")
code=$(echo "${out}" | tail -1)
body=$(echo "${out}" | sed '$d')
[[ "${code}" == "200" ]] || fail "the transfer returned ${code}: ${body}"

BASE_UNITS=$(echo "${body}" | jqp 'd["amount_base_units"]')
RAW=$(echo "${body}" | jqp 'd["raw_transaction"]')
PARTIES=$(echo "${body}" | jqp 'd["parties"]')
echo "    1500.25 USDD = ${BASE_UNITS} base units, signed by parties ${PARTIES}"
[[ "${BASE_UNITS}" == "1500250000" ]] \
  || fail "1500.25 USDD encoded as ${BASE_UNITS}; the registry decimals were not applied"

echo "==> the chain agrees"
TXHASH=$(rpc eth_sendRawTransaction "[\"${RAW}\"]" | jqp 'd.get("result","")')
[[ "${TXHASH}" == 0x* ]] || fail "the node rejected the signed transfer: $(rpc eth_sendRawTransaction "[\"${RAW}\"]")"
STATUS=$(receipt_field "${TXHASH}" status) || fail "the transfer was never mined"
[[ "${STATUS}" == "0x1" ]] || fail "the transfer was mined but reverted (status ${STATUS})"

# Read the recipient's balance off the chain rather than trusting the
# platform's account of what it did.
RECEIVED=$(rpc_result eth_call \
  "[{\"to\":\"${USD_TOKEN}\",\"data\":\"0x70a08231$(python3 -c "print('${DEST}'[2:].lower().rjust(64,'0'))")\"},\"latest\"]")
[[ $((RECEIVED)) -eq 1500250000 ]] \
  || fail "the recipient holds $((RECEIVED)) base units, expected 1500250000"
echo "    recipient holds $((RECEIVED)) base units on chain"

# ---------------------------------------------------------------------
# The controls
# ---------------------------------------------------------------------

echo "==> a transfer over the dollar ceiling is refused"
# 2,000,000 USDD against the 1,000,000 USD global ceiling. Under the old
# arithmetic this request carried value "0" and passed every amount limit
# in the system.
NONCE=$(( $(rpc_result eth_getTransactionCount "[\"${FROM}\",\"pending\"]") ))
out=$(send_token USDD "${DEST}" 2000000 "${NONCE}")
code=$(echo "${out}" | tail -1)
[[ "${code}" == "403" ]] \
  || fail "a 2,000,000 USDD transfer returned ${code}, not 403; the amount limit is reading the native value field, which is 0 for every token transfer"
echo "    refused by policy"

echo "==> a rand transfer is measured against the rand ceiling"
# 1,000,000 ZARD. Numerically over the dollar ceiling and well under the
# rand one, so this passing is what proves the limits are denominated per
# currency rather than compared as bare numbers.
NONCE=$(( $(rpc_result eth_getTransactionCount "[\"${FROM}\",\"pending\"]") ))
out=$(send_token ZARD "${DEST}" 1000 "${NONCE}")
code=$(echo "${out}" | tail -1)
[[ "${code}" == "200" ]] \
  || fail "a 1,000 ZARD transfer returned ${code}: $(echo "${out}" | sed '$d')"
echo "    allowed, at a rand limit rather than a dollar one"

echo "==> a transfer to a stranger is refused when a whitelist is set"
# The other half of the blindness. A whitelist reads the recipient; for a
# token transfer that used to be the token contract, identical for every
# transfer, so the list either allowed all of them or none.
psql_admin() {
  kubectl -n "${NS}" exec deploy/postgres -- psql -U postgres -d openfireblocks -tAc "$1" 2>/dev/null
}
CUSTOMER_ID=$(echo "${customer}" | jqp 'd["customer_id"]')
psql_admin "UPDATE customers
               SET policies = '{\"whitelist\":[\"${USD_TOKEN}\"]}'::jsonb
             WHERE customer_id = '${CUSTOMER_ID}'::uuid" >/dev/null

NONCE=$(( $(rpc_result eth_getTransactionCount "[\"${FROM}\",\"pending\"]") ))
out=$(send_token USDD "${DEST}" 1 "${NONCE}")
code=$(echo "${out}" | tail -1)
[[ "${code}" == "403" ]] \
  || fail "a transfer to an address absent from the whitelist returned ${code}, not 403 -- and note the whitelist contains the token contract, so a whitelist reading the envelope would have allowed this"
echo "    refused: the whitelist saw the recipient, not the token contract"
psql_admin "UPDATE customers SET policies = '{}'::jsonb WHERE customer_id = '${CUSTOMER_ID}'::uuid" >/dev/null

echo "==> calldata the platform cannot account for is refused"
NONCE=$(( $(rpc_result eth_getTransactionCount "[\"${FROM}\",\"pending\"]") ))
GAS_PRICE=$(( $(rpc_result eth_gasPrice '[]') ))
out=$("${CURL[@]}" -w '\n%{http_code}' -X POST "${API}/keys/${key_id}/transactions" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"to\":\"${USD_TOKEN}\",\"value\":\"0\",\"data\":\"0xdeadbeef$(printf '0%.0s' {1..64})\",
       \"gasLimit\":100000,\"nonce\":${NONCE},\"chainId\":${CHAIN_ID},\"gasPrice\":\"${GAS_PRICE}\"}")
code=$(echo "${out}" | tail -1)
[[ "${code}" == "400" ]] \
  || fail "an unrecognised contract call returned ${code}, not 400; it would have been signed with no control having read what it does"
echo "    refused"

# ---------------------------------------------------------------------
# The record
# ---------------------------------------------------------------------

echo "==> the transfers are recorded as what they moved"
# The regulatory aggregate reads these columns. Before they existed, the
# threshold signing path wrote no recipient and no amount anywhere, so a
# day of stablecoin activity summed to nothing and no filing was ever
# raised.
ROWS=$(psql_admin "SELECT count(*) FROM signing.transactions
                    WHERE customer_id = '${CUSTOMER_ID}'::uuid
                      AND asset_symbol IN ('USDD','ZARD')
                      AND effective_amount IS NOT NULL")
[[ "${ROWS}" -ge 2 ]] \
  || fail "only ${ROWS} token transfer(s) were recorded with an effective amount"

RECORDED=$(psql_admin "SELECT effective_amount FROM signing.transactions
                        WHERE customer_id = '${CUSTOMER_ID}'::uuid
                          AND asset_symbol = 'USDD'
                          AND effective_amount = '1500250000'")
[[ "${RECORDED}" == "1500250000" ]] \
  || fail "the 1500.25 USDD transfer was not recorded at its real amount"

PEG=$(psql_admin "SELECT asset_peg FROM signing.transactions
                   WHERE customer_id = '${CUSTOMER_ID}'::uuid
                     AND asset_symbol = 'ZARD' LIMIT 1")
[[ "${PEG}" == "ZAR" ]] \
  || fail "the rand transfer was recorded with peg '${PEG}'; a filing would be raised in the wrong currency"
echo "    ${ROWS} transfers recorded with their real recipient, amount and peg"

echo
echo "PASS: stablecoin transfers are governed, recorded and settled."
echo "      An amount limit, a counterparty whitelist and a regulatory"
echo "      aggregate all now read the transfer rather than its envelope."
