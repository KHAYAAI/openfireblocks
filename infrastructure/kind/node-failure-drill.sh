#!/usr/bin/env bash
#
# Drains a node hosting an MPC party and checks that a 2-of-3 committee
# keeps signing.
#
# The design says losing one party of three is survivable. That is an
# argument, not evidence, and arguments of this shape are usually wrong in
# some specific way that only shows up when you actually take the node away.
# This one was: the gateway picked the committee as "the first `threshold`
# parties by id", so a 2-of-3 key was really a fixed 2-of-2 -- losing party
# 1 failed every signature while party 3 sat idle holding a good share. The
# fault tolerance was in the comment, not the code.
#
# The drill is deliberately hostile in its choice of victim: it drains the
# node hosting a party the system would *prefer* to use, because draining
# the spare proves nothing.
#
# Requires a deployed cluster. The drill opens its own tunnel to the
# gateway and reopens it after the drain, so no port-forward is needed
# beforehand.
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
CURL=(curl -sS --noproxy '*')

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

DRAINED_NODE=""
PF_PID=""
cleanup() {
  [[ -n "${PF_PID}" ]] && kill "${PF_PID}" >/dev/null 2>&1 || true
  if [[ -n "${DRAINED_NODE}" ]]; then
    echo "==> uncordoning ${DRAINED_NODE}"
    kubectl uncordon "${DRAINED_NODE}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

# The drill manages its own tunnel to the gateway.
#
# Draining a node evicts whatever was on it, and that can include the API
# gateway itself -- a port-forward is bound to one pod and does not follow
# the replacement, so it simply stops answering. That is an artefact of how
# this cluster is reached, not a property of the platform, and mistaking it
# for "signing failed" would make the drill lie in the direction that
# flatters us.
open_tunnel() {
  [[ -n "${PF_PID}" ]] && kill "${PF_PID}" >/dev/null 2>&1 || true
  kubectl -n "${NS}" rollout status deploy/ofb-openfireblocks-api-gateway \
    --timeout=300s >/dev/null 2>&1 || true
  kubectl -n "${NS}" port-forward svc/ofb-openfireblocks-api-gateway 3000:3000 \
    >/dev/null 2>&1 &
  PF_PID=$!
  for _ in $(seq 1 60); do
    "${CURL[@]}" -m 2 -o /dev/null "${API}/health" 2>/dev/null && return 0
    sleep 1
  done
  fail "the API gateway never came back after the drain"
}

open_tunnel

echo "==> provisioning a 2-of-3 threshold key"
S=$(date +%s)
customer=$("${CURL[@]}" -X POST "${API}/admin/customers" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"email\":\"drill-${S}@example.com\",\"name\":\"drill-${S}\",\"tier\":\"enterprise\"}")
api_key=$(echo "${customer}" | jqp 'd["api_key"]')
created=$("${CURL[@]}" -X POST "${API}/keys" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"blockchain\":\"ethereum\",\"threshold\":2,\"total_parties\":3,\"name\":\"drill-${S}\"}")
key_id=$(echo "${created}" | jqp 'd["id"]')
for _ in $(seq 1 120); do
  key=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}")
  status=$(echo "${key}" | jqp 'd.get("status")')
  [[ "${status}" != "pending_dkg" ]] && break
  sleep 5
done
[[ "${status}" == "active" ]] || fail "key did not activate: ${key}"
ADDRESS=$(echo "${key}" | jqp 'd["address"]')
echo "    ${ADDRESS}"

# A signed transaction is the check throughout, rather than a raw digest:
# it proves the surviving committee produces a signature that still recovers
# to this key's address. A signature that verifies against the wrong key
# would otherwise look like success.
sign_once() {
  local nonce="$1"
  "${CURL[@]}" -X POST "${API}/keys/${key_id}/transactions" \
    -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
    -d "{\"to\":\"0x70997970C51812dc3A010C7d01b50e0d17dc79C8\",\"value\":\"1000\",
         \"gasLimit\":21000,\"nonce\":${nonce},\"chainId\":11155111,
         \"gasPrice\":\"20000000000\"}"
}

echo "==> baseline: signing with all three parties up"
baseline=$(sign_once 0)
from=$(echo "${baseline}" | jqp 'd.get("from","")')
[[ "${from}" == "${ADDRESS}" ]] || fail "baseline signature is not from ${ADDRESS}: ${baseline}"
committee=$(echo "${baseline}" | jqp 'd["parties"]')
echo "    signed by parties ${committee}, sender ${from}"

# The victim: a party the system just chose, on a node that does not pin
# any stateful workload.
#
# Two constraints, and they pull against each other.
#
# Hostility: draining the node hosting a party the committee did *not* use
# proves nothing, so the victim must be a committee member.
#
# Isolation: this cluster's PersistentVolumes come from kind's local-path
# provisioner, which makes them node-local -- a PV is physically pinned to
# the node that created it (`kubectl get pv -o jsonpath=...nodeAffinity`).
# Draining the node holding Vault's or Postgres's volume does not test
# threshold signing; it tests what happens when the platform loses its
# database or its secrets backend, which is a different question with an
# obvious answer. Mixing them in would make this drill fail for a reason
# that has nothing to do with the property it exists to check.
#
# So it drains a committee member's node that carries no bound volume. With
# three parties spread across three workers and one node holding storage,
# such a node exists.
pinned_nodes() {
  kubectl get pv -o jsonpath='{range .items[*]}{.spec.nodeAffinity.required.nodeSelectorTerms[0].matchExpressions[0].values[0]}{"\n"}{end}' 2>/dev/null | sort -u
}
PINNED="$(pinned_nodes)"

