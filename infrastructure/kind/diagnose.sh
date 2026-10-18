#!/usr/bin/env bash
#
# Collects what is needed to explain a failed smoke test into one file, so a
# crash is diagnosed from evidence rather than guessed at. Run it straight
# after a failure, before tearing anything down:
#
#   infrastructure/kind/diagnose.sh            # writes ./ofb-diagnostics.txt
#
# Read-only: it only runs get/describe/logs/top. It prints no secret values.
set -uo pipefail

NS="${K8S_NAMESPACE:-openfireblocks}"
OUT="${1:-ofb-diagnostics.txt}"

section() { printf '\n===== %s =====\n' "$*"; }
run() { echo "\$ $*"; "$@" 2>&1 || echo "(exit $?)"; }

{
  section "when"
  date -u
  section "host"
  uname -a
  command -v nproc >/dev/null && nproc
  command -v free >/dev/null && free -m
  section "docker"
  run docker info --format 'CPUs={{.NCPU}} Memory={{.MemTotal}}'
  run docker stats --no-stream
  section "nodes"
  run kubectl get nodes -o wide
  run kubectl top nodes
  section "pods (look at RESTARTS)"
  run kubectl -n "${NS}" get pods -o wide
  run kubectl -n "${NS}" top pods
  section "events (newest last)"
  run kubectl -n "${NS}" get events --sort-by=.lastTimestamp
  section "api-gateway: why it last stopped"
  run kubectl -n "${NS}" describe pod -l app.kubernetes.io/component=api-gateway
  section "api-gateway: current log"
  run kubectl -n "${NS}" logs deploy/ofb-openfireblocks-api-gateway --tail=200
  section "api-gateway: previous (crashed) container log"
  run kubectl -n "${NS}" logs deploy/ofb-openfireblocks-api-gateway --previous --tail=200
  section "temporal-worker log"
  run kubectl -n "${NS}" logs deploy/ofb-openfireblocks-temporal-worker --all-containers --tail=100
  section "mpc-signer log"
  run kubectl -n "${NS}" logs deploy/ofb-openfireblocks-mpc-signer --all-containers --tail=100
  for n in 1 2 3; do
    section "party-${n} log"
    run kubectl -n "${NS}" logs "deploy/party-${n}" --all-containers --tail=100
  done
} >"${OUT}" 2>&1

echo "wrote ${OUT} ($(wc -l <"${OUT}") lines)"
echo "Send it to whoever is diagnosing. It contains pod names and logs, not secret values."
