#!/usr/bin/env bash
#
# Sends Bitcoin the way a customer would.
#
# bitcoin-drill.sh already proves the cryptography: a threshold key whose
# private key does not exist can produce a transaction Bitcoin Core accepts.
# But it proves it the way an engineer would, not the way a customer can. It
# computes the sighash itself, on the host, with a Go tool the platform does
# not expose -- and it feeds that digest to POST /keys/:id/sign, a route
# gated behind a per-tenant capability precisely because a digest is opaque
# to policy.
#
# So Bitcoin was cryptographically finished and commercially unusable. A
# customer holding an API key could derive a Bitcoin address and could not
# spend from it, unless they were prepared to implement coin selection, fee
# estimation, BIP143 and witness assembly themselves.
#
# This drill uses nothing but the public API and bitcoind. Everything that
# used to be the customer's problem -- which coins, what fee, what change,
# what to hash, how to package it, where to send it -- happens inside the
# platform. The only thing done from outside is funding the address, which
# is what a counterparty would do.
#
# The tenant deliberately does NOT have raw digest signing enabled. If this
# drill passes, Bitcoin works on the default capabilities.
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
CURL=(curl -sS --noproxy '*')

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

BPOD="$(kubectl -n "${NS}" get pod -l app=bitcoind --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
[[ -n "${BPOD}" ]] || fail "bitcoind is not running; kubectl apply -f bitcoind-regtest.yaml"

btc() {
  kubectl -n "${NS}" exec "${BPOD}" -- bitcoin-cli -regtest \
    -rpcuser=ofb -rpcpassword=ofb-regtest "$@" 2>/dev/null
}

echo "==> bitcoind regtest, block $(btc getblockcount)"
btc createwallet drill >/dev/null 2>&1 || btc loadwallet drill >/dev/null 2>&1 || true
MINER=$(btc getnewaddress)
# Coinbase output needs 100 confirmations before it can be spent, so a
# shorter chain leaves the wallet with a balance it cannot actually use.
[[ "$(btc getblockcount)" -ge 101 ]] || btc generatetoaddress 101 "${MINER}" >/dev/null

echo "==> provisioning a 2-of-3 Bitcoin key"
S=$(date +%s)
customer=$("${CURL[@]}" -X POST "${API}/admin/customers" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"email\":\"btcapi-${S}@example.com\",\"name\":\"btcapi-${S}\",\"tier\":\"enterprise\"}")
api_key=$(echo "${customer}" | jqp 'd["api_key"]')
customer_id=$(echo "${customer}" | jqp 'd["customer_id"]')

# Note what is NOT done here: no call to /admin/customers/<id>/raw-digest-signing.
# This tenant cannot sign an opaque digest, which is the whole point.

created=$("${CURL[@]}" -X POST "${API}/keys" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"blockchain\":\"bitcoin\",\"threshold\":2,\"total_parties\":3,\"name\":\"btcapi-${S}\"}")
key_id=$(echo "${created}" | jqp 'd["id"]')
for _ in $(seq 1 120); do
  key=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}")
  status=$(echo "${key}" | jqp 'd.get("status")')
  [[ "${status}" != "pending_dkg" ]] && break
  sleep 5
done
[[ "${status}" == "active" ]] || fail "key did not activate: ${key}"
echo "    key ${key_id}"

echo "==> asking the platform where to deposit"
# The question a custody platform has to be able to answer. Before this
# route existed a customer could provision a Bitcoin key and had no
# supported way to learn its address.
addrs=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}/addresses")
DEPOSIT=$(echo "${addrs}" | jqp 'd["addresses"]["preferred"]')
LEGACY=$(echo "${addrs}" | jqp 'd["addresses"]["legacy"]')
[[ -n "${DEPOSIT}" && "${DEPOSIT}" != "None" ]] || fail "no deposit address: ${addrs}"
echo "    preferred ${DEPOSIT}"
echo "    legacy    ${LEGACY}"

