#!/usr/bin/env bash
# End-to-end smoke test against a running stack (docker compose up -d --build).
# Verifies: health, admin tenant creation, authenticated signing, tenant
# isolation and the audit trail. Exits non-zero on the first failure.
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:3000}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-key}"
DEMO_KEY="${DEMO_KEY:-dev-demo-key}"

say() { printf '\n=== %s ===\n' "$1"; }
fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

say "health"
curl -fsS "$BASE_URL/health" | grep -q '"status":"ok"' || fail "health not ok"
curl -fsS "$BASE_URL/health/ready" | grep -q '"status":"ready"' || fail "not ready"

say "create tenant (admin)"
CREATE=$(curl -fsS -X POST "$BASE_URL/admin/customers" \
  -H "Authorization: Bearer $ADMIN_KEY" -H 'Content-Type: application/json' \
  -d '{"email":"smoke@bank.example","tier":"enterprise"}')
echo "$CREATE"
NEW_KEY=$(printf '%s' "$CREATE" | sed -n 's/.*"api_key":"\([^"]*\)".*/\1/p')
[ -n "$NEW_KEY" ] || fail "no api_key returned"

say "unauthenticated sign is rejected"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/sign" \
  -H 'Content-Type: application/json' -d '{}')
[ "$code" = "401" ] || fail "expected 401, got $code"

say "authenticated sign (demo tenant)"
SIGN=$(curl -fsS -X POST "$BASE_URL/sign" \
  -H "Authorization: Bearer $DEMO_KEY" -H 'Content-Type: application/json' \
  -d '{"chainId":11155111,"to":"0x742d35Cc6634C0532925a3b844Bc9e7595f42bE","value":"0","gasLimit":21000,"gasPrice":"20000000000","nonce":0}')
echo "$SIGN"
REQ_ID=$(printf '%s' "$SIGN" | sed -n 's/.*"requestId":"\([^"]*\)".*/\1/p')
[ -n "$REQ_ID" ] || fail "no requestId"

say "audit trail present"
curl -fsS "$BASE_URL/transactions/$REQ_ID/audit" \
  -H "Authorization: Bearer $DEMO_KEY" | grep -q 'SIGN_SUCCESS' || fail "no SIGN_SUCCESS in audit"

say "tenant isolation: new tenant cannot read demo's tx"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/transactions/$REQ_ID" \
  -H "Authorization: Bearer $NEW_KEY")
[ "$code" = "404" ] || fail "expected 404 cross-tenant, got $code"

printf '\nALL SMOKE CHECKS PASSED\n'
