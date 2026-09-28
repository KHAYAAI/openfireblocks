#!/usr/bin/env bash
#
# Tells a customer that something happened.
#
# The webhooks service could sign payloads, record every attempt, and retry
# with exponential backoff, and none of it had ever been used: nothing in
# the platform emitted an event, and there was no route to register an
# endpoint. So the delivery machinery operated on webhooks that could not
# exist, carrying events that were never sent. A customer integrating
# against this platform could not be told that their key had finished its
# DKG ceremony or that their transaction had been signed -- only poll for
# it, which for a ceremony taking a minute or two means polling hard or
# finding out late.
#
# This drill stands up a real receiver, registers it the way a customer
# would, does something worth hearing about, and checks what arrived.
#
# The receiver speaks HTTPS with a certificate from a CA created here,
# because the platform refuses to deliver over plaintext -- the payload
# says what a customer signed and for how much, and a signature proves
# origin, not confidentiality. Nothing in this drill disables verification;
# the CA is trusted explicitly, the way a customer behind a corporate CA
# would have theirs trusted.
set -euo pipefail

API="${API:-http://127.0.0.1:3000}"
NS="${K8S_NAMESPACE:-openfireblocks}"
ADMIN_KEY="${ADMIN_KEY:-dev-admin-api-key}"
CURL=(curl -sS --noproxy '*')
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
jqp() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }

S=$(date +%s)
RECEIVER="webhook-receiver-${S}"

echo "==> a CA, and a certificate for the receiver"
# openssl rather than a fixture: a checked-in certificate expires, and the
# drill would then fail for a reason that has nothing to do with webhooks.
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -subj "/CN=openfireblocks drill CA" \
  -keyout "${WORK}/ca.key" -out "${WORK}/ca.crt" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
  -subj "/CN=${RECEIVER}" \
  -keyout "${WORK}/tls.key" -out "${WORK}/tls.csr" >/dev/null 2>&1
# The SAN has to name the in-cluster service DNS, because that is the name
# the webhooks service will dial and the name TLS verifies against.
cat > "${WORK}/ext" <<EXT
subjectAltName = DNS:${RECEIVER}, DNS:${RECEIVER}.${NS}.svc.cluster.local
EXT
openssl x509 -req -in "${WORK}/tls.csr" -CA "${WORK}/ca.crt" -CAkey "${WORK}/ca.key" \
  -CAcreateserial -days 2 -extfile "${WORK}/ext" -out "${WORK}/tls.crt" >/dev/null 2>&1
echo "    CA and a certificate for ${RECEIVER}"

echo "==> a receiver that records what it is sent"
kubectl -n "${NS}" delete secret webhook-drill-ca webhook-drill-tls --ignore-not-found >/dev/null 2>&1
kubectl -n "${NS}" create secret generic webhook-drill-ca --from-file=ca.crt="${WORK}/ca.crt" >/dev/null
kubectl -n "${NS}" create secret tls webhook-drill-tls \
  --cert="${WORK}/tls.crt" --key="${WORK}/tls.key" >/dev/null

# Runs on the api-gateway image because it is already in the cluster and
# has a Node runtime. The receiver keeps every request in memory and serves
# them back, so the drill can assert on exactly what the platform sent
# rather than on the platform's own account of it.
kubectl -n "${NS}" delete deploy "${RECEIVER}" --ignore-not-found --wait=true >/dev/null 2>&1
kubectl -n "${NS}" delete svc "${RECEIVER}" --ignore-not-found >/dev/null 2>&1
GATEWAY_IMAGE=$(kubectl -n "${NS}" get deploy ofb-openfireblocks-api-gateway \
  -o jsonpath='{.spec.template.spec.containers[0].image}')

cat <<YAML | kubectl apply -f - >/dev/null
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${RECEIVER}
  namespace: ${NS}
spec:
  replicas: 1
  selector:
    matchLabels: { app: ${RECEIVER} }
  template:
    metadata:
      labels: { app: ${RECEIVER} }
    spec:
      volumes:
        - name: tls
          secret: { secretName: webhook-drill-tls }
      containers:
        - name: receiver
          image: ${GATEWAY_IMAGE}
          imagePullPolicy: IfNotPresent
          volumeMounts:
            - name: tls
              mountPath: /etc/receiver-tls
              readOnly: true
          command: ["node", "-e"]
          args:
            - |
              const https = require('https'), fs = require('fs');
              const received = [];
              https.createServer({
                key: fs.readFileSync('/etc/receiver-tls/tls.key'),
                cert: fs.readFileSync('/etc/receiver-tls/tls.crt'),
              }, (req, res) => {
                if (req.method === 'GET') {
                  res.writeHead(200, {'Content-Type': 'application/json'});
                  return res.end(JSON.stringify(received));
                }
                let body = '';
                req.on('data', (c) => { body += c; });
                req.on('end', () => {
                  received.push({ headers: req.headers, body });
                  res.writeHead(200);
                  res.end('ok');
                });
              }).listen(8443, () => console.log('receiver listening on 8443'));
          ports:
            - containerPort: 8443
