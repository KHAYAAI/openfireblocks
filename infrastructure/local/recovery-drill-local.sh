#!/usr/bin/env bash
#
# The recovery drill, with real processes and a real Vault, on one host.
#
# infrastructure/kind/recovery-drill.sh is the same drill against a
# deployed cluster, and it is the one that proves the deployment. This one
# exists because that one needs a cluster, and a proof that only runs where
# somebody stood up Kubernetes is a proof that mostly does not run.
#
# What is real here, and it is most of it: a real `vault server -dev`
# holding the sealed shares, three real mpc-party processes with their own
# pids and their own memory, real HTTP between them, a real tss-lib DKG,
# and a real SIGKILL. Nothing is stubbed. `kill -9` does to a process
# exactly what losing its host does -- no shutdown hook, no flush, no
# chance to write anything down -- which is the scenario the recovery
# procedure exists for.
#
# What is not real: the parties share a kernel and a filesystem, so this
# says nothing about host isolation, and there is no mTLS, so it says
# nothing about the transport. Those are what the kind drill and
# party-isolation-check.sh are for. Read this as "the recovery logic and
# the sealed material are sound", not "the deployment is".
#
# The step that keeps it honest is the same one: after the kill, signing
# must fail. A drill that only showed the parties coming back would pass
# in the exact case where the restore path is broken.
#
# Usage:
#   ./infrastructure/local/recovery-drill-local.sh
#
# Needs: go, and either a running Vault (VAULT_ADDR set) or the `vault`
# binary on PATH, in which case the drill starts and stops its own.
set -euo pipefail

CURVE="${CURVE:-ed25519}"
PARTY_COUNT="${PARTY_COUNT:-3}"
THRESHOLD="${THRESHOLD:-1}" # threshold+1 parties sign; 1 means a 2-of-3
BASE_PORT="${BASE_PORT:-17700}"
CEREMONY_ID="${CEREMONY_ID:-local-recovery-drill-$(date +%s)}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKDIR="$(mktemp -d)"
CURL=(curl -sS --noproxy '*')

# A failed drill you cannot diagnose is a failed drill you re-run instead
# of fixing, so the party logs come out with the failure rather than being
# deleted with the work directory.
fail() {
  echo "FAIL: $*" >&2
  for log in "${WORKDIR}"/party-*.log; do
    [[ -e "${log}" ]] || continue
    echo "--- $(basename "${log}") (last 25 lines) ---" >&2
    tail -25 "${log}" >&2
  done
  exit 1
}
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

PARTY_PIDS=()
VAULT_PID=""
cleanup() {
  for pid in "${PARTY_PIDS[@]:-}"; do kill -9 "${pid}" >/dev/null 2>&1 || true; done
  [[ -n "${VAULT_PID}" ]] && kill "${VAULT_PID}" >/dev/null 2>&1 || true
  rm -rf "${WORKDIR}"
}
trap cleanup EXIT

port_for() { echo $((BASE_PORT + $1)); }
url_for() { echo "http://127.0.0.1:$(port_for "$1")"; }

# ---------------------------------------------------------------------------
# A real Vault
# ---------------------------------------------------------------------------

echo "==> Vault"
if [[ -n "${VAULT_ADDR:-}" ]]; then
  echo "    using the Vault already configured at ${VAULT_ADDR}"
else
  command -v vault >/dev/null || fail "no VAULT_ADDR and no vault binary; install Vault or point VAULT_ADDR at one"
  vault server -dev -dev-root-token-id=drill-root -dev-listen-address=127.0.0.1:18200 \
    > "${WORKDIR}/vault.log" 2>&1 &
  VAULT_PID=$!
  export VAULT_ADDR=http://127.0.0.1:18200 VAULT_TOKEN=drill-root
  for _ in $(seq 1 40); do
    "${CURL[@]}" -o /dev/null -m 1 "${VAULT_ADDR}/v1/sys/health" 2>/dev/null && break
    sleep 0.25
  done
  "${CURL[@]}" -o /dev/null -m 2 "${VAULT_ADDR}/v1/sys/health" \
    || fail "Vault did not start; see ${WORKDIR}/vault.log"
  echo "    started a dev Vault at ${VAULT_ADDR} (pid ${VAULT_PID})"
