// k6 load test for the OpenFireblocks sign endpoint.
//
//   k6 run -e BASE_URL=http://localhost:3000 -e API_KEY=dev-demo-key tests/load/k6-script.js
//
// Ramps to 200 virtual users and asserts the Phase 1 SLOs: p99 latency < 500ms
// and a >99% success rate. Note: a single shared signer nonce means heavy
// concurrent broadcasting will conflict on-chain — run with broadcasting
// disabled (no ETHEREUM_RPC_SEPOLIA) to load-test the sign path itself.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

const errorRate = new Rate('errors');

export const options = {
  stages: [
    { duration: '30s', target: 50 },
    { duration: '1m', target: 200 },
    { duration: '1m', target: 200 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    http_req_duration: ['p(99)<500'],
    errors: ['rate<0.01'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:3000';
const API_KEY = __ENV.API_KEY || 'dev-demo-key';

export default function () {
  const payload = JSON.stringify({
    chainId: 11155111,
    to: '0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045',
    data: '0x',
    value: '0',
    gasLimit: 21000,
    gasPrice: '20000000000',
    nonce: 0,
  });

  const res = http.post(`${BASE_URL}/sign`, payload, {
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${API_KEY}`,
    },
  });

  // 200 = signed; 403 = policy/velocity denial (still a valid, fast response).
  const ok = check(res, {
    'status is 200 or 403': (r) => r.status === 200 || r.status === 403,
  });
  errorRate.add(!ok);

  sleep(0.1);
}
