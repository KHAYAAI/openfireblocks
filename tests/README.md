# Tests

## Unit tests (run in CI)
- Gateway: `cd services/api-gateway && npm test`
- Go services: `cd services/<svc> && go test ./...`
- SDK: `cd sdks/sdk-js && npm test`

## Smoke test (against a running stack)
End-to-end check of health, auth, signing, tenant isolation and the audit trail.

```bash
cd infrastructure && docker compose up -d --build
BASE_URL=http://localhost:3000 ./tests/smoke/smoke.sh
```

## Load test (k6)
Validates the p99 < 500ms SLO under load. Install [k6](https://k6.io), then:

```bash
k6 run -e BASE_URL=http://localhost:3000 -e API_KEY=dev-demo-key tests/load/k6-script.js
```

Run with broadcasting disabled (no `ETHEREUM_RPC_SEPOLIA`) to exercise the sign
path without on-chain nonce contention from the single shared key.
