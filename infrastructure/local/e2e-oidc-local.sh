#!/usr/bin/env bash
#
# Sign in to the gateway through an independent OpenID Connect provider
# (node-oidc-provider, an OpenID-certified implementation): discovery, the
# authorization code flow with PKCE, a verified ID token, UserInfo, replay and
# tamper refusals, account provisioning. Proves the gateway's OIDC client
# against software it did not write; it is not a run against Keycloak, Entra ID
# or Okta, each of which has its own quirks.
#
# Needs: node and npm (it installs oidc-provider into a temporary directory),
# psql/postgres, and `npm ci` done in services/api-gateway.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"; PIDS=()
cleanup() { "$ROOT/infrastructure/local/postgres-local.sh" stop >/dev/null 2>&1 || true; for p in "${PIDS[@]:-}"; do kill "$p" >/dev/null 2>&1 || true; done; rm -rf "$WORK" "${PGLOCAL_DIR:-/nonexistent}"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; for f in "$WORK"/*.log; do [ -e "$f" ] || continue; echo "--- $(basename "$f") ---" >&2; tail -12 "$f" >&2; done; exit 1; }
start() { local n="$1"; shift; "$@" > "$WORK/$n.log" 2>&1 & PIDS+=($!); disown $! 2>/dev/null || true; }
up() { for _ in $(seq 1 80); do (exec 3<>/dev/tcp/127.0.0.1/$2) 2>/dev/null && return 0; sleep 0.5; done; fail "$1 did not come up on :$2"; }

[ -d "$ROOT/services/api-gateway/node_modules" ] || fail "run npm ci in services/api-gateway first"
export PGLOCAL_PORT="${PGLOCAL_PORT:-55498}" PGLOCAL_DIR="${TMPDIR:-/tmp}/ofb-oidc-pg-$$"
eval "$("$ROOT/infrastructure/local/postgres-local.sh" start)"
npm install --prefix "$WORK" --silent oidc-provider@8 >/dev/null 2>&1 || fail "could not install oidc-provider"
(cd "$ROOT/services/api-gateway" && npx --no-install nest build >/dev/null) || fail "the gateway did not build"

GW=13998; IDP=18091
start idp env PORT=$IDP REDIRECT_URI=http://127.0.0.1:$GW/auth/sso/callback OIDC_PROVIDER_PATH="$WORK/node_modules/oidc-provider" node "$ROOT/infrastructure/local/e2e/oidc-provider.js"
up idp $IDP
start gateway env PORT=$GW JWT_SECRET=oidc-e2e-secret-oidc-e2e-secret-xx OIDC_ISSUER=http://127.0.0.1:$IDP OIDC_CLIENT_ID=ofb-console \
  OIDC_CLIENT_SECRET=s3cr3t-for-tests OIDC_REDIRECT_URI=http://127.0.0.1:$GW/auth/sso/callback node "$ROOT/services/api-gateway/dist/main.js"
up gateway $GW
GATEWAY=http://127.0.0.1:$GW node "$ROOT/infrastructure/local/e2e/oidc.js" || fail "the OIDC scenario failed"