echo "==> funding it, twice, at both addresses"
# Two deposits at the two encodings of the same key. This is the case a
# platform that tracked only one address form would get wrong: the balance
# would read low and a spend needing both coins would fail with
# "insufficient funds" against a wallet that plainly has the money.
btc -rpcwallet=drill sendtoaddress "${DEPOSIT}" 0.3 >/dev/null || fail "funding the segwit address failed"
btc -rpcwallet=drill sendtoaddress "${LEGACY}"  0.3 >/dev/null || fail "funding the legacy address failed"
btc generatetoaddress 1 "${MINER}" >/dev/null
echo "    0.6 BTC deposited across both addresses"

echo "==> spending, through the public API"
DEST=$(btc getnewaddress)
# 45,000,000 sats = 0.45 BTC: more than either deposit alone, so the
# platform has to select both coins and pay for two inputs.
SEND=45000000
spend=$("${CURL[@]}" -w '\n%{http_code}' -X POST "${API}/keys/${key_id}/bitcoin-transactions" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"destination\":\"${DEST}\",\"amount\":\"${SEND}\",\"feeRate\":5}")
code=$(echo "${spend}" | tail -1)
body=$(echo "${spend}" | sed '$d')
[[ "${code}" == "200" ]] || fail "the spend returned ${code}: ${body}"

TXID=$(echo "${body}" | jqp 'd["txid"]')
FEE=$(echo "${body}" | jqp 'd["fee"]')
CHANGE=$(echo "${body}" | jqp 'd["change"]')
INPUTS=$(echo "${body}" | jqp 'd["inputs"]')
VSIZE=$(echo "${body}" | jqp 'd["virtual_size"]')
PARTIES=$(echo "${body}" | jqp 'd["parties"]')
BEFORE=$(echo "${body}" | jqp 'd["balance_before"]')
echo "    ${INPUTS} input(s), ${VSIZE} vbytes, fee ${FEE} sats, change ${CHANGE} sats"
echo "    signed by parties ${PARTIES}"
echo "    txid ${TXID}"

[[ "${INPUTS}" -eq 2 ]] \
  || fail "selected ${INPUTS} input(s); the spend needs both deposits, so only one address was seen"
[[ "${BEFORE}" -eq 60000000 ]] \
  || fail "the platform saw a balance of ${BEFORE}, not the 60000000 sats deposited"

echo "==> checking Bitcoin Core agrees"
# The platform said it broadcast. Core is the judge of whether it did, and
# of whether the bytes were valid -- a transaction can be assembled
# perfectly and still be refused by the network.
btc getrawtransaction "${TXID}" >/dev/null 2>&1 \
  || fail "Core has never heard of ${TXID}; the platform reported a broadcast that did not happen"

btc generatetoaddress 1 "${MINER}" >/dev/null
CONFS=$(btc getrawtransaction "${TXID}" 1 | jqp 'd.get("confirmations",0)')
[[ "${CONFS}" -ge 1 ]] || fail "the transaction was accepted but never mined"

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

