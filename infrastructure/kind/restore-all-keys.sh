#!/usr/bin/env bash
#
# Restores every active key onto every signing party.
#
# A party holds a key in memory only after it has been restored: a party that
# restarts, or a cluster rebuilt from a backup, starts with no keys at all and
# every key's shares are still sealed in Vault, safe but not usable. Restoring
# is deliberately an explicit operator action (docs/deployment/KEY-RECOVERY.md,
# step 4), and until now it was a script that proved one key at a time
# (recovery-drill.sh --restore-only). A platform with 24 keys cannot be
# recovered by hand 24 times.
#
# This reads the keys from the database, asks each party to restore each
# ceremony from what is sealed in Vault, and checks that every party reports the
# address the database holds for that key. It does not sign; run
# recovery-drill.sh --restore-only for one key afterwards to prove signing, or
# smoke-test.sh.
#
# It is safe to run again: a party that already holds a ceremony is skipped, and
# a key that cannot be restored (for instance a share that was retired on
# purpose) is reported and does not stop the others.
#
# Usage:
#   ./restore-all-keys.sh                    # every active key
#   ./restore-all-keys.sh --dry-run          # list what would be restored
#   ./restore-all-keys.sh --ceremony-id C    # one key
#
# Needs a deployed cluster, kubectl, python3 and Go. Environment as for
# recovery-drill.sh: K8S_NAMESPACE, PARTY_COUNT, PARTY_PORT, VAULT_ROOT_TOKEN
# (default: the dev credentials secret) and AUTHORIZER_KEY (default: the same
# secret; in a real deployment the authorising key is not in the cluster and
# the restore authorisations must come from whoever holds it).
set -euo pipefail

NS="${K8S_NAMESPACE:-openfireblocks}"
PARTY_COUNT="${PARTY_COUNT:-3}"
PARTY_PORT="${PARTY_PORT:-7000}"
PARTY_TUNNEL_BASE="${PARTY_TUNNEL_BASE:-17000}"
CURL=(curl -sS --noproxy '*')

ONLY_CEREMONY=""
DRY_RUN=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ceremony-id) ONLY_CEREMONY="$2"; shift 2 ;;
    --dry-run)     DRY_RUN=1; shift ;;
    -h|--help)     sed -n '2,32p' "$0" | sed 's/^# \?//'; exit 0 ;;
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
# The keys to restore
# ---------------------------------------------------------------------------

