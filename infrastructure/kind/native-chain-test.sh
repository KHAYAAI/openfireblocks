#!/usr/bin/env bash
#
# Public-testnet proof for Solana (devnet) and Cosmos (a testnet): a 2-of-3 threshold key signs a
# transfer through the platform's API and a real public network accepts it.
#
# Before this, Solana and Cosmos were only ever exercised against mock nodes that check the
# signature but are not the real chains. A transaction a public validator set has confirmed is
# the first evidence the wire format is accepted by the real thing.
#
# Prerequisite (once per chain, on the server): point the signer at the network.
#   Solana:  helm upgrade ofb infrastructure/helm/openfireblocks -n openfireblocks --reuse-values \
#              --set mpcSigner.solana.rpcUrl=https://api.devnet.solana.com --wait
#   Cosmos:  helm upgrade ofb infrastructure/helm/openfireblocks -n openfireblocks --reuse-values \
#              --set mpcSigner.cosmos.lcdUrl=<LCD URL> --set mpcSigner.cosmos.chainId=<chain id> \
#              --set mpcSigner.cosmos.bech32Prefix=cosmos --set mpcSigner.cosmos.feeDenom=uatom --wait
#   (then: kubectl -n openfireblocks rollout status deployment/ofb-openfireblocks-mpc-signer)
#
# Run it ON THE SERVER:
#   set -a; source ~/ofb-secrets.txt; set +a
#   infrastructure/kind/native-chain-test.sh solana
#   infrastructure/kind/native-chain-test.sh cosmos
#
# It creates a throwaway tenant, a 2-of-3 key (and a second key to receive the transfer unless
# DEST is set), shows the address to fund, waits for the balance, sends, and waits for the
# network to confirm. Solana is funded by an airdrop request automatically; Cosmos needs the
# testnet faucet. Ctrl-C and re-run is safe: state is kept in ~/.<chain>-test-state.
#
# Optional: DEST, AMOUNT, DENOM (Cosmos, default uatom), SOLANA_RPC (for the airdrop only),
# EXPLORER_BASE (Cosmos explorer tx URL prefix, to print a link).
set -euo pipefail

CHAIN="${1:-}"
[[ "${CHAIN}" == "solana" || "${CHAIN}" == "cosmos" ]] || { echo "usage: $0 solana|cosmos" >&2; exit 2; }
API="${API:-http://127.0.0.1:3000}"
: "${ADMIN_API_KEY:?export ADMIN_API_KEY first (source ~/ofb-secrets.txt with set -a)}"
STATE="${HOME}/.${CHAIN}-test-state"
EVIDENCE="${HOME}/${CHAIN}-evidence.txt"
# The platform treats a native-coin transfer as over the Travel Rule threshold because it has no
# price source for it (a refusal that is correct, not a bug), so every send needs originator and
# beneficiary details. These are clearly-labelled testnet values; override with TRAVEL_RULE='{...}'.
if [[ -z "${TRAVEL_RULE:-}" ]]; then
  TRAVEL_RULE='{"originator":{"legalPerson":{"name":"FORGE CUSTODY testnet treasury","customerIdentification":"forge-testnet-001"}},"beneficiary":{"legalPerson":{"name":"Testnet recipient"}},"beneficiaryUnhosted":true}'
fi

if [[ "${CHAIN}" == "solana" ]]; then
  AMOUNT="${AMOUNT:-1000000}"          # lamports
  NEED="${NEED:-5000000}"              # amount + fee + headroom
  DENOM_JSON=""
  SOLANA_RPC="${SOLANA_RPC:-https://api.devnet.solana.com}"
else
  AMOUNT="${AMOUNT:-1000}"
  NEED="${NEED:-30000}"
  DENOM="${DENOM:-uatom}"
  DENOM_JSON=",\"denom\":\"${DENOM}\""
fi