# The fee actually paid, computed from the chain rather than from what the
# platform claimed. A wallet that quotes one fee and pays another is the
# expensive kind of wrong, and the arithmetic only closes if the change
# output was really created.
ACTUAL_FEE=$(btc getrawtransaction "${TXID}" 1 | python3 -c "
import sys, json
tx = json.load(sys.stdin)
print(60000000 - sum(int(round(o['value'] * 1e8)) for o in tx['vout']))
")
[[ "${ACTUAL_FEE}" -eq "${FEE}" ]] \
  || fail "the platform quoted a fee of ${FEE} sats and the chain paid ${ACTUAL_FEE}"

echo "==> checking the platform recorded what it signed"
# The record a custody platform exists to keep, and one nothing was
# writing: signing_requests was read by GET /keys/:id/details, by
# compliance's structuring signal, and by settlement, and never written by
# anything. A signature that leaves no trace of who produced it is not
# custody, it is a signing oracle.
history=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}/details")
RECORDED=$(echo "${history}" | python3 -c "
import sys, json
d = json.load(sys.stdin)
rows = d.get('signing_requests') or d.get('signingRequests') or []
print(len(rows))
")
# One row per ceremony, and a two-input spend is two signatures.
[[ "${RECORDED}" -eq "${INPUTS}" ]] \
  || fail "the platform signed ${INPUTS} digest(s) and recorded ${RECORDED}"

PARTIES_RECORDED=$(echo "${history}" | python3 -c "
import sys, json
d = json.load(sys.stdin)
rows = d.get('signing_requests') or d.get('signingRequests') or []
print(sum(1 for r in rows if r.get('signing_parties')))
")
# Which parties signed is the first question an auditor asks: a threshold
# signature only means something if you can say which shares combined.
[[ "${PARTIES_RECORDED}" -eq "${INPUTS}" ]] \
  || fail "${PARTIES_RECORDED} of ${RECORDED} recorded signatures name the committee that produced them"
echo "    ${RECORDED} signature(s) recorded, each naming its committee"

echo "==> checking a retried request does not sign twice"
# Every signing route accepted an idempotencyKey and ignored it, so a
# client retrying a timed-out call ran a second ceremony -- and here, would
# have broadcast a second transaction.
IDEM="drill-${S}-retry"
first=$("${CURL[@]}" -X POST "${API}/keys/${key_id}/bitcoin-transactions" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"destination\":\"${DEST}\",\"amount\":\"100000\",\"feeRate\":5,\"idempotencyKey\":\"${IDEM}\"}")
first_txid=$(echo "${first}" | jqp 'd.get("txid","")')
[[ -n "${first_txid}" ]] || fail "the first idempotent spend failed: ${first}"

second=$("${CURL[@]}" -X POST "${API}/keys/${key_id}/bitcoin-transactions" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"destination\":\"${DEST}\",\"amount\":\"100000\",\"feeRate\":5,\"idempotencyKey\":\"${IDEM}\"}")
second_txid=$(echo "${second}" | jqp 'd.get("txid","")')
[[ "${second_txid}" == "${first_txid}" ]] \
  || fail "a retry with the same idempotency key produced ${second_txid}, not ${first_txid}: a second transaction was signed"
echo "    the retry returned the same transaction ${first_txid}"
btc generatetoaddress 1 "${MINER}" >/dev/null

echo "==> checking the change came back to the key"
sleep 2
after=$("${CURL[@]}" -X POST "${API}/keys/${key_id}/bitcoin-transactions" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"destination\":\"${DEST}\",\"amount\":\"1\",\"feeRate\":5}" \
  -o /dev/null -w '%{http_code}' || true)
# A spend of one satoshi is below the dust threshold and must be refused as
# a client error. This is here because the first run of this drill got a 503
# instead: the platform planned the transaction, ran a threshold ceremony
# for every input, assembled it, and only then had Bitcoin Core reject it as
# dust -- the most expensive possible way to discover a mistake that costs
# nothing to catch, reported as though the platform were down.
[[ "${after}" == "400" ]] \
  || fail "a 1-sat spend returned ${after}; dust must be refused as a client error, before any ceremony runs"

# The change from the first spend, less the second spend and its fee. The
# point is that the change output was really created and is really
# spendable -- it was, since the retry spend came out of it.
remaining=$(btc scantxoutset start "[\"addr(${DEPOSIT})\"]" | jqp 'int(round(d["total_amount"] * 1e8))')
[[ "${remaining}" -gt 0 && "${remaining}" -lt "${CHANGE}" ]] \
  || fail "the key holds ${remaining} sats; expected less than the ${CHANGE} sats of change, and more than nothing"

echo
echo "PASS: a customer with an ordinary API key -- and without the raw-digest"
echo "      capability -- deposited 0.6 BTC across both of the key's address"
echo "      forms, called one endpoint, and moved ${RECEIVED} sats. The"
echo "      platform selected ${INPUTS} inputs, paid a ${FEE} sat fee the chain"
echo "      confirms exactly, returned ${CHANGE} sats of change to the key,"
echo "      and had it signed by parties ${PARTIES} of a key whose private key"
echo "      does not exist. Bitcoin Core mined it (${CONFS} confirmation)."
echo
echo "      Each of the ${RECORDED} signatures is recorded with the committee that"
echo "      produced it, and a retry under the same idempotency key returned the"
echo "      original transaction rather than signing a second one."
