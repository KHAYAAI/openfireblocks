#!/usr/bin/env bash
#
# Turns platform activity into an invoice.
#
# Billing had two working ends and no middle. Subscribe created
# subscriptions. ChargeInvoice collected payment and was tested against
# Stripe's exact request format. Between them: RecordUsage took a map of
# counters from a caller, and no caller anywhere in the platform ever
# called it. Nothing created an invoice from usage. So the only invoices
# that could exist were ones somebody wrote by hand, and a customer could
# use the platform for a year without generating a single charge.
#
# This drill does the thing a month-end job would do, in order, against the
# real services: subscribe a customer, make them do measurable work, count
# it, raise the invoice, and check the arithmetic against what actually
# happened.
#
# The usage numbers are counted from signing_requests and key_pairs rather
# than pushed in by whatever did the work. That decision is what this drill
# is really testing: a counter incremented at the point of work is lost on
# a crash, doubled on a retry, and impossible to reconcile later. Counting
# the rows the platform already writes means the invoice and the audit
# trail cannot disagree, because they are the same rows.
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
BILLING="ofb-openfireblocks-billing:8085"
CURL=(curl -sS --noproxy '*')

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

psql_admin() {
  kubectl -n "${NS}" exec deploy/postgres -- psql -U postgres -d openfireblocks -tAc "$1" 2>/dev/null
}

# Billing is an internal service with no ingress, so it is reached from
# inside the cluster -- the same path the platform itself would use, rather
# than one that does not exist in production.
billing() {
  local method="$1" path="$2" body="${3:-}"
  kubectl -n "${NS}" exec deploy/ofb-openfireblocks-api-gateway -- \
    node -e '
      const [url, method, body] = process.argv.slice(1);
      const opts = { method, headers: {}, signal: AbortSignal.timeout(30000) };
      if (body) { opts.body = body; opts.headers["Content-Type"] = "application/json"; }
      fetch(url, opts).then(async (r) => {
        const text = await r.text();
        console.log(JSON.stringify({ status: r.status, body: text }));
      }).catch((e) => console.log(JSON.stringify({ status: 0, body: String(e && e.message) })));
    ' "http://${BILLING}${path}" "${method}" "${body}" 2>/dev/null
}

body_of() { python3 -c "import sys,json;print(json.load(sys.stdin)['body'])"; }
status_of() { python3 -c "import sys,json;print(json.load(sys.stdin)['status'])"; }

S=$(date +%s)

echo "==> a plan with limits low enough to exceed"
# Deliberately small: the point is to prove overage is charged at the right
# rate, and a realistic 1,000-signature allowance would need 1,001
# threshold ceremonies to cross.
PLAN_ID=$(psql_admin "
  INSERT INTO plans (name, description, price_cents, currency, billing_cycle,
                     signing_limit, key_limit, support_level,
                     overage_signing_cents, overage_key_cents)
  VALUES ('drill-${S}', 'billing drill', 50000, 'usd', 'monthly',
          2, 5, 'standard', 700, 1500)
  RETURNING plan_id;" | head -1 | tr -d '[:space:]')
[[ -n "${PLAN_ID}" ]] || fail "could not create a plan"
echo "    plan ${PLAN_ID}: \$500.00/mo, 2 signatures and 5 keys included"
echo "    overage \$7.00 per signature, \$15.00 per key"

echo "==> a customer, subscribed"
customer=$("${CURL[@]}" -X POST "${API}/admin/customers" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"email\":\"billing-${S}@example.com\",\"name\":\"billing-${S}\",\"tier\":\"enterprise\"}")
api_key=$(echo "${customer}" | jqp 'd["api_key"]')
customer_id=$(echo "${customer}" | jqp 'd["customer_id"]')

sub=$(billing POST /v1/subscribe "{\"customer_id\":\"${customer_id}\",\"plan_id\":\"${PLAN_ID}\"}")
[[ "$(echo "${sub}" | status_of)" == "201" ]] || fail "subscribe failed: $(echo "${sub}" | body_of)"
SUB_ID=$(echo "${sub}" | body_of | jqp 'd["subscription_id"]')
echo "    customer ${customer_id}"
echo "    subscription ${SUB_ID}"

# The subscription's period starts now, so everything below is work done
# inside it. Doing it in this order is the point: usage counted from before
# a subscription existed would be usage nobody agreed to pay for.

echo "==> doing work worth billing for"
"${CURL[@]}" -o /dev/null -X PUT "${API}/admin/customers/${customer_id}/raw-digest-signing" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d '{"enabled":true}'

created=$("${CURL[@]}" -X POST "${API}/keys" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"blockchain\":\"ethereum\",\"threshold\":2,\"total_parties\":3,\"name\":\"billing-${S}\"}")
key_id=$(echo "${created}" | jqp 'd["id"]')
for _ in $(seq 1 120); do
  key=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}")
  status=$(echo "${key}" | jqp 'd.get("status")')
  [[ "${status}" != "pending_dkg" ]] && break
  sleep 5