fi
[[ -n "${VAULT_TOKEN:-}" ]] || fail "VAULT_TOKEN is not set"

# ---------------------------------------------------------------------------
# Real party processes
# ---------------------------------------------------------------------------

echo "==> building mpc-party"
(cd "${ROOT}/services/mpc-party" && go build -o "${WORKDIR}/mpc-party" .) \
  || fail "mpc-party did not build"
(cd "${ROOT}/infrastructure/local/verify" && go build -o "${WORKDIR}/verify" .) \
  || fail "the signature verifier did not build"

start_parties() {
  local label="$1"
  PARTY_PIDS=()
  for id in $(seq 1 "${PARTY_COUNT}"); do
    # No mTLS here, so there is no client certificate to bind a sender
    # to and peer enforcement has to be turned off explicitly. This is
    # the flag's intended use and the reason it must be set to exactly
    # "1": a typo leaves the check on, which is the safe direction.
    PARTY_ID="${id}" PORT="$(port_for "${id}")" \
      TSS_ALLOW_UNAUTHENTICATED_PEERS=1 \
      TSS_PREPARAMS_POOL=0 \
      VAULT_ADDR="${VAULT_ADDR}" VAULT_TOKEN="${VAULT_TOKEN}" \
      "${WORKDIR}/mpc-party" > "${WORKDIR}/party-${id}-${label}.log" 2>&1 &
    PARTY_PIDS+=($!)
    # Disowned so the shell does not print a job-control notice when the
    # drill kills them. The kill is the point of the exercise; it should
    # not read like something went wrong.
    disown %% 2>/dev/null || true
  done

  for id in $(seq 1 "${PARTY_COUNT}"); do
    local ok=0
    for _ in $(seq 1 60); do
      "${CURL[@]}" -o /dev/null -m 1 "$(url_for "${id}")/health" 2>/dev/null && { ok=1; break; }
      sleep 0.25
    done
    [[ "${ok}" == "1" ]] || fail "party ${id} did not come up; see ${WORKDIR}/party-${id}-${label}.log"
  done
  echo "    ${PARTY_COUNT} parties up as pids ${PARTY_PIDS[*]}"
}

peers_json() {
  local out="" id
  for id in $(seq 1 "${PARTY_COUNT}"); do
    [[ -n "${out}" ]] && out="${out},"
    out="${out}\"${id}\":\"$(url_for "${id}")\""
  done
  echo "{${out}}"
}

echo "==> starting the parties"
start_parties original
PEERS=$(peers_json)

# ---------------------------------------------------------------------------
# A real DKG
# ---------------------------------------------------------------------------

echo "==> running a real ${CURVE} DKG"
for id in $(seq 1 "${PARTY_COUNT}"); do
  resp=$("${CURL[@]}" -X POST "$(url_for "${id}")/tss/keygen/start" \
    -H 'Content-Type: application/json' \
    -d "{\"ceremony_id\":\"${CEREMONY_ID}\",\"threshold\":${THRESHOLD},\"curve\":\"${CURVE}\",\"peers\":${PEERS}}")
  echo "${resp}" | grep -q '"error"' && fail "party ${id} refused to start keygen: ${resp}"
done

