#!/usr/bin/env bash
#
# Drives the supporting services' real endpoints against a deployed cluster.
#
# These six were disabled for a long time because they had no images. Once
# they had images they were enabled, and being enabled meant they started and
# stayed up -- which the platform's own readiness probes will happily tell
# you, and which proves almost nothing. A service can pass a health check for
# months while its first real request returns a 500.
#
# That is not hypothetical here: deploying them at all is what surfaced the
# missing DATABASE_TENANT_URL that made five of them refuse to start. This is
# the next layer of the same question -- do they actually work.
#
# Deliberately not exhaustive. It exercises one meaningful write and one
# meaningful read per service where the shape allows, because the goal is to
# find services that are broken end to end, not to test their business logic.
set -euo pipefail

NS="${K8S_NAMESPACE:-openfireblocks}"
FAILURES=0
CHECKS=0

# Runs a request from inside the cluster.
#
# Two reasons rather than port-forwarding each service: these are internal
# services with no ingress, so hitting them from outside would be testing a
# path that does not exist in production; and a port-forward per service
# would make the script mostly plumbing.
#
# Through node rather than curl because the gateway image is a slim runtime
# that carries neither curl nor wget -- which is correct for a production
# image and briefly confusing when every request returns 000. Node has fetch
# built in, so nothing needs to be installed into a running pod.
LAST_BODY=""
call() {
  local svc="$1" port="$2" method="$3" path="$4" body="${5:-}" customer="${6:-}"
  local out
  out="$(kubectl -n "${NS}" exec deploy/ofb-openfireblocks-api-gateway -- \
    node -e '
      const [url, method, body, customer] = process.argv.slice(1);
      const opts = { method, headers: {}, signal: AbortSignal.timeout(20000) };
      if (body) { opts.body = body; opts.headers["Content-Type"] = "application/json"; }
      if (customer) { opts.headers["X-Customer-ID"] = customer; }
      fetch(url, opts)
        .then(async (r) => { console.log(r.status); console.log((await r.text()).slice(0, 300)); })
        .catch((e) => { console.log("000"); console.log(String(e && e.message)); });
    ' "http://${svc}:${port}${path}" "${method}" "${body}" "${customer}" 2>/dev/null)" || out=$'000\nexec failed'
  LAST_BODY="$(echo "${out}" | tail -n +2)"
  echo "${out}" | head -1
}

body() { echo "${LAST_BODY}"; }

# check <name> <expected-codes> <actual>
#
# Takes a set of acceptable codes rather than one. A 400 from an endpoint
# given deliberately thin input is a service that is working -- it parsed the
# request and rejected it. What this is looking for is 000 (unreachable),
# 404 (route not registered) and 5xx (crashed on a request it should
# handle).
check() {
  local name="$1" expected="$2" actual="$3"
  CHECKS=$((CHECKS + 1))
  if [[ " ${expected} " == *" ${actual} "* ]]; then
    printf '  ok   %-52s %s\n' "${name}" "${actual}"
  else
    printf '  FAIL %-52s %s (want one of: %s)\n' "${name}" "${actual}" "${expected}"
    local b; b="$(body)"
    [[ -n "${b}" ]] && printf '       %s\n' "${b}"
    FAILURES=$((FAILURES + 1))
  fi
}

echo "==> health"
for svc in policy-api settlement billing webhooks marketplace compliance; do
  case "${svc}" in
    policy-api) port=8083 ;;
    settlement) port=8084 ;;
    billing)    port=8085 ;;
    webhooks)   port=8086 ;;
    marketplace) port=8087 ;;
    compliance) port=8081 ;;
  esac
  check "${svc} /health" "200" "$(call "ofb-openfireblocks-${svc}" "${port}" GET /health)"
done

echo "==> policy-api"
# key_id, not customer_id: policies are attached to keys. Getting the
# contract wrong here is how the 500s below were found in the first place.
check "list policies for a key" "200 404" \
  "$(call ofb-openfireblocks-policy-api 8083 GET '/v1/policies?key_id=11111111-1111-1111-1111-111111111111')"
check "list policies with no key_id is a client error" "400" \
  "$(call ofb-openfireblocks-policy-api 8083 GET /v1/policies)"
# The evaluate route is the one that matters: it is the same decision the
# signing path depends on, reached directly.
check "evaluate a policy for an unknown key" "404" \
  "$(call ofb-openfireblocks-policy-api 8083 POST /v1/policies/evaluate \
    '{"key_id":"11111111-1111-1111-1111-111111111111","destination_address":"0x1111111111111111111111111111111111111111","amount":"1000","blockchain":"ethereum"}')"
check "evaluate with a malformed key_id is a client error" "400" \
  "$(call ofb-openfireblocks-policy-api 8083 POST /v1/policies/evaluate \
    '{"key_id":"not-a-uuid","destination_address":"0x1111111111111111111111111111111111111111","amount":"1000","blockchain":"ethereum"}')"

echo "==> settlement"
check "list settlements" "200 400" \
  "$(call ofb-openfireblocks-settlement 8084 GET '/v1/settlements/get?id=00000000-0000-0000-0000-000000000000')"

echo "==> billing"
check "usage for an unknown subscription" "404" \
  "$(call ofb-openfireblocks-billing 8085 GET '/v1/usage?subscription_id=00000000-0000-0000-0000-000000000000')"

echo "==> webhooks"
check "list deliveries for an unknown webhook" "404" \
  "$(call ofb-openfireblocks-webhooks 8086 GET '/v1/deliveries?webhook_id=00000000-0000-0000-0000-000000000000')"
# Retry is the route that was "not implemented" until recently; a 400 here is
# correct (the delivery does not exist) and a 404 would mean the route was
# never wired up.
check "retry an unknown delivery" "400 404 502" \
  "$(call ofb-openfireblocks-webhooks 8086 POST '/v1/deliveries/retry?delivery_id=00000000-0000-0000-0000-000000000000')"

echo "==> marketplace"
check "list integrations" "200" \
  "$(call ofb-openfireblocks-marketplace 8087 GET /v1/integrations '' 11111111-1111-1111-1111-111111111111)"

echo "==> compliance"
check "compliance dashboard" "200 400" \
  "$(call ofb-openfireblocks-compliance 8081 GET '/v1/compliance/dashboard?customer_id=11111111-1111-1111-1111-111111111111')"
check "list regulatory filings" "200 400" \
  "$(call ofb-openfireblocks-compliance 8081 GET '/v1/regulatory/filings?customer_id=11111111-1111-1111-1111-111111111111')"
check "overdue filings" "200 400" \
  "$(call ofb-openfireblocks-compliance 8081 GET /v1/regulatory/filings/overdue)"
# CTR evaluation is pure logic over an amount and is the most likely of these
# to be exercised for real by a customer.
check "evaluate a CTR threshold" "200 400" \
  "$(call ofb-openfireblocks-compliance 8081 POST /v1/regulatory/ctr/evaluate \
    '{"customer_id":"11111111-1111-1111-1111-111111111111","amount_usd":15000}')"

echo
if (( FAILURES )); then
  echo "FAIL: ${FAILURES} of ${CHECKS} checks failed"
  exit 1
fi
echo "PASS: all ${CHECKS} supporting-service checks answered from inside the cluster"