done
[[ "${status}" == "active" ]] || fail "key did not activate: ${key}"
echo "    1 key provisioned"

# Four signatures against an allowance of two, so two are overage.
SIGNATURES=4
for i in $(seq 1 ${SIGNATURES}); do
  digest=$(printf '%064x' "$((0xabc0 + i))")
  signed=$("${CURL[@]}" -X POST "${API}/keys/${key_id}/sign" \
    -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
    -d "{\"message\":\"${digest}\",\"to\":\"0x1111111111111111111111111111111111111111\",\"value\":\"1000\",\"chainId\":1}")
  echo "${signed}" | jqp 'd["signature"]' >/dev/null \
    || fail "signature ${i} failed: ${signed}"
done
echo "    ${SIGNATURES} signatures produced"

echo "==> measuring what was used"
usage=$(billing POST "/v1/usage/measure?subscription_id=${SUB_ID}")
[[ "$(echo "${usage}" | status_of)" == "200" ]] || fail "measuring usage failed: $(echo "${usage}" | body_of)"
metrics=$(echo "${usage}" | body_of)
MEASURED_SIGS=$(echo "${metrics}" | jqp 'd["signing_requests"]')
MEASURED_KEYS=$(echo "${metrics}" | jqp 'd["key_operations"]')
echo "    ${MEASURED_SIGS} signatures, ${MEASURED_KEYS} key(s)"

# The number billing counted has to be the number the platform actually
# did. This is the check that the metering is reading real work rather than
# a counter somebody remembered to increment.
[[ "${MEASURED_SIGS}" -eq "${SIGNATURES}" ]] \
  || fail "billing counted ${MEASURED_SIGS} signatures; the platform produced ${SIGNATURES}"
[[ "${MEASURED_KEYS}" -eq 1 ]] \
  || fail "billing counted ${MEASURED_KEYS} keys; one was provisioned"