ADDRESS=""
PUBKEY=""
for _ in $(seq 1 240); do
  status=$("${CURL[@]}" "$(url_for 1)/tss/keygen/status?ceremony_id=${CEREMONY_ID}" 2>/dev/null || echo '{}')
  state=$(echo "${status}" | jqp 'd.get("status","")' 2>/dev/null || echo "")
  [[ "${state}" == "failed" ]] && fail "DKG failed: ${status}"
  if [[ "${state}" == "completed" ]]; then
    ADDRESS=$(echo "${status}" | jqp 'd.get("address","")')
    PUBKEY=$(echo "${status}" | jqp 'd.get("public_key","")')
    break
  fi
  sleep 1
done
[[ -n "${ADDRESS}" ]] || fail "DKG did not complete in time"
echo "    key at ${ADDRESS}"

# Every party must report its share sealed. A ceremony that completed
# with sealing off leaves shares that only ever existed in memory, and the
# recovery this drill is about would have nothing to recover from.
for id in $(seq 1 "${PARTY_COUNT}"); do
  sealed=$("${CURL[@]}" "$(url_for "${id}")/tss/keygen/status?ceremony_id=${CEREMONY_ID}" | jqp 'd.get("sealed")')
  [[ "${sealed}" == "True" ]] || fail "party ${id} reports sealed=${sealed}; nothing was written to Vault"
done
echo "    all ${PARTY_COUNT} shares sealed in Vault"

# And confirm it independently, out of Vault itself, the way an operator
# recovering would. Asking the parties is not evidence: a party that has
# lost its memory cannot tell you what it sealed.
for id in $(seq 1 "${PARTY_COUNT}"); do
  entry=$("${CURL[@]}" -H "X-Vault-Token: ${VAULT_TOKEN}" \
    "${VAULT_ADDR}/v1/secret/data/openfireblocks/mpc-party/party-${id}/${CEREMONY_ID}")
  echo "${entry}" | grep -q 'save_data' || fail "party ${id} has no share in Vault"
  echo "${entry}" | grep -q 'ceremony_context' \
    || fail "party ${id}'s share is sealed without its ceremony context and cannot be restored"
done
echo "    and each carries its ceremony context"

# ---------------------------------------------------------------------------
# Sign, so there is a baseline
# ---------------------------------------------------------------------------

COMMITTEE="[1, 2]"
sign_once() {
  local label="$1" message="$2"
  # Declared separately: bash expands every word of a `local` statement
  # before performing any of its assignments, so referring to `label` in
  # the same statement that defines it reads an unset variable.
  local sign_id
  sign_id="drill-${label}-$(date +%s%N)"
  for id in 1 2; do
    "${CURL[@]}" -o /dev/null -X POST "$(url_for "${id}")/tss/sign/start" \
      -H 'Content-Type: application/json' \
      -d "{\"sign_id\":\"${sign_id}\",\"keygen_ceremony_id\":\"${CEREMONY_ID}\",\"message_hash_hex\":\"${message}\",\"committee_party_ids\":${COMMITTEE}}" \
      2>/dev/null || return 1
  done
  for _ in $(seq 1 120); do
    local st sig state
    st=$("${CURL[@]}" "$(url_for 1)/tss/sign/status?sign_id=${sign_id}" 2>/dev/null || echo '{}')
    state=$(echo "${st}" | jqp 'd.get("status","")' 2>/dev/null || echo "")
    [[ "${state}" == "failed" ]] && return 1
    sig=$(echo "${st}" | jqp 'd.get("signature","")' 2>/dev/null || echo "")
    if [[ -n "${sig}" ]]; then echo "${sig}"; return 0; fi
    sleep 0.5
  done
  return 1
}

# A distinct message per phase, so a cached or replayed signature cannot
# stand in for a fresh one.
BASELINE_MSG=$(printf 'baseline-%s' "$(date +%s%N)" | sha256sum | cut -d' ' -f1)
echo "==> baseline signature, before anything is destroyed"
BASELINE_SIG=$(sign_once baseline "${BASELINE_MSG}") || fail "the key could not sign before the drill started"

