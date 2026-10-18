#!/usr/bin/env bash
#
# Registers a token in the platform's token registry and has the platform verify it against the live
# contract. READ-ONLY: it sends no transaction, creates no key and spends nothing. Safe on a mainnet.
#
# Default: OUSD (Open Standard's dollar stablecoin) on Ethereum mainnet. Override for another token or chain:
#   CHAIN_ID=8453 TOKEN_ADDRESS=0xB2000000000000000000002fEb517dFeC7415344 ./token-verify-readonly.sh
#
# Prerequisite: the platform needs a read RPC for that chain, set on the cluster:
#   helm upgrade ofb infrastructure/helm/openfireblocks -n openfireblocks --reuse-values \
#     --set-string external.evmRpc.1=https://ethereum-rpc.publicnode.com --wait
#   (Base: external.evmRpc.8453=https://base-rpc.publicnode.com), then restart the gateway and ofb-forward.
#
# Run ON THE SERVER:  set -a; source ~/ofb-secrets.txt; set +a; ./infrastructure/kind/token-verify-readonly.sh
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
: "${ADMIN_API_KEY:?export ADMIN_API_KEY first (source ~/ofb-secrets.txt with set -a)}"
CHAIN_ID="${CHAIN_ID:-1}"
TOKEN_ADDRESS="${TOKEN_ADDRESS:-0x9f6F3991D525015a6F8CaF062C83b62fD3AC4436}"
SYMBOL="${SYMBOL:-OUSD}"
TOKEN_NAME="${TOKEN_NAME:-OpenUSD}"
DECIMALS="${DECIMALS:-6}"
PEG="${PEG:-USD}"
ISSUER="${ISSUER:-Open Standard}"
EVIDENCE="${HOME}/token-verify-${SYMBOL}-${CHAIN_ID}.txt"

fail() { echo "FAIL: $*" >&2; exit 1; }
api() { curl -sS --noproxy '*' "$@"; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

api "${API}/health/ready" | grep -q '"postgres":"ok"' || fail "gateway is not ready"
echo "==> registering ${SYMBOL} on chain ${CHAIN_ID} (${TOKEN_ADDRESS}) and verifying it on-chain. Nothing is sent."

existing=$(api -H "x-admin-key: ${ADMIN_API_KEY}" "${API}/admin/tokens?chainId=${CHAIN_ID}")
TOKEN_ID=$(echo "${existing}" | jqp '(lambda t: t[0]["tokenId"] if t else "")([x for x in d["tokens"] if x["symbol"]=="'"${SYMBOL}"'" and (x.get("contractAddress") or "").lower()=="'"${TOKEN_ADDRESS}"'".lower()])')
if [[ -z "${TOKEN_ID}" ]]; then
  reg=$(api -X POST "${API}/admin/tokens" -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_API_KEY}" \
    -d "{\"chainId\":${CHAIN_ID},\"contractAddress\":\"${TOKEN_ADDRESS}\",\"symbol\":\"${SYMBOL}\",\"name\":\"${TOKEN_NAME}\",\"decimals\":${DECIMALS},\"pegCurrency\":\"${PEG}\",\"issuer\":\"${ISSUER}\"}")
  TOKEN_ID=$(echo "${reg}" | jqp 'd["token"]["tokenId"]') || fail "could not register the token: ${reg}"
fi
ver=$(api -X POST "${API}/admin/tokens/${TOKEN_ID}/verify" -H "x-admin-key: ${ADMIN_API_KEY}")
tstatus=$(echo "${ver}" | jqp 'd["token"]["status"]') || fail "verification call failed: ${ver}"
[[ "${tstatus}" == "verified" ]] || fail "the contract did not verify (status ${tstatus}): ${ver}"

{
  echo "Token verified against the live contract by the platform (read-only; nothing sent)"
  echo "date:      $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "chain id:  ${CHAIN_ID}"
  echo "contract:  ${TOKEN_ADDRESS}"
  echo "symbol:    ${SYMBOL}   decimals: ${DECIMALS}   peg: ${PEG}   issuer: ${ISSUER}"
  echo "token id:  ${TOKEN_ID}"
  echo "status:    ${tstatus} (symbol and decimals read from the contract by the platform)"
} | tee "${EVIDENCE}"
echo
echo "PASS: the platform verified ${SYMBOL} against the real contract. No transaction was made."
