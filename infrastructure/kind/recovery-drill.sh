#!/usr/bin/env bash
#
# Destroys every MPC party and proves the key survives.
#
# This is the drill behind docs/deployment/KEY-RECOVERY.md, and behind the
# only claim that matters to a bank's risk committee evaluating a small
# vendor: what happens to our assets if you disappear. Self-hosting is the
# strongest possible answer to that question, and an answer that is
# asserted rather than executed is not one a control function accepts.
#
# So the drill does not simulate loss. It deletes the party pods with no
# grace period, which discards everything the ceremonies held in memory --
# the shares, the committee identities, the threshold, the curve, the
# refresh epoch -- and then rebuilds the parties from what was sealed in
# Vault and signs with them.
#
# The step that makes it honest is the one in the middle. Between the
# destroy and the restore the drill *requires* signing to fail. Without
# that, a drill where state quietly survived the restart would pass while
# proving nothing at all, and it would pass in exactly the case where the
# recovery path is broken -- which is the case it exists to catch.
#
# The final assertion is not "the parties came back". It is that a
# signature produced by the restored committee recovers to the address the
# original DKG derived. A recovery that produces a different address has
# recovered nothing.
#
# Usage:
#   ./recovery-drill.sh
#       Provisions a fresh 2-of-3 key and drills it. This is what CI runs.
#
#   ./recovery-drill.sh --ceremony-id C --key-id K --api-key KEY
#       Drills an existing key. For rehearsing against a real deployment,
#       which section 6 of KEY-RECOVERY.md asks you to do annually.
#
# Requires a deployed cluster. The drill opens its own tunnels and cleans
# them up.
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
PARTY_COUNT="${PARTY_COUNT:-3}"
PARTY_PORT="${PARTY_PORT:-7000}"
# Local ports for the per-party tunnels. Well clear of the gateway's 3000.
PARTY_TUNNEL_BASE="${PARTY_TUNNEL_BASE:-17000}"
CURL=(curl -sS --noproxy '*')

CEREMONY_ID=""
KEY_ID=""
API_KEY=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ceremony-id) CEREMONY_ID="$2"; shift 2 ;;
    --key-id)      KEY_ID="$2"; shift 2 ;;
    --api-key)     API_KEY="$2"; shift 2 ;;
    -h|--help)     sed -n '2,36p' "$0" | sed 's/^# \?//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKDIR="$(mktemp -d)"
