#!/usr/bin/env bash
#
# The whole platform, end to end, on one host, with real processes:
# Postgres, Temporal, policy-service, mpc-signer, three mpc-party processes, the
# Temporal worker, the API gateway, and a Solana RPC stand-in that verifies
# signatures. Then infrastructure/local/e2e/fullstack.js drives it through the
# public API: a real DKG, a real threshold-signed transfer, a held transfer
# released by two approvers with one-time codes, and an emergency freeze.
#
# What is real: every process above except the chain, real HTTP between them, a
# real tss-lib ceremony, real Ed25519 threshold signatures, real database rules.
# What is not: the chain (mock-solana-node.js checks the signature but is not
# Solana), and there is no mTLS or host isolation between the parties (see
# party-isolation-check.sh and the kind drills for those).
#
# Needs: go, node (with the gateway's node_modules installed: `npm ci` in
# services/api-gateway), psql, and the `temporal` CLI on PATH (or TEMPORAL_BIN).
#
#   ./infrastructure/local/e2e-fullstack-local.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
TEMPORAL_BIN="${TEMPORAL_BIN:-$(command -v temporal || true)}"
PIDS=()
cleanup() { "$ROOT/infrastructure/local/postgres-local.sh" stop >/dev/null 2>&1 || true; for p in "${PIDS[@]:-}"; do kill "$p" >/dev/null 2>&1 || true; done; sleep 1; for p in "${PIDS[@]:-}"; do kill -9 "$p" >/dev/null 2>&1 || true; done; rm -rf "$WORK" "${PGLOCAL_DIR:-/nonexistent}"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; for f in "$WORK"/*.log; do [ -e "$f" ] || continue; echo "--- $(basename "$f") (last 15) ---" >&2; tail -15 "$f" >&2; done; exit 1; }
up() { for _ in $(seq 1 "${3:-60}"); do (exec 3<>/dev/tcp/127.0.0.1/$2) 2>/dev/null && return 0; sleep 0.5; done; fail "$1 did not come up on :$2"; }
start() { local name="$1"; shift; "$@" > "$WORK/$name.log" 2>&1 & PIDS+=($!); disown $! 2>/dev/null || true; }

[ -n "$TEMPORAL_BIN" ] || fail "the temporal CLI is required (https://docs.temporal.io/cli); set TEMPORAL_BIN"
[ -d "$ROOT/services/api-gateway/node_modules/otplib" ] || fail "run npm ci in services/api-gateway first"

echo "==> database (every migration)"
# Its own port and directory: postgres-local.sh start wipes its data directory,
# so it must never be pointed at a database someone is using.
export PGLOCAL_PORT="${PGLOCAL_PORT:-55499}" PGLOCAL_DIR="${TMPDIR:-/tmp}/ofb-e2e-pg-$$"
eval "$("$ROOT/infrastructure/local/postgres-local.sh" start)"
export DATABASE_URL DATABASE_ADMIN_URL
PGPORT="$(echo "$DATABASE_ADMIN_URL" | sed -E 's#.*:([0-9]+)/.*#\1#')"

echo "==> building the services"
for s in policy-service mpc-signer temporal-worker mpc-party; do
  (cd "$ROOT/services/$s" && go build -o "$WORK/$s" .) || fail "$s did not build"
done
(cd "$ROOT/services/api-gateway" && npx --no-install nest build >/dev/null) || fail "the gateway did not build"

echo "==> starting the stack"
P_SOL=18899; P_SIGN=18080; P_POL=18081; P_TEMP=17233; P_GW=13999
start temporal "$TEMPORAL_BIN" server start-dev --port $P_TEMP --ui-port 18233 --headless --log-level error
for _ in $(seq 1 60); do "$TEMPORAL_BIN" operator namespace list --address 127.0.0.1:$P_TEMP >/dev/null 2>&1 && break; sleep 1; done
start solana env BALANCE_LAMPORTS=100000000000 PORT=$P_SOL node "$ROOT/infrastructure/local/mock-solana-node.js"
start policy env PORT=$P_POL "$WORK/policy-service"
start signer env PORT=$P_SIGN SOLANA_RPC_URL=http://127.0.0.1:$P_SOL \
  MPC_SIGNER_PRIVATE_KEY=0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318 IMMUDB_URL=127.0.0.1:1 "$WORK/mpc-signer"
SHARE_KEY="$WORK.sharekey"; head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$SHARE_KEY"; chmod 600 "$SHARE_KEY"
for id in 1 2 3; do
  start "party-$id" env PARTY_ID=$id PORT=1770$id TSS_ALLOW_UNAUTHENTICATED_PEERS=1 TSS_PREPARAMS_POOL=0 \
    SHARE_STORE_DIR="$WORK/shares" SHARE_STORE_KEY_FILE="$SHARE_KEY" "$WORK/mpc-party"
done
start worker env TEMPORAL_HOSTPORT=127.0.0.1:$P_TEMP DATABASE_URL="$DATABASE_ADMIN_URL" ETHEREUM_RPC_SEPOLIA=http://127.0.0.1:1 \
  MPC_SIGNER_URL=http://127.0.0.1:$P_SIGN POLICY_SERVICE_URL=http://127.0.0.1:$P_POL "$WORK/temporal-worker"
start gateway env PORT=$P_GW TEMPORAL_HOSTPORT=127.0.0.1:$P_TEMP POLICY_SERVICE_URL=http://127.0.0.1:$P_POL MPC_SIGNER_URL=http://127.0.0.1:$P_SIGN \
  MPC_PARTY_ENDPOINT_TEMPLATE='http://127.0.0.1:1770{id}' MPC_PARTY_HEALTH_TEMPLATE='http://127.0.0.1:1770{id}/health' \
  JWT_SECRET=e2e-secret-e2e-secret-e2e-secret-e2e node "$ROOT/services/api-gateway/dist/main.js"
for pp in $P_SOL $P_SIGN $P_POL 17701 17702 17703 $P_GW; do up service $pp; done
sleep 2

echo "==> the scenario"
GATEWAY=http://127.0.0.1:$P_GW SOLANA_MOCK=http://127.0.0.1:$P_SOL OTPLIB_PATH="$ROOT/services/api-gateway/node_modules/otplib" \
  PSQL="PGPASSWORD=dev-only psql -h 127.0.0.1 -p $PGPORT -U app_admin -d openfireblocks -Atc" \
  node "$ROOT/infrastructure/local/e2e/fullstack.js" || fail "the scenario failed"