---
apiVersion: v1
kind: Service
metadata:
  name: ${RECEIVER}
  namespace: ${NS}
spec:
  selector: { app: ${RECEIVER} }
  ports:
    - port: 443
      targetPort: 8443
YAML
kubectl -n "${NS}" rollout status deploy/"${RECEIVER}" --timeout=180s >/dev/null
echo "    https://${RECEIVER} is up"

echo "==> teaching the platform to trust the receiver's CA"
# Not "skip verification". The platform trusts this CA explicitly, exactly
# as it would trust a customer's corporate CA, and still verifies the
# certificate against it.
helm upgrade ofb "$(dirname "$0")/../helm/openfireblocks" \
  -f "$(dirname "$0")/values-kind.yaml" -n "${NS}" \
  --set webhooks.trustedCASecret=webhook-drill-ca --reuse-values >/dev/null
# Forced, not left to Helm. On a re-run the pod spec is already what the
# upgrade wants, so Helm changes nothing and the pod keeps running with the
# CA it loaded at startup -- which on the first run of this drill meant a
# service that had never read the new CA reporting every delivery as an
# untrusted certificate.
kubectl -n "${NS}" rollout restart deploy/ofb-openfireblocks-webhooks >/dev/null
kubectl -n "${NS}" rollout status deploy/ofb-openfireblocks-webhooks --timeout=300s >/dev/null
echo "    webhooks service restarted with the drill CA"

echo "==> a customer registering an endpoint"
customer=$("${CURL[@]}" -X POST "${API}/admin/customers" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" \
  -d "{\"email\":\"hook-${S}@example.com\",\"name\":\"hook-${S}\",\"tier\":\"enterprise\"}")
api_key=$(echo "${customer}" | jqp 'd["api_key"]')
customer_id=$(echo "${customer}" | jqp 'd["customer_id"]')

registered=$("${CURL[@]}" -w '\n%{http_code}' -X POST "${API}/webhooks" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"url\":\"https://${RECEIVER}\",\"events\":[\"key.created\",\"signature.created\"]}")
code=$(echo "${registered}" | tail -1)
body=$(echo "${registered}" | sed '$d')
[[ "${code}" == "201" ]] || fail "registering the endpoint returned ${code}: ${body}"
WEBHOOK_ID=$(echo "${body}" | jqp 'd["webhook_id"]')
SECRET=$(echo "${body}" | jqp 'd["secret"]')
[[ -n "${SECRET}" && "${SECRET}" != "None" ]] \
  || fail "no signing secret was returned; the receiver could not authenticate anything"
echo "    webhook ${WEBHOOK_ID}, subscribed to key.created and signature.created"

echo "==> checking a plaintext endpoint is refused"
# The payload says what a customer signed and for how much. A signature
# proves origin, not confidentiality, so http:// has to be refused at
# registration rather than delivered to.
plain=$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST "${API}/webhooks" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d '{"url":"http://insecure.example.com","events":["key.created"]}')
[[ "${plain}" == "400" ]] || fail "a plaintext endpoint was accepted with ${plain}"

# And an event name that will never fire, which otherwise looks exactly
# like a broken integration: the customer waits and nothing arrives.
unknown=$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST "${API}/webhooks" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d '{"url":"https://example.com","events":["key.exploded"]}')
[[ "${unknown}" == "400" ]] || fail "a subscription to a nonexistent event was accepted with ${unknown}"
echo "    plaintext and unknown-event registrations both refused"

echo "==> doing something worth hearing about"
"${CURL[@]}" -o /dev/null -X PUT "${API}/admin/customers/${customer_id}/raw-digest-signing" \
  -H 'Content-Type: application/json' -H "x-admin-key: ${ADMIN_KEY}" -d '{"enabled":true}'

created=$("${CURL[@]}" -X POST "${API}/keys" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"blockchain\":\"ethereum\",\"threshold\":2,\"total_parties\":3,\"name\":\"hook-${S}\"}")
key_id=$(echo "${created}" | jqp 'd["id"]')
for _ in $(seq 1 120); do
  key=$("${CURL[@]}" -H "x-api-key: ${api_key}" "${API}/keys/${key_id}")
  status=$(echo "${key}" | jqp 'd.get("status")')
  [[ "${status}" != "pending_dkg" ]] && break
  sleep 5
done
[[ "${status}" == "active" ]] || fail "key did not activate: ${key}"

DIGEST=$(printf '%064x' $((0xfeed)))
signed=$("${CURL[@]}" -X POST "${API}/keys/${key_id}/sign" \
  -H 'Content-Type: application/json' -H "x-api-key: ${api_key}" \
  -d "{\"message\":\"${DIGEST}\",\"to\":\"0x1111111111111111111111111111111111111111\",\"value\":\"1000\",\"chainId\":1}")