PIDS=()
cleanup() {
  for pid in "${PIDS[@]:-}"; do kill "${pid}" >/dev/null 2>&1 || true; done
  rm -rf "${WORKDIR}"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Reaching the parties
# ---------------------------------------------------------------------------

# With mTLS on -- which is how this platform is meant to run, and how the
# kind deployment runs -- a party's ceremony port requires a verified
# client certificate. The drill borrows the temporal worker's, because that
# is the identity that already drives ceremonies in normal operation: if
# the restore endpoint would refuse it, the drill should find that out
# rather than paper over it with a certificate minted for the occasion.
#
# Note what is *not* borrowed: a party certificate. Sender binding
# (services/mpc-party/peer_identity.go) attributes relayed protocol
# messages to the common name of the certificate that carried them, so a
# drill holding a party's identity could inject traffic as that party. It
# has no business being able to.
MTLS=0
setup_client_identity() {
  local pod
  pod=$(kubectl -n "${NS}" get pod -l app.kubernetes.io/component=temporal-worker \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [[ -z "${pod}" ]]; then
    echo "    no temporal worker pod; assuming the parties are in plaintext"
    return
  fi
  local base=/etc/openfireblocks/mtls
  for f in tls.crt tls.key ca.crt; do
    if ! kubectl -n "${NS}" exec "${pod}" -c temporal-worker -- cat "${base}/${f}" \
      > "${WORKDIR}/${f}" 2>/dev/null; then
      echo "    no client certificate in the worker; assuming the parties are in plaintext"
      return
    fi
  done
  [[ -s "${WORKDIR}/tls.crt" ]] || return
  MTLS=1
  echo "    borrowed the temporal worker's client certificate"
}

party_tunnel_port() { echo $((PARTY_TUNNEL_BASE + $1)); }

open_party_tunnels() {
  for id in $(seq 1 "${PARTY_COUNT}"); do
    local port
    port=$(party_tunnel_port "${id}")
    kubectl -n "${NS}" port-forward "svc/party-${id}" "${port}:${PARTY_PORT}" \
      >/dev/null 2>&1 &
    PIDS+=($!)
  done
  # Ports take a moment to bind; a request that races the forward looks
  # exactly like a party that is down.
  sleep 3
}

# party_curl <id> <curl args...> -- speaks to party <id> over its tunnel,
# with the hostname the server certificate was issued for.
party_curl() {
  local id="$1"; shift
  local port
  port=$(party_tunnel_port "${id}")
  if [[ "${MTLS}" == "1" ]]; then
    # --resolve rather than -k: the drill verifies the party's certificate
    # as a peer would. Skipping that would let the drill pass against a
    # party presenting any certificate at all.
    "${CURL[@]}" \
      --cert "${WORKDIR}/tls.crt" --key "${WORKDIR}/tls.key" --cacert "${WORKDIR}/ca.crt" \
      --resolve "party-${id}.internal:${port}:127.0.0.1" \
      "$@"
  else
    "${CURL[@]}" "$@"
  fi
}

party_url() {
  local id="$1" path="$2" port
  port=$(party_tunnel_port "${id}")
  if [[ "${MTLS}" == "1" ]]; then
    echo "https://party-${id}.internal:${port}${path}"
  else
    echo "http://127.0.0.1:${port}${path}"
  fi
}

# The peers map a restored party needs in order to rebuild its committee.
# In-cluster service DNS, because the parties talk to each other, not
# through the drill's tunnels.
peers_json() {
  local out="" id
  for id in $(seq 1 "${PARTY_COUNT}"); do
    [[ -n "${out}" ]] && out="${out},"
    out="${out}\"${id}\":\"https://party-${id}:${PARTY_PORT}\""
  done
  [[ "${MTLS}" == "1" ]] || out="${out//https:/http:}"
  echo "{${out}}"
}

# ---------------------------------------------------------------------------
# Step 1 -- a key to lose
# ---------------------------------------------------------------------------

echo "==> checking the gateway"
"${CURL[@]}" "${API}/health/ready" | grep -q '"postgres":"ok"' \
  || fail "the gateway is not ready; is the cluster up and port-forwarded?"

if [[ -z "${CEREMONY_ID}" ]]; then
  echo "==> provisioning a 2-of-3 key to destroy"
  suffix=$(date +%s)
  customer=$("${CURL[@]}" -X POST "${API}/admin/customers" \
    -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
    -d "{\"email\":\"recovery-${suffix}@example.com\",\"name\":\"recovery-${suffix}\",\"tier\":\"enterprise\"}")
  API_KEY=$(echo "${customer}" | jqp 'd["api_key"]') || fail "no api_key: ${customer}"
  customer_id=$(echo "${customer}" | jqp 'd["customer_id"]')

  created=$("${CURL[@]}" -X POST "${API}/keys" \
    -H 'Content-Type: application/json' -H "x-api-key: ${API_KEY}" \
    -d "{\"blockchain\":\"ethereum\",\"threshold\":2,\"total_parties\":${PARTY_COUNT},\"name\":\"recovery-${suffix}\"}")
  KEY_ID=$(echo "${created}" | jqp 'd["id"]') || fail "key not created: ${created}"
  CEREMONY_ID=$(echo "${created}" | jqp 'd["ceremony_id"]')

  # Safe-prime generation dominates and is a randomised search, so this is
  # minutes rather than seconds on a cold pool.
  for _ in $(seq 1 120); do
    key=$("${CURL[@]}" -H "x-api-key: ${API_KEY}" "${API}/keys/${KEY_ID}")
    status=$(echo "${key}" | jqp 'd.get("status")')
    [[ "${status}" != "pending_dkg" ]] && break
    sleep 5
  done
  [[ "${status}" == "active" ]] || fail "DKG did not complete: ${key}"

  # Raw-digest signing is gated per tenant; the drill signs a digest, so it
  # has to be granted the same way smoke-test.sh grants it.
  "${CURL[@]}" -o /dev/null -X PUT "${API}/admin/customers/${customer_id}/raw-digest-signing" \
    -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
    -d '{"enabled":true}'
else
  [[ -n "${KEY_ID}" && -n "${API_KEY}" ]] \
    || fail "--ceremony-id needs --key-id and --api-key; the drill signs through the public API"
  key=$("${CURL[@]}" -H "x-api-key: ${API_KEY}" "${API}/keys/${KEY_ID}")
fi

ORIGINAL_ADDRESS=$(echo "${key}" | jqp 'd["address"]')
[[ -n "${ORIGINAL_ADDRESS}" ]] || fail "the key has no address: ${key}"
echo "    key ${KEY_ID} at ${ORIGINAL_ADDRESS}"
echo "    ceremony ${CEREMONY_ID}"

# ---------------------------------------------------------------------------
# Step 2 -- what was sealed
# ---------------------------------------------------------------------------

# Read the sealed material before destroying anything. If the shares were
# never written, or were written without the ceremony context, the restore
# cannot work and the drill should say why here rather than fail later with
# a less specific error. This is the failure mode the last row of
# KEY-RECOVERY.md section 5 describes: a ceremony that completed with no
# VAULT_ADDR set leaves shares that only ever existed in memory.
echo "==> checking what is sealed in Vault"
vault_pod=$(kubectl -n "${NS}" get pod -l app=vault \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
[[ -n "${vault_pod}" ]] || fail "no Vault pod found in ${NS}; the shares cannot have been sealed"

# The drill reads the shares as an operator recovering them would: with a
# token, out of Vault, from outside the parties. It never asks a party
# what it holds -- a party that has lost its memory cannot tell you, and
# that is the whole scenario.
VAULT_ROOT_TOKEN="${VAULT_ROOT_TOKEN:-$(kubectl -n "${NS}" get secret dev-dependency-credentials \
  -o jsonpath='{.data.vault-root-token}' 2>/dev/null | base64 -d || true)}"
[[ -n "${VAULT_ROOT_TOKEN}" ]] \
  || fail "no Vault token; set VAULT_ROOT_TOKEN, or deploy the dev credentials secret"

for id in $(seq 1 "${PARTY_COUNT}"); do
  path="${VAULT_KV_MOUNT:-secret}/openfireblocks/mpc-party/party-${id}/${CEREMONY_ID}"
  sealed=$(kubectl -n "${NS}" exec "${vault_pod}" -- \
    env "VAULT_TOKEN=${VAULT_ROOT_TOKEN}" VAULT_ADDR=http://127.0.0.1:8200 \
    vault kv get -format=json "${path}" 2>/dev/null) \
    || fail "party ${id} has no sealed share at ${path}"
  echo "${sealed}" | grep -q 'save_data' \
    || fail "party ${id}'s entry at ${path} holds no share"
  echo "${sealed}" | grep -q 'ceremony_context' \
    || fail "party ${id}'s share is sealed without its ceremony context; it cannot be restored (see KEY-RECOVERY.md section 5)"
done
echo "    all ${PARTY_COUNT} shares sealed, each with its ceremony context"

# ---------------------------------------------------------------------------
# Step 3 -- a baseline signature
# ---------------------------------------------------------------------------

MESSAGE=""

# A fresh digest for each phase of the drill.
#
# Reusing one digest would let a cached or replayed signature stand in for
# a real one: the post-restore signature could be the baseline signature
# handed back, and the drill would report a successful recovery having
# proved only that something remembered an earlier answer. Every phase
# signs something nothing has seen before.
new_message() {
  MESSAGE="$(printf 'recovery-drill-%s-%s' "$1" "$(date +%s%N)" \
    | sha256sum | cut -d' ' -f1)"
}

# try_sign echoes a signature, or nothing if the key could not sign. It
# never fails the drill: step 5 needs signing to fail, and a helper that
# exits on a failed signature could not express that.
try_sign() {
  local signed
  signed=$("${CURL[@]}" -X POST "${API}/keys/${KEY_ID}/sign" \
    -H 'Content-Type: application/json' -H "x-api-key: ${API_KEY}" \
    -d "{\"message\":\"${MESSAGE}\",\"to\":\"0x1111111111111111111111111111111111111111\",\"value\":\"1000\",\"chainId\":11155111}" \
    2>/dev/null) || return 0
  echo "${signed}" | jqp 'd.get("signature","")' 2>/dev/null || true
}

sign_and_recover() {
  local label="$1" signature recovered
  new_message "${label}"
  signature=$(try_sign)
  [[ -n "${signature}" ]] || return 1
  recovered=$(cd "$(dirname "${BASH_SOURCE[0]}")/recover" && go run . "${MESSAGE}" "${signature}")
  [[ "${recovered,,}" == "${ORIGINAL_ADDRESS,,}" ]] \
    || fail "${label}: the signature recovers to ${recovered}, not ${ORIGINAL_ADDRESS}"
  echo "    ${label}: recovered ${recovered}"
}

echo "==> baseline: signing before the parties are destroyed"
sign_and_recover "baseline" || fail "the key could not sign before the drill started"

# ---------------------------------------------------------------------------
# Step 4 -- destroy the parties
# ---------------------------------------------------------------------------

echo "==> destroying every party"
# --force --grace-period=0 deliberately. A graceful stop is not the
# scenario: the scenario is the hosts going away. Nothing in this platform
# writes ceremony state on shutdown, and if it ever did, this drill should
# fail rather than quietly start passing for the wrong reason.
kubectl -n "${NS}" delete pod -l app.kubernetes.io/component=mpc-party \
  --force --grace-period=0 >/dev/null 2>&1 || true

for id in $(seq 1 "${PARTY_COUNT}"); do
  kubectl -n "${NS}" rollout status "deploy/party-${id}" --timeout=300s >/dev/null \
    || fail "party ${id} did not come back"
done
echo "    all ${PARTY_COUNT} parties restarted with empty memory"

setup_client_identity
open_party_tunnels

# ---------------------------------------------------------------------------
# Step 5 -- prove the loss is real
# ---------------------------------------------------------------------------

# The step that stops this drill from lying. If the parties still know the
# ceremony, nothing was destroyed and everything after this proves nothing.
echo "==> confirming the parties have genuinely lost the key"
for id in $(seq 1 "${PARTY_COUNT}"); do
  code=$(party_curl "${id}" -o /dev/null -w '%{http_code}' \
    "$(party_url "${id}" "/tss/keygen/status?ceremony_id=${CEREMONY_ID}")")
  [[ "${code}" == "404" ]] \
    || fail "party ${id} still knows ceremony ${CEREMONY_ID} (HTTP ${code}); the destroy step did not destroy anything"
done

new_message "post-destroy"
if [[ -n "$(try_sign)" ]]; then
  fail "the key produced a signature after every party was destroyed; state survived that should not have, and this drill proves nothing until that is explained"
fi
echo "    every party returns 404 for the ceremony, and signing fails"

# ---------------------------------------------------------------------------
# Step 6 -- restore
# ---------------------------------------------------------------------------

# With ceremonyAuthorizer enabled -- which values-kind.yaml does -- a
# party refuses any ceremony request without a signature from the
# authorising key. Every other drill goes through the API gateway, so the
# temporal worker signs for it; this one calls the parties directly,
# because restoring a key is an operator action with no gateway route.
#
# The key is read from the same Secret the worker mounts. In a real
# deployment it would not be readable at all -- that is the point of
# ceremonyAuthorizer.signerUrl -- and an operator restoring a key would
# obtain the signature from whoever holds the authorising key, which is
# the control working rather than an inconvenience.
AUTHORIZATION=""
AUTHORIZATION_SIGNATURE=""
authorize_restore() {
  local key
  key=$(kubectl -n "${NS}" get secret dev-dependency-credentials \
    -o jsonpath='{.data.authorizer-key}' 2>/dev/null | base64 -d || true)
  if [[ -z "${key}" ]]; then
    echo "    no authorising key in the cluster; assuming ceremony authorisation is off"
    return
  fi
  local out
  out=$(cd "${ROOT}/infrastructure/kind/authorize" && go run . "${key}" restore "${CEREMONY_ID}") \
    || fail "could not produce a ceremony authorisation"
  AUTHORIZATION=$(echo "${out}" | sed -n '1p')
  AUTHORIZATION_SIGNATURE=$(echo "${out}" | sed -n '2p')
  echo "    signed a restore authorisation with the cluster's authorising key"
}

restore_body() {
  local peers="$1"
  if [[ -z "${AUTHORIZATION}" ]]; then
    printf '{"ceremony_id":"%s","peers":%s}' "${CEREMONY_ID}" "${peers}"
    return
  fi
  # The authorisation travels as a JSON *string*, not a nested object --
  # the field is a string on both sides. Encoded with python rather than
  # by hand, because getting the escaping wrong produces a 400 that reads
  # like a rejected signature.
  AUTH="${AUTHORIZATION}" SIG="${AUTHORIZATION_SIGNATURE}" CID="${CEREMONY_ID}" PEERS="${peers}" \
    python3 -c 'import json,os; print(json.dumps({"ceremony_id":os.environ["CID"],"peers":json.loads(os.environ["PEERS"]),"authorization":os.environ["AUTH"],"authorization_signature":os.environ["SIG"]}))'
}

echo "==> restoring each party from sealed material"
PEERS=$(peers_json)
authorize_restore
for id in $(seq 1 "${PARTY_COUNT}"); do
  restored=$(party_curl "${id}" -X POST \
    -H 'Content-Type: application/json' \
    -d "$(restore_body "${PEERS}")" \
    "$(party_url "${id}" "/tss/keygen/restore")")
  address=$(echo "${restored}" | jqp 'd.get("address","")') \
    || fail "party ${id} refused to restore: ${restored}"
  [[ "${address,,}" == "${ORIGINAL_ADDRESS,,}" ]] \
    || fail "party ${id} restored to ${address}, not ${ORIGINAL_ADDRESS}"
  echo "    party ${id} restored at ${address}"
done

# ---------------------------------------------------------------------------
# Step 7 -- the only proof that counts
# ---------------------------------------------------------------------------

echo "==> signing with the restored committee"
sign_and_recover "post-restore" \
  || fail "the restored committee could not sign"

echo
echo "PASS: every party was destroyed with no grace period, lost the ceremony,"
echo "      was rebuilt from material sealed in Vault, and the restored committee"
echo "      signed for ${ORIGINAL_ADDRESS} -- the address the original DKG derived."
echo
echo "      Nothing in the restore path contacted a vendor. That is the claim"
echo "      docs/deployment/KEY-RECOVERY.md makes, and this is its evidence."
