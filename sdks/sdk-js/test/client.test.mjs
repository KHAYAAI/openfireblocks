// SDK tests using Node's built-in test runner against the compiled dist output
// with a stubbed fetch (no network).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OpenFireblocksClient, OpenFireblocksError } from '../dist/index.js';

function stubFetch(handler) {
  return async (url, init) => handler(url, init);
}

test('sign sends auth header and returns parsed body', async () => {
  let seenUrl, seenInit;
  const client = new OpenFireblocksClient({
    baseUrl: 'http://gw',
    apiKey: 'k1',
    fetch: stubFetch((url, init) => {
      seenUrl = url;
      seenInit = init;
      return {
        ok: true,
        status: 200,
        text: async () =>
          JSON.stringify({
            requestId: 'r1',
            signedTx: '0xabc',
            txHash: '0xhash',
            from: '0xfrom',
            status: 'signed',
            broadcasted: false,
          }),
      };
    }),
  });

  const res = await client.sign({
    chainId: 11155111,
    to: '0x742d35Cc6634C0532925a3b844Bc9e7595f42bE',
    gasLimit: 21000,
    gasPrice: '20000000000',
    nonce: 0,
  });

  assert.equal(seenUrl, 'http://gw/sign');
  assert.equal(seenInit.method, 'POST');
  assert.equal(seenInit.headers.Authorization, 'Bearer k1');
  assert.equal(res.requestId, 'r1');
  assert.equal(res.status, 'signed');
});

test('non-2xx throws OpenFireblocksError with body', async () => {
  const client = new OpenFireblocksClient({
    baseUrl: 'http://gw/',
    apiKey: 'k1',
    fetch: stubFetch(() => ({
      ok: false,
      status: 403,
      text: async () => JSON.stringify({ error: 'policy denied' }),
    })),
  });

  await assert.rejects(
    () =>
      client.sign({
        chainId: 1,
        to: '0xabc',
        gasLimit: 21000,
        gasPrice: '1',
        nonce: 0,
      }),
    (err) => {
      assert.ok(err instanceof OpenFireblocksError);
      assert.equal(err.status, 403);
      assert.deepEqual(err.body, { error: 'policy denied' });
      return true;
    },
  );
});

test('getTransaction encodes the id into the path', async () => {
  let seenUrl;
  const client = new OpenFireblocksClient({
    baseUrl: 'http://gw',
    apiKey: 'k1',
    fetch: stubFetch((url) => {
      seenUrl = url;
      return { ok: true, status: 200, text: async () => JSON.stringify({ request_id: 'a b' }) };
    }),
  });
  await client.getTransaction('a b');
  assert.equal(seenUrl, 'http://gw/transactions/a%20b');
});