VICTIM=""
DRAINED_NODE=""
for candidate in $(echo "${committee}" | tr -d '[],'); do
  node=$(kubectl -n "${NS}" get pod -l "openfireblocks.com/party-id=${candidate}" \
    --field-selector=status.phase=Running -o jsonpath='{.items[0].spec.nodeName}' 2>/dev/null)
  [[ -z "${node}" ]] && continue
  if echo "${PINNED}" | grep -qx "${node}"; then
    echo "    skipping party-${candidate}: its node ${node} pins a stateful volume"
    continue
  fi
  VICTIM="${candidate}"
  DRAINED_NODE="${node}"
  break
done

if [[ -z "${VICTIM}" ]]; then
  fail "every committee member shares a node with node-pinned storage; \
draining any of them would test storage, not threshold signing"
fi

echo "==> draining ${DRAINED_NODE}, which hosts party-${VICTIM} (a party in the committee above)"
kubectl cordon "${DRAINED_NODE}" >/dev/null
# --force because the party pods are bare Deployments with local emptyDir
# state; --delete-emptydir-data acknowledges the certificate volume goes
# with it, which is the point -- the replacement cannot come back on this
# node because it is cordoned.
kubectl drain "${DRAINED_NODE}" \
  --ignore-daemonsets --delete-emptydir-data --force \
  --timeout=180s >/dev/null 2>&1 || true

for _ in $(seq 1 60); do
  running=$(kubectl -n "${NS}" get pod -l "openfireblocks.com/party-id=${VICTIM}" \
    --field-selector=status.phase=Running -o name 2>/dev/null | wc -l)
  [[ "${running}" -eq 0 ]] && break
  sleep 2
done
echo "    party-${VICTIM} is gone; $(kubectl -n "${NS}" get pod -l app.kubernetes.io/component=mpc-party \
  --field-selector=status.phase=Running -o name | wc -l) of 3 parties remain"

# Vault was deliberately not disturbed, and that is a limitation worth
# stating rather than hiding.
#
# It now runs on a PersistentVolumeClaim rather than in memory, so it
# survives its *pod* being destroyed -- verified directly. It does not
# survive its *node* going away on this cluster, because kind's local-path
# volumes are node-pinned: the data is physically on that machine, and the
# pod cannot be scheduled anywhere else. Draining Vault's node leaves it
# permanently Pending and takes the platform with it.
#
# That is not fixable by manifest on a single-host cluster. The real answer
# is a Raft cluster of three Vault replicas, one per node, each with its own
# volume, so losing a node leaves quorum -- which is the production
# architecture and a change of a different size to this drill.
echo "==> Vault was not on the drained node (see the note in this script)"
if kubectl -n "${NS}" get pod vault-0 -o jsonpath='{.status.phase}' 2>/dev/null | grep -q Running; then
  echo "    vault-0 still Running"
else
  fail "vault-0 is not running; the drill disturbed the secrets backend and is no longer measuring signing"
fi

# The surviving parties must be back to serving before signing means
# anything -- a certificate reissue restarts them.
for _ in $(seq 1 120); do
  up=$(kubectl -n "${NS}" get pods -l app.kubernetes.io/component=mpc-party \
    --no-headers 2>/dev/null | grep -c '2/2' || true)
  [[ "${up}" -ge 2 ]] && break
  sleep 2
done

# The gateway may itself have been evicted by the drain; reconnect before
# concluding anything about signing.
open_tunnel

echo "==> signing again with the surviving parties"
started=$(date +%s%3N)
degraded=$(sign_once 1)
elapsed=$(( $(date +%s%3N) - started ))
from=$(echo "${degraded}" | jqp 'd.get("from","")')
if [[ "${from}" != "${ADDRESS}" ]]; then
  fail "signing failed with one party down -- a 2-of-3 key that cannot lose a party is a 2-of-2: ${degraded}"
fi
survivors=$(echo "${degraded}" | jqp 'd["parties"]')
echo "    signed by parties ${survivors} in ${elapsed}ms, sender ${from}"

echo "${survivors}" | grep -q "${VICTIM}" \
  && fail "the drained party ${VICTIM} appears in the committee ${survivors}"

echo "==> confirming the signature is genuinely valid, not just returned"
raw=$(echo "${degraded}" | jqp 'd["raw_transaction"]')
recovered=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../services/api-gateway" && node -e "
const { Transaction } = require('ethers');
console.log(Transaction.from('${raw}').from);
")
[[ "${recovered}" == "${ADDRESS}" ]] \
  || fail "the degraded signature recovers to ${recovered}, not ${ADDRESS}"
echo "    recovers to ${recovered}"

echo
echo "PASS: with ${DRAINED_NODE} drained and party-${VICTIM} gone, a 2-of-3 key"
echo "      still signed in ${elapsed}ms using parties ${survivors}, and the"
echo "      signature recovers to ${ADDRESS}."