echo "==> reading the active keys from the database"
PSQL=(kubectl -n "${NS}" exec deploy/postgres -- psql -U postgres -d openfireblocks)
# One row per key that finished its ceremony. A superuser reads across tenants;
# this is an operator tool, not an API route.
ROWS=$("${PSQL[@]}" -At -F '|' -c "
  SELECT d.ceremony_id, k.key_id, k.blockchain, COALESCE(k.address, '')
    FROM dkg_ceremonies d JOIN key_pairs k ON k.key_id = d.key_id
   WHERE k.status = 'active' AND d.status = 'completed'
   ORDER BY k.created_at") || fail "could not read the keys from the database"
if [[ -n "${ONLY_CEREMONY}" ]]; then
  ROWS=$(echo "${ROWS}" | grep "^${ONLY_CEREMONY}|" || true)
fi
TOTAL=$(echo "${ROWS}" | grep -c . || true)
echo "    ${TOTAL} active key(s)"
[[ "${TOTAL}" -gt 0 ]] || { echo "nothing to restore"; exit 0; }

if [[ "${DRY_RUN}" == "1" ]]; then
  echo "${ROWS}" | while IFS='|' read -r cid kid chain addr; do
    echo "    would restore ceremony ${cid} (key ${kid}, ${chain}, ${addr})"
  done
  exit 0
fi

# ---------------------------------------------------------------------------
# Reaching the parties: a client certificate from the cluster's own CA
# ---------------------------------------------------------------------------

vault_pod=$(kubectl -n "${NS}" get pod -l app=vault -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
VAULT_ROOT_TOKEN="${VAULT_ROOT_TOKEN:-$(kubectl -n "${NS}" get secret dev-dependency-credentials \
  -o jsonpath='{.data.vault-root-token}' 2>/dev/null | base64 -d || true)}"

MTLS=0
mint_client_identity() {
  [[ -n "${vault_pod}" && -n "${VAULT_ROOT_TOKEN}" ]] || return 1
  local out
  out=$(kubectl -n "${NS}" exec "${vault_pod}" -- \
    env "VAULT_TOKEN=${VAULT_ROOT_TOKEN}" VAULT_ADDR=http://127.0.0.1:8200 \
    vault write -format=json pki/production/issue/internal-service \
    common_name=restore-all-keys.internal ttl=1h 2>/dev/null) || return 1
  echo "${out}" | python3 -c '
import sys, json
d = json.load(sys.stdin)["data"]
w = sys.argv[1]
open(w + "/tls.crt", "w").write(d["certificate"] + "\n")
open(w + "/tls.key", "w").write(d["private_key"] + "\n")
open(w + "/ca.crt", "w").write(d["issuing_ca"] + "\n")
' "${WORKDIR}" || return 1
  MTLS=1
}
echo "==> opening a channel to the parties"
if mint_client_identity; then
  echo "    minted a short-lived client certificate from the cluster's CA"
else
  echo "    no client certificate available; assuming the parties are in plaintext"
fi

party_tunnel_port() { echo $((PARTY_TUNNEL_BASE + $1)); }
for id in $(seq 1 "${PARTY_COUNT}"); do
  kubectl -n "${NS}" port-forward "svc/party-${id}" "$(party_tunnel_port "${id}"):${PARTY_PORT}" \
    >/dev/null 2>&1 &
  PIDS+=($!)
done
sleep 3

party_curl() {
  local id="$1"; shift
  local port; port=$(party_tunnel_port "${id}")
  if [[ "${MTLS}" == "1" ]]; then
    "${CURL[@]}" \
      --cert "${WORKDIR}/tls.crt" --key "${WORKDIR}/tls.key" --cacert "${WORKDIR}/ca.crt" \
      --resolve "party-${id}.internal:${port}:127.0.0.1" "$@"
  else
    "${CURL[@]}" "$@"
  fi
}
party_url() {
  local id="$1" path="$2" port; port=$(party_tunnel_port "${id}")
  if [[ "${MTLS}" == "1" ]]; then echo "https://party-${id}.internal:${port}${path}"
  else echo "http://127.0.0.1:${port}${path}"; fi
}

# The full service names, the ones the parties' certificates are valid for.
peers_json() {
  local out="" id
  for id in $(seq 1 "${PARTY_COUNT}"); do
    [[ -n "${out}" ]] && out="${out},"
    out="${out}\"${id}\":\"https://party-${id}.${NS}.svc.cluster.local:${PARTY_PORT}\""
  done
  [[ "${MTLS}" == "1" ]] || out="${out//https:/http:}"
  echo "{${out}}"
}
PEERS=$(peers_json)

# ---------------------------------------------------------------------------
# Authorisations: one per ceremony, signed by the authorising key
# ---------------------------------------------------------------------------

AUTHORIZER_KEY="${AUTHORIZER_KEY:-$(kubectl -n "${NS}" get secret dev-dependency-credentials \
  -o jsonpath='{.data.authorizer-key}' 2>/dev/null | base64 -d || true)}"
if [[ -n "${AUTHORIZER_KEY}" ]]; then
  (cd "${ROOT}/infrastructure/kind/authorize" && go build -o "${WORKDIR}/authorize" .) \
    || fail "could not build the authorisation tool"
  echo "==> restore authorisations will be signed with the authorising key"
else
  echo "==> no authorising key available; assuming ceremony authorisation is off"
fi

restore_body() {
  local cid="$1" auth="" sig=""
  if [[ -n "${AUTHORIZER_KEY}" ]]; then
    local out
    out=$("${WORKDIR}/authorize" "${AUTHORIZER_KEY}" restore "${cid}") || return 1
    auth=$(echo "${out}" | sed -n '1p'); sig=$(echo "${out}" | sed -n '2p')
  fi
  CID="${cid}" PEERS="${PEERS}" AUTH="${auth}" SIG="${sig}" python3 -c '
import json, os
b = {"ceremony_id": os.environ["CID"], "peers": json.loads(os.environ["PEERS"])}
if os.environ["AUTH"]:
    b["authorization"] = os.environ["AUTH"]
    b["authorization_signature"] = os.environ["SIG"]
print(json.dumps(b))'
}

# ---------------------------------------------------------------------------
# Restore
# ---------------------------------------------------------------------------

echo "==> restoring ${TOTAL} key(s) on ${PARTY_COUNT} parties"
restored=0; skipped=0; failed=0; keys_ok=0; keys_bad=0
while IFS='|' read -r cid kid chain want; do
  [[ -n "${cid}" ]] || continue
  key_bad=0
  body=$(restore_body "${cid}") || { echo "    ${cid}: could not produce an authorisation"; keys_bad=$((keys_bad+1)); failed=$((failed+PARTY_COUNT)); continue; }
  for id in $(seq 1 "${PARTY_COUNT}"); do
    code=$(party_curl "${id}" -o /dev/null -w '%{http_code}' \
      "$(party_url "${id}" "/tss/keygen/status?ceremony_id=${cid}")" || true)
    if [[ "${code}" == "200" ]]; then skipped=$((skipped+1)); continue; fi
    resp=$(party_curl "${id}" -X POST -H 'Content-Type: application/json' -d "${body}" \
      "$(party_url "${id}" "/tss/keygen/restore")" || true)
    got=$(echo "${resp}" | jqp 'd.get("address","")' 2>/dev/null || true)
    if [[ -z "${got}" ]]; then
      echo "    ${cid} party ${id}: not restored: $(echo "${resp}" | cut -c1-160)"
      failed=$((failed+1)); key_bad=1
    elif [[ -n "${want}" && "${got,,}" != "${want,,}" ]]; then
      echo "    ${cid} party ${id}: restored to ${got}, but the database holds ${want}"
      failed=$((failed+1)); key_bad=1
    else
      restored=$((restored+1))
    fi
  done
  if [[ "${key_bad}" == "0" ]]; then keys_ok=$((keys_ok+1)); else keys_bad=$((keys_bad+1)); fi
done <<< "${ROWS}"

echo
echo "keys fully restored on all ${PARTY_COUNT} parties: ${keys_ok} of ${TOTAL}   with a problem: ${keys_bad}"
echo "party restores done now: ${restored}   already held: ${skipped}   failed: ${failed}"
[[ "${keys_bad}" == "0" ]] || fail "some keys were not restored; see above (a retired share cannot be restored by design)"
echo "PASS: every active key is restored on every party at the address the database holds."