# Verification is delegated to crypto/ed25519 via
# infrastructure/local/verify. The drill previously carried a hand-written
# Ed25519 implementation in Python, which rejected roughly one good
# signature in five -- a verifier that is wrong in the failing direction
# makes a drill report that recovery is broken when it is not, which is the
# more expensive of the two ways to be wrong here.
verify_ed25519() {
  "${WORKDIR}/verify" "$1" "$2" "$3"
}

[[ "$(verify_ed25519 "${PUBKEY}" "${BASELINE_MSG}" "${BASELINE_SIG}")" == "VALID" ]] \
  || fail "the baseline signature does not verify; the drill cannot trust anything after this"
echo "    verified against the DKG's public key"

# ---------------------------------------------------------------------------
# Destroy
# ---------------------------------------------------------------------------

echo "==> SIGKILLing every party"
for pid in "${PARTY_PIDS[@]}"; do kill -9 "${pid}" 2>/dev/null || true; done
for pid in "${PARTY_PIDS[@]}"; do wait "${pid}" 2>/dev/null || true; done
echo "    ${PARTY_COUNT} processes killed with no chance to write anything down"

echo "==> starting replacements with empty memory"
start_parties restored

# ---------------------------------------------------------------------------
# Prove the loss is real
# ---------------------------------------------------------------------------

echo "==> confirming the key is genuinely gone"
for id in $(seq 1 "${PARTY_COUNT}"); do
  code=$("${CURL[@]}" -o /dev/null -w '%{http_code}' \
    "$(url_for "${id}")/tss/keygen/status?ceremony_id=${CEREMONY_ID}")
  [[ "${code}" == "404" ]] \
    || fail "party ${id} still knows the ceremony (HTTP ${code}); nothing was destroyed and this drill proves nothing"
done

LOST_MSG=$(printf 'post-kill-%s' "$(date +%s%N)" | sha256sum | cut -d' ' -f1)
if sign_once post-kill "${LOST_MSG}" >/dev/null 2>&1; then
  fail "the key signed after every party was killed; state survived that should not have"
fi
echo "    every party returns 404, and signing fails"

# ---------------------------------------------------------------------------
# Restore
# ---------------------------------------------------------------------------

echo "==> restoring each party from the sealed material"
for id in $(seq 1 "${PARTY_COUNT}"); do
  restored=$("${CURL[@]}" -X POST "$(url_for "${id}")/tss/keygen/restore" \
    -H 'Content-Type: application/json' \
    -d "{\"ceremony_id\":\"${CEREMONY_ID}\",\"peers\":${PEERS}}")
  addr=$(echo "${restored}" | jqp 'd.get("address","")') \
    || fail "party ${id} refused to restore: ${restored}"
  [[ "${addr}" == "${ADDRESS}" ]] \
    || fail "party ${id} restored to ${addr}, not ${ADDRESS}"
  echo "    party ${id} restored at ${addr}"
done

# ---------------------------------------------------------------------------
# The only proof that counts
# ---------------------------------------------------------------------------

echo "==> signing with the restored committee"
RESTORED_MSG=$(printf 'post-restore-%s' "$(date +%s%N)" | sha256sum | cut -d' ' -f1)
RESTORED_SIG=$(sign_once post-restore "${RESTORED_MSG}") \
  || fail "the restored committee could not sign"

[[ "$(verify_ed25519 "${PUBKEY}" "${RESTORED_MSG}" "${RESTORED_SIG}")" == "VALID" ]] \
  || fail "a signature from the restored committee does not verify against the original public key; the recovery recovered nothing"
echo "    verified against the original public key"

echo
echo "PASS: ${PARTY_COUNT} real party processes ran a real ${CURVE} DKG, sealed their"
echo "      shares in a real Vault, were killed with SIGKILL, came back knowing"
echo "      nothing, were restored from Vault alone, and signed for"
echo "      ${ADDRESS} -- the address the original DKG derived."
echo
echo "      Nothing in the restore path contacted a vendor."
