#!/usr/bin/env bash
#
# Spends real Bitcoin with a key nobody holds.
#
# The Ethereum path has been proven end to end for a while. Bitcoin was
# derivation only: the platform could produce a correct-looking address and
# had no way to move anything out of it, because everything in the Bitcoin
# signer took a private key as an argument and a threshold key does not have
# one.
#
# This is the same threshold-ECDSA ceremony Ethereum uses -- both chains are
# secp256k1, and a signature over a 32-byte digest does not care which chain
# produced the digest. Everything Bitcoin-specific is on either side of it:
# what gets hashed (the sighash) and how the signature is packaged (the
# signature script). Both live in services/mpc-signer/chains and are reached
# here through infrastructure/kind/btctool, so this drill exercises the code
# the platform ships.
#
# What makes it evidence rather than a demo: Bitcoin Core is the judge. The
# unit tests already run btcd's script interpreter over the assembled
# transaction, which catches signing the wrong digest and malformed scripts.
# What they cannot catch is a disagreement between our reading of the rules
# and Core's. Only Core accepting the bytes and mining them settles that.
#
# Requires bitcoind-regtest.yaml deployed and the API reachable.
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CURL=(curl -sS --noproxy '*')

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

BPOD="$(kubectl -n "${NS}" get pod -l app=bitcoind --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
[[ -n "${BPOD}" ]] || fail "bitcoind is not running; kubectl apply -f ${HERE}/bitcoind-regtest.yaml"

btc() {
  kubectl -n "${NS}" exec "${BPOD}" -- bitcoin-cli -regtest \
    -rpcuser=ofb -rpcpassword=ofb-regtest "$@" 2>/dev/null
}

btctool() { (cd "${HERE}/btctool" && go run . "$@"); }

echo "==> bitcoind regtest, block $(btc getblockcount)"

# A wallet for the drill's own funding transactions. bitcoind needs one to
# hold coinbase output; the threshold key is not in it and never will be.
btc createwallet drill >/dev/null 2>&1 || btc loadwallet drill >/dev/null 2>&1 || true

echo "==> provisioning a 2-of-3 threshold key"
S=$(date +%s)
customer=$("${CURL[@]}" -X POST "${API}/admin/customers" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"email\":\"btc-${S}@example.com\",\"name\":\"btc-${S}\",\"tier\":\"enterprise\"}")
api_key=$(echo "${customer}" | jqp 'd["api_key"]')
customer_id=$(echo "${customer}" | jqp 'd["customer_id"]')

created=$("${CURL[@]}" -X POST "${API}/keys" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"blockchain\":\"bitcoin\",\"threshold\":2,\"total_parties\":3,\"name\":\"btc-${S}\"}")
key_id=$(echo "${created}" | jqp 'd["id"]')
for _ in $(seq 1 120); do
  key=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}")
  status=$(echo "${key}" | jqp 'd.get("status")')
  [[ "${status}" != "pending_dkg" ]] && break
  sleep 5
done
[[ "${status}" == "active" ]] || fail "key did not activate: ${key}"

PUBKEY=$(echo "${key}" | jqp 'd["public_key"]')
[[ -n "${PUBKEY}" && "${PUBKEY}" != "None" ]] || fail "the key has no public key: ${key}"

# The same DKG public key, read through Bitcoin's address rules rather than
# Ethereum's. One key, many chains -- which is the point of deriving rather
# than provisioning a separate key per chain.
BTC_ADDR=$(btctool address "${PUBKEY}")
echo "    key ${key_id}"
echo "    bitcoin address ${BTC_ADDR}"

echo "==> funding it on regtest"
# 101 blocks: coinbase output needs 100 confirmations before it is spendable,
# so anything less leaves the wallet with a balance it cannot actually use.
MINER=$(btc getnewaddress)
btc generatetoaddress 101 "${MINER}" >/dev/null
FUND_TXID=$(btc -rpcwallet=drill sendtoaddress "${BTC_ADDR}" 0.5)
[[ -n "${FUND_TXID}" ]] || fail "funding transaction was not accepted"
btc generatetoaddress 1 "${MINER}" >/dev/null
echo "    funded with 0.5 BTC in ${FUND_TXID}"

# Find the output that actually paid the threshold address. sendtoaddress
# also creates a change output, and spending the wrong one produces a
# transaction whose signature cannot possibly satisfy the script.
VOUT_JSON=$(btc getrawtransaction "${FUND_TXID}" 1)
read -r VOUT AMOUNT SCRIPT <<<"$(echo "${VOUT_JSON}" | python3 -c "
import sys, json
tx = json.load(sys.stdin)
for out in tx['vout']:
    spk = out['scriptPubKey']
    if '${BTC_ADDR}' in (spk.get('addresses') or [spk.get('address')]):
        print(out['n'], int(round(out['value'] * 1e8)), spk['hex'])
        break