fail() { echo "FAIL: $*" >&2; exit 1; }
api() { curl -sS --noproxy '*' "$@"; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

echo "==> health"
api "${API}/health/ready" | grep -q '"postgres":"ok"' || fail "gateway is not ready"

make_key() { # name -> prints key id
  local created
  created=$(api -X POST "${API}/keys" -H 'Content-Type: application/json' -H "x-api-key: ${API_KEY}" \
    -d "{\"blockchain\":\"${CHAIN}\",\"threshold\":2,\"total_parties\":3,\"name\":\"$1\"}")
  echo "${created}" | jqp 'd["id"]' || fail "key not created: ${created}"
}
wait_active() { # key id
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
balance_of() { api -H "x-api-key: ${API_KEY}" "${API}/keys/$1/balances" | jqp 'int(d["balances"][0]["amount"])' 2>/dev/null || echo 0; }

if [[ -f "${STATE}" ]]; then
  # shellcheck disable=SC1090
  source "${STATE}"
  echo "==> reusing keys from ${STATE}"
else
  echo "==> creating a tenant"
  suffix=$(date +%s)
  customer=$(api -X POST "${API}/admin/customers" -H 'Content-Type: application/json' \
    -H "x-admin-key: ${ADMIN_API_KEY}" \
    -d "{\"email\":\"${CHAIN}-${suffix}@example.com\",\"name\":\"${CHAIN}-${suffix}\",\"tier\":\"enterprise\"}")
  API_KEY=$(echo "${customer}" | jqp 'd["api_key"]') || fail "no api_key: ${customer}"
  echo "==> provisioning the sending key (real DKG)"
  KEY_ID=$(make_key "${CHAIN}-send-${suffix}")
  DEST_KEY_ID=""
  umask 077
  printf 'API_KEY=%s\nKEY_ID=%s\nDEST_KEY_ID=%s\n' "${API_KEY}" "${KEY_ID}" "${DEST_KEY_ID}" > "${STATE}"
fi
wait_active "${KEY_ID}"
ADDRESS=$(addr_of "${KEY_ID}")

if [[ -z "${DEST:-}" ]]; then
  if [[ -z "${DEST_KEY_ID:-}" ]]; then
    echo "==> provisioning a second key to receive the transfer"
    DEST_KEY_ID=$(make_key "${CHAIN}-recv-$(date +%s)")
    sed -i "s/^DEST_KEY_ID=.*/DEST_KEY_ID=${DEST_KEY_ID}/" "${STATE}"
  fi
  wait_active "${DEST_KEY_ID}"
  DEST=$(addr_of "${DEST_KEY_ID}")
fi

echo
echo "    The sending key's ${CHAIN} address (fund this):"
echo "        ${ADDRESS}"
echo "    Recipient:"
echo "        ${DEST}"
echo

bal=$(balance_of "${KEY_ID}")
if (( bal < NEED )) && [[ "${CHAIN}" == "solana" ]]; then
  echo "==> requesting a 1 SOL devnet airdrop (it is often rate-limited; use https://faucet.solana.com if it fails)"
  air=$(curl -sS -m 30 -H 'Content-Type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"requestAirdrop\",\"params\":[\"${ADDRESS}\",1000000000]}" "${SOLANA_RPC}" || true)
  echo "    ${air}" | cut -c1-200
fi

echo "==> waiting for the balance to reach ${NEED} (read through the platform's own signer)"
for i in $(seq 1 90); do
  bal=$(balance_of "${KEY_ID}")
  if (( bal >= NEED )); then break; fi
  (( i % 3 == 1 )) && echo "    balance ${bal} ... still waiting; fund ${ADDRESS}"
  sleep 20
done
(( bal >= NEED )) || fail "no funds within 30 minutes; fund ${ADDRESS} and re-run (the keys are kept)"
echo "    funded: ${bal}"

echo "==> signing ${AMOUNT} to ${DEST} with two of the three parties and broadcasting"
sent=$(api -X POST "${API}/keys/${KEY_ID}/${CHAIN}-transactions" -H 'Content-Type: application/json' \
  -H "x-api-key: ${API_KEY}" -d "{\"destination\":\"${DEST}\",\"amount\":\"${AMOUNT}\"${DENOM_JSON},\"travelRule\":${TRAVEL_RULE}}")
if [[ "${CHAIN}" == "solana" ]]; then
  txid=$(echo "${sent}" | jqp 'd["signature"]') || fail "send failed: ${sent}"
else
  txid=$(echo "${sent}" | jqp 'd["txhash"]') || fail "send failed: ${sent}"
fi
parties=$(echo "${sent}" | jqp 'd.get("parties")')
[[ "$(echo "${sent}" | jqp 'd.get("broadcast", True)')" == "True" ]] || fail "signed but not broadcast: ${sent}"
echo "    signed by parties ${parties}; transaction ${txid}"

echo "==> waiting for the network to confirm it"
ok=""
for _ in $(seq 1 60); do
  st=$(api -H "x-api-key: ${API_KEY}" "${API}/keys/${KEY_ID}/${CHAIN}-transactions/${txid}" || echo '{}')
  if [[ "${CHAIN}" == "solana" ]]; then
    ok=$(echo "${st}" | jqp 'd.get("found") and d.get("err") in (None, False) and d.get("confirmation_status") in ("confirmed","finalized")' 2>/dev/null || true)
  else
    ok=$(echo "${st}" | jqp 'd.get("found") and d.get("code") in (None, 0)' 2>/dev/null || true)
  fi
  [[ "${ok}" == "True" ]] && break
  sleep 5
done
[[ "${ok}" == "True" ]] || fail "not confirmed within 5 minutes: ${st}"

if [[ "${CHAIN}" == "solana" ]]; then
  link="https://explorer.solana.com/tx/${txid}?cluster=devnet"
else
  link="${EXPLORER_BASE:+${EXPLORER_BASE}${txid}}"
fi
{
  echo "${CHAIN} acceptance of a threshold-signed transaction"
  echo "date:        $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "from:        ${ADDRESS}"
  echo "to:          ${DEST}"
  echo "amount:      ${AMOUNT}${DENOM:+ ${DENOM}}"
  echo "signed by:   parties ${parties} (2-of-3 threshold)"
  echo "transaction: ${txid}"
  echo "status:      $(echo "${st}" | cut -c1-300)"
  [[ -n "${link}" ]] && echo "explorer:    ${link}"
} | tee "${EVIDENCE}"
echo
echo "PASS: a public ${CHAIN} network confirmed a transaction signed by the platform's 2-of-3 key."
