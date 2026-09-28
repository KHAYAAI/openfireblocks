#!/usr/bin/env bash
# Multi-chain smoke test against a running stack.
# Verifies: chain enumeration, multi-chain signing on all supported chains,
# error handling, and cross-chain isolation.
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:3000}"
API_KEY="${API_KEY:-dev-demo-key}"

say() { printf '\n=== %s ===\n' "$1"; }
fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

say "get supported chains"
CHAINS=$(curl -fsS -X POST "$BASE_URL/sign-multi-chain/chains" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json')
echo "$CHAINS"
echo "$CHAINS" | grep -q '"ethereum"' || fail "ethereum not in chains"
echo "$CHAINS" | grep -q '"bitcoin"' || fail "bitcoin not in chains"
echo "$CHAINS" | grep -q '"solana"' || fail "solana not in chains"
echo "$CHAINS" | grep -q '"cosmos-hub"' || fail "cosmos-hub not in chains"

say "sign on ethereum"
ETH=$(curl -fsS -X POST "$BASE_URL/sign-multi-chain" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{"chainId":"ethereum","message":"0xdeadbeef","metadata":{"network":"mainnet"}}')
echo "$ETH"
ETH_REQ=$(printf '%s' "$ETH" | sed -n 's/.*"requestId":"\([^"]*\)".*/\1/p')
[ -n "$ETH_REQ" ] || fail "no requestId for ethereum"
echo "$ETH" | grep -q '"ethereum"' || fail "wrong chainId in response"
echo "$ETH" | grep -q '"signed"' || fail "ethereum not signed"

say "sign on bitcoin"
BTC=$(curl -fsS -X POST "$BASE_URL/sign-multi-chain" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{"chainId":"bitcoin","message":"0xaabbccdd00112233445566778899aabbccdd00112233445566778899aabbccdd","metadata":{"network":"testnet"}}')
echo "$BTC"
BTC_REQ=$(printf '%s' "$BTC" | sed -n 's/.*"requestId":"\([^"]*\)".*/\1/p')
[ -n "$BTC_REQ" ] || fail "no requestId for bitcoin"
echo "$BTC" | grep -q '"bitcoin"' || fail "wrong chainId for bitcoin"
echo "$BTC" | grep -q '"signed"' || fail "bitcoin not signed"

say "sign on solana"
SOL=$(curl -fsS -X POST "$BASE_URL/sign-multi-chain" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{"chainId":"solana","message":"0xbbccddee00112233445566778899aabbccddee00112233445566778899aabbccdd","metadata":{"recentBlockhash":"4MZYZ1Jb3sP9X8jYy7YxYyPq3YyXx8j5k5j5k5j5k5j5k5j5k5j5k5j5k5j5k5j"}}')
echo "$SOL"
SOL_REQ=$(printf '%s' "$SOL" | sed -n 's/.*"requestId":"\([^"]*\)".*/\1/p')
[ -n "$SOL_REQ" ] || fail "no requestId for solana"
echo "$SOL" | grep -q '"solana"' || fail "wrong chainId for solana"
echo "$SOL" | grep -q '"signed"' || fail "solana not signed"

say "sign on cosmos-hub"
COSMOS=$(curl -fsS -X POST "$BASE_URL/sign-multi-chain" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{"chainId":"cosmos-hub","message":"0xccddee00112233445566778899aabbccddee00112233445566778899aabbccddee","metadata":{"account_number":42,"sequence":0}}')
echo "$COSMOS"
COSMOS_REQ=$(printf '%s' "$COSMOS" | sed -n 's/.*"requestId":"\([^"]*\)".*/\1/p')
[ -n "$COSMOS_REQ" ] || fail "no requestId for cosmos"
echo "$COSMOS" | grep -q '"cosmos-hub"' || fail "wrong chainId for cosmos"
echo "$COSMOS" | grep -q '"signed"' || fail "cosmos not signed"
echo "$COSMOS" | grep -q '"cosmos1' || fail "cosmos address not bech32"

say "unsupported chain is rejected"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/sign-multi-chain" \
  -H "Authorization: Bearer $API_KEY" -H 'Content-Type: application/json' \
  -d '{"chainId":"unknown-chain","message":"0xdeadbeef"}')
[ "$code" = "400" ] || fail "expected 400 for unsupported chain, got $code"

say "unauthenticated multi-chain is rejected"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/sign-multi-chain" \
  -H 'Content-Type: application/json' \
  -d '{"chainId":"ethereum","message":"0xdeadbeef"}')
[ "$code" = "401" ] || fail "expected 401 for unauthenticated, got $code"

say "signatures differ across chains for same message"
# Verify that ethereum and cosmos signatures differ (different signing algorithms)
ETH_SIG=$(printf '%s' "$ETH" | sed -n 's/.*"signature":"\([^"]*\)".*/\1/p')
COSMOS_SIG=$(printf '%s' "$COSMOS" | sed -n 's/.*"signature":"\([^"]*\)".*/\1/p')
[ "$ETH_SIG" != "$COSMOS_SIG" ] || fail "signatures should differ across chains"

printf '\nALL MULTI-CHAIN SMOKE CHECKS PASSED\n'