else:
    raise SystemExit('no output paid the threshold address')
")"
[[ -n "${VOUT:-}" ]] || fail "could not find the funding output"
echo "    spending vout ${VOUT} (${AMOUNT} sats)"

echo "==> planning the spend and computing the sighash"
DEST=$(btc getnewaddress)
SEND=$(( AMOUNT - 10000 ))   # 10k sats fee
PLAN=$(python3 -c "
import json
print(json.dumps({
  'network': 'regtest',
  'inputs':  [{'txid': '${FUND_TXID}', 'vout': ${VOUT}, 'amount': ${AMOUNT}, 'script': '${SCRIPT}'}],
  'outputs': [{'address': '${DEST}', 'amount': ${SEND}}],
}))" | btctool plan)
SIGHASH=$(echo "${PLAN}" | jqp 'd["sighashes"][0]')
[[ ${#SIGHASH} -eq 64 ]] || fail "sighash is not 32 bytes: ${SIGHASH}"
echo "    sighash ${SIGHASH}"

echo "==> threshold-signing the sighash with 2 of the 3 parties"
# The digest route, which is what a non-Ethereum chain needs: the gateway
# cannot build a Bitcoin transaction, so it signs the digest this drill
# computed. It is gated per tenant precisely because a digest is opaque to
# policy -- see migration 017 -- so the drill grants it explicitly.
"${CURL[@]}" -o /dev/null -X PUT "${API}/admin/customers/${customer_id}/raw-digest-signing" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d '{"enabled":true}'

signed=$("${CURL[@]}" -X POST "${API}/keys/${key_id}/sign" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"message\":\"${SIGHASH}\",\"to\":\"${DEST}\",\"value\":\"${SEND}\",\"chainId\":0}")
SIG=$(echo "${signed}" | jqp 'd.get("signature","")')
[[ -n "${SIG}" ]] || fail "signing failed: ${signed}"
PARTIES=$(echo "${signed}" | jqp 'd["parties"]')
# 65 bytes: r (32) || s (32) || v (1). Bitcoin does not use the recovery id.
R=${SIG:0:64}
SS=${SIG:64:64}
echo "    signed by parties ${PARTIES}"

echo "==> assembling the Bitcoin transaction"
ASSEMBLED=$(python3 -c "
import json,sys
plan = json.loads(sys.stdin.read())
print(json.dumps({'plan': plan,
                  'signatures': [{'r': '${R}', 's': '${SS}'}],
                  'pubkey_hex': '${PUBKEY}'}))
" <<<"${PLAN}" | btctool assemble)
RAW=$(echo "${ASSEMBLED}" | jqp 'd["raw_tx_hex"]')
EXPECTED_TXID=$(echo "${ASSEMBLED}" | jqp 'd["txid"]')
echo "    assembled ${EXPECTED_TXID}"

echo "==> broadcasting to Bitcoin Core"
TXID=$(btc sendrawtransaction "${RAW}") \
  || fail "Bitcoin Core rejected the transaction"
[[ "${TXID}" == "${EXPECTED_TXID}" ]] \
  || fail "Core computed txid ${TXID}, we predicted ${EXPECTED_TXID}"
echo "    accepted as ${TXID}"

echo "==> mining it"
btc generatetoaddress 1 "${MINER}" >/dev/null
CONFS=$(btc getrawtransaction "${TXID}" 1 | jqp 'd.get("confirmations",0)')
[[ "${CONFS}" -ge 1 ]] || fail "the transaction was accepted but never mined"

# Core's own view of where the money went.
RECEIVED=$(btc getrawtransaction "${TXID}" 1 | python3 -c "
import sys, json
tx = json.load(sys.stdin)
for out in tx['vout']:
    spk = out['scriptPubKey']
    if '${DEST}' in (spk.get('addresses') or [spk.get('address')]):
        print(int(round(out['value'] * 1e8)))
        break
else:
    print(0)
")
[[ "${RECEIVED}" -eq "${SEND}" ]] \
  || fail "the recipient received ${RECEIVED} sats, expected ${SEND}"

echo
echo "PASS: a Bitcoin transaction spending from ${BTC_ADDR} -- an address"
echo "      derived from a 2-of-3 threshold key whose private key does not"
echo "      exist -- was signed by parties ${PARTIES}, accepted by Bitcoin"
echo "      Core, mined (${CONFS} confirmation), and moved ${RECEIVED} sats."