echo "${signed}" | jqp 'd["signature"]' >/dev/null || fail "signing failed: ${signed}"
echo "    1 key provisioned, 1 signature produced"

echo "==> what actually arrived at the receiver"
# Delivery is asynchronous by design -- the customer's signature does not
# wait on their notification endpoint -- so this polls rather than assuming.
for _ in $(seq 1 30); do
  # Read back over the node https module rather than fetch: fetch has no
  # way to reach a server by an address its certificate does not name, and
  # this is the receiver talking to itself on loopback.
  arrived=$(kubectl -n "${NS}" exec deploy/"${RECEIVER}" -- node -e '
    const https = require("https");
    https.get({host:"127.0.0.1",port:8443,path:"/",rejectUnauthorized:false}, (res) => {
      let body = ""; res.on("data", (c) => { body += c; });
      res.on("end", () => console.log(body));
    }).on("error", () => console.log("[]"));
  ' 2>/dev/null || echo '[]')
  COUNT=$(echo "${arrived}" | python3 -c "import sys,json;print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)
  [[ "${COUNT}" -ge 2 ]] && break
  sleep 3
done
[[ "${COUNT}" -ge 2 ]] \
  || fail "the receiver got ${COUNT} deliveries; expected key.created and signature.created"
echo "    ${COUNT} deliveries received"

echo "${arrived}" | SECRET="${SECRET}" DIGEST="${DIGEST}" KEY_ID="${key_id}" python3 -c "
import sys, os, json, hmac, hashlib

deliveries = json.load(sys.stdin)
secret = os.environ['SECRET'].encode()
types = set()

for d in deliveries:
    event = json.loads(d['body'])
    types.add(event['event_type'])

    # The signature is the only thing that lets a receiver tell our POST
    # from anybody else's. A webhook that cannot be authenticated is an
    # open endpoint that anyone can drive.
    headers = {k.lower(): v for k, v in d['headers'].items()}
    sent = next((v for k, v in headers.items() if 'signature' in k), None)
    assert sent, f'{event[\"event_type\"]} arrived with no signature header: {sorted(headers)}'
    expected = hmac.new(secret, d['body'].encode(), hashlib.sha256).hexdigest()
    assert expected in sent, (
        f'{event[\"event_type\"]}: the signature does not verify against the secret '
        'the customer was given'
    )

    assert event.get('event_id'), 'an event arrived with no id to correlate it by'
    assert event.get('customer_id'), 'an event arrived without naming the tenant'

assert 'key.created' in types, f'no key.created arrived: {sorted(types)}'
assert 'signature.created' in types, f'no signature.created arrived: {sorted(types)}'

# The payload has to carry enough to act on. An event saying only 'a
# signature happened' forces the customer to poll anyway, which is the
# thing webhooks exist to avoid.
sig = next(json.loads(d['body']) for d in deliveries
           if json.loads(d['body'])['event_type'] == 'signature.created')
assert sig['data']['key_id'] == os.environ['KEY_ID'], 'the event names the wrong key'
assert sig['data']['message'] == os.environ['DIGEST'], 'the event does not say what was signed'
assert sig['data']['parties'], 'the event does not say which parties signed'

print('    every delivery is signed and verifies against the customer secret')
print('    signature.created names the key, the digest, and the committee')
" || fail "the deliveries did not carry what a customer needs"

echo "==> what the platform says it delivered"
deliveries=$("${CURL[@]}" -H "x-api-key: ${api_key}" \
  "${API}/webhooks/deliveries?webhook_id=${WEBHOOK_ID}")
SUCCEEDED=$(echo "${deliveries}" | python3 -c "
import sys, json
rows = json.load(sys.stdin)
print(sum(1 for r in rows if r.get('success')))
")
[[ "${SUCCEEDED}" -ge 2 ]] \
  || fail "the platform recorded ${SUCCEEDED} successful deliveries; the receiver got ${COUNT}"
echo "    ${SUCCEEDED} recorded as delivered, matching what arrived"

echo "==> cleaning up"
kubectl -n "${NS}" delete deploy "${RECEIVER}" --ignore-not-found >/dev/null 2>&1
kubectl -n "${NS}" delete svc "${RECEIVER}" --ignore-not-found >/dev/null 2>&1

echo
echo "PASS: a customer registered an HTTPS endpoint through the public API,"
echo "      provisioned a key and signed one digest, and ${COUNT} events reached"
echo "      their receiver over verified TLS -- each signed with the secret"
echo "      they were handed at registration, each naming the tenant, and the"
echo "      signature event carrying the key, the digest and the committee"
echo "      that produced it. Plaintext endpoints and subscriptions to events"
echo "      that do not exist were both refused at registration."