# And it must agree with the audit trail, since they are the same rows.
RECORDED=$(psql_admin "SELECT COUNT(*) FROM signing_requests
  WHERE customer_id = '${customer_id}' AND status = 'completed';" | tr -d '[:space:]')
[[ "${RECORDED}" -eq "${MEASURED_SIGS}" ]] \
  || fail "the audit trail has ${RECORDED} signatures and billing counted ${MEASURED_SIGS}"

echo "==> raising the invoice"
inv=$(billing POST "/v1/invoices/generate?subscription_id=${SUB_ID}")
[[ "$(echo "${inv}" | status_of)" == "200" ]] || fail "generating the invoice failed: $(echo "${inv}" | body_of)"
invoice=$(echo "${inv}" | body_of)
INVOICE_ID=$(echo "${invoice}" | jqp 'd["invoice_id"]')
AMOUNT=$(echo "${invoice}" | jqp 'd["amount"]')
LINES=$(echo "${invoice}" | jqp 'len(d["line_items"])')

# $500.00 base + 2 signatures over at $7.00 = $514.00.
EXPECTED=$(( 50000 + (SIGNATURES - 2) * 700 ))
echo "    invoice ${INVOICE_ID}: ${LINES} line(s), ${AMOUNT} cents"
[[ "${AMOUNT}" -eq "${EXPECTED}" ]] \
  || fail "invoiced ${AMOUNT} cents, expected ${EXPECTED} (\$500 base + $((SIGNATURES - 2)) signatures over at \$7)"

# One key against an allowance of five is not overage, so there must be no
# key line. Charging for usage inside the allowance is the mistake that
# costs a customer relationship.
KEY_LINES=$(echo "${invoice}" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(sum(1 for i in d['line_items'] if 'Keys' in i['description']))
")
[[ "${KEY_LINES}" -eq 0 ]] || fail "charged key overage for 1 key against an allowance of 5"

# Every line has to be checkable by the person paying it.
echo "${invoice}" | python3 -c "
import sys, json
d = json.load(sys.stdin)
for item in d['line_items']:
    assert item['description'].strip(), 'a line item has no description'
    assert item['amount'] == item['quantity'] * item['unit_price'], \
        f\"{item['description']}: {item['quantity']} x {item['unit_price']} != {item['amount']}\"
assert sum(i['amount'] for i in d['line_items']) == d['amount'], 'the lines do not sum to the total'
print('    every line explains itself and the lines sum to the total')
" || fail "the invoice does not add up"

echo "==> generating it again"
# Invoice generation will be driven by a schedule, and a schedule that
# fires twice -- a retry, an overlapping run, an operator doing it by hand
# -- must not bill the same month twice.
again=$(billing POST "/v1/invoices/generate?subscription_id=${SUB_ID}")
AGAIN_ID=$(echo "${again}" | body_of | jqp 'd["invoice_id"]')
[[ "${AGAIN_ID}" == "${INVOICE_ID}" ]] \
  || fail "a second run raised invoice ${AGAIN_ID}; the customer would be billed twice"

# Exactly one. Subscribing used to raise its own base-rate invoice
# immediately, which double-billed every customer as soon as period
# invoicing existed -- the base rate on signing up, and the base rate again
# on the invoice that also carried their overage. Invisible until something
# generated period invoices, which is what this drill first did.
COUNT=$(psql_admin "SELECT COUNT(*) FROM invoices WHERE subscription_id = '${SUB_ID}';" | tr -d '[:space:]')
[[ "${COUNT}" -eq 1 ]] || fail "${COUNT} invoices exist for one subscription and one period"
echo "    the same invoice came back; ${COUNT} row in the database"

echo "==> trying to charge it"
# Stripe is not configured in this cluster and must not be faked. What
# matters is that the failure is explicit and the invoice is NOT marked
# paid -- an unpaid invoice recorded as paid is money that silently never
# arrives.
charge=$(billing POST /v1/invoices/charge \
  "{\"invoice_id\":\"${INVOICE_ID}\",\"customer_id\":\"${customer_id}\",\"stripe_customer_id\":\"cus_drill\"}")
CHARGE_STATUS=$(echo "${charge}" | status_of)
# 503, not 500: the request was fine and the deployment is unfinished. The
# distinction is what tells an operator to finish configuring payments
# rather than to go looking for a crash.
[[ "${CHARGE_STATUS}" == "503" ]] \
  || fail "charging with no payment processor answered ${CHARGE_STATUS}; expected 503"

PAID=$(psql_admin "SELECT status FROM invoices WHERE invoice_id = '${INVOICE_ID}';" | tr -d '[:space:]')
[[ "${PAID}" == "unpaid" ]] \
  || fail "the invoice is marked ${PAID} after a charge that never happened"
echo "    refused with ${CHARGE_STATUS}, and the invoice is still unpaid"

echo
echo "PASS: a customer subscribed, provisioned a key, produced ${SIGNATURES} signatures,"
echo "      and billing counted exactly that -- from the same rows that form"
echo "      the audit trail -- then raised a ${AMOUNT} cent invoice: \$500.00 base"
echo "      plus $((SIGNATURES - 2)) signatures of overage at \$7.00, with no charge for the"
echo "      key that was inside its allowance. Running generation again"
echo "      returned the same invoice rather than billing the month twice,"
echo "      and charging it with no payment processor configured was refused"
echo "      without marking anything paid."
