import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer as httpServer, IncomingMessage, ServerResponse } from 'node:http';
import { AddressInfo } from 'node:net';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { InMemoryTransport } from '@modelcontextprotocol/sdk/inMemory.js';
import { createServer } from '../src/server.js';
import { gatewayClient } from '../src/gateway.js';

// A stand-in gateway that records what the MCP server asked for and answers as the real
// agent routes do. The real routes are tested in the gateway (agents.live.spec.ts,
// agent-broadcast.spec.ts); these test the translation between them and an AI client.
type Handler = (req: { method: string; url: string; body: any; auth: string }) => { status: number; body: unknown };

async function harness(handler: Handler) {
  const seen: Array<{ method: string; url: string; body: any; auth: string }> = [];
  const srv = httpServer(async (req: IncomingMessage, res: ServerResponse) => {
    let raw = '';
    for await (const c of req) raw += c;
    const r = { method: req.method!, url: req.url!, body: raw ? JSON.parse(raw) : undefined, auth: String(req.headers.authorization ?? '') };
    seen.push(r);
    const out = handler(r);
    res.writeHead(out.status, { 'content-type': 'application/json' });
    res.end(JSON.stringify(out.body));
  });
  await new Promise<void>((ok) => srv.listen(0, '127.0.0.1', ok));
  const url = `http://127.0.0.1:${(srv.address() as AddressInfo).port}`;
  const mcp = createServer(gatewayClient(url, 'ofb_agent_test'));
  const [a, b] = InMemoryTransport.createLinkedPair();
  const client = new Client({ name: 'test', version: '0' });
  await Promise.all([mcp.connect(a), client.connect(b)]);
  return {
    client,
    seen,
    close: async () => { await client.close(); await mcp.close(); await new Promise((ok) => srv.close(() => ok(undefined))); },
  };
}

const text = (r: any) => r.content.map((c: any) => c.text).join('\n');

test('offers five tools, and only send_payment can move money', async () => {
  const h = await harness(() => ({ status: 200, body: {} }));
  const { tools } = await h.client.listTools();
  assert.deepEqual(tools.map((t) => t.name).sort(), ['get_payment', 'get_spending_status', 'list_payment_tokens', 'list_payments', 'send_payment']);
  for (const t of tools) {
    assert.equal(t.annotations?.readOnlyHint, t.name !== 'send_payment', `${t.name} read-only hint`);
  }
  assert.equal(tools.find((t) => t.name === 'send_payment')!.annotations?.destructiveHint, true);
  await h.close();
});

test('reads the spending status with the agent key and summarises it', async () => {
  const h = await harness(() => ({
    status: 200,
    body: { agentId: 'a1', allowedTokens: ['USDC'], allowedRecipients: ['0x70997970C51812dc3A010C7d01b50e0d17dc79C8'], perTransferLimitZar: 5000, dailyLimitZar: 10000, remaining24hZar: 8150 },
  }));
  const r: any = await h.client.callTool({ name: 'get_spending_status', arguments: {} });
  assert.equal(h.seen[0].url, '/agent/me');
  assert.equal(h.seen[0].auth, 'Bearer ofb_agent_test');
  assert.match(text(r), /R5000 per payment; R8150 of your R10000/);
  assert.match(text(r), /Only these recipients/);
  await h.close();
});

test('pays: sends exactly the asked-for payment with an idempotency key, and reports the transaction', async () => {
  const h = await harness(({ method }) => method === 'POST'
    ? { status: 200, body: { broadcast: true, transaction_hash: '0xabc', token: { symbol: 'USDC' }, amount: '125.50', recipient: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', parties: [1, 2], agent: { valueZar: 2322, remaining24hZar: 7678 } } }
    : { status: 404, body: {} });
  const r: any = await h.client.callTool({
    name: 'send_payment',
    arguments: { token: 'USDC', recipient: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', amount: '125.50', chain_id: 11155111, purpose: 'invoice INV-1043' },
  });
  assert.equal(r.isError, undefined);
  const sent = h.seen[0];
  assert.equal(sent.url, '/agent/transfers');
  assert.deepEqual({ ...sent.body, idempotencyKey: undefined }, {
    token: 'USDC', recipient: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', amount: '125.50', chainId: 11155111, purpose: 'invoice INV-1043', idempotencyKey: undefined,
  });
  assert.match(sent.body.idempotencyKey, /^mcp-[0-9a-f-]{36}$/);
  assert.equal(r.structuredContent.explorer, 'https://sepolia.etherscan.io/tx/0xabc');
  assert.equal(r.structuredContent.idempotency_key, sent.body.idempotencyKey);
  assert.match(text(r), /PAID: 125.50 USDC/);
  await h.close();
});

test('a retry with the caller\'s idempotency key is sent with that key', async () => {
  const h = await harness(() => ({ status: 200, body: { broadcast: true, transaction_hash: '0xabc' } }));
  await h.client.callTool({ name: 'send_payment', arguments: { token: 'USDC', recipient: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', amount: '1', chain_id: 1337, purpose: 'order 77', idempotency_key: 'order-77-attempt' } });
  assert.equal(h.seen[0].body.idempotencyKey, 'order-77-attempt');
  await h.close();
});

test('a retry the gateway recognises is reported as already paid, not paid again', async () => {
  const h = await harness(() => ({ status: 200, body: { replayed: true, broadcast: true, transaction_hash: '0xfirst', agent: { remainingZar: 1 } } }));
  const r: any = await h.client.callTool({ name: 'send_payment', arguments: { token: 'USDC', recipient: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', amount: '1', chain_id: 1337, purpose: 'order 77', idempotency_key: 'order-77' } });
  assert.equal(r.structuredContent.outcome, 'already_paid');
  assert.match(text(r), /ALREADY PAID.*Nothing was paid again/s);
  await h.close();
});

test('a refusal is reported as final, with the instruction not to work around it', async () => {
  const h = await harness(() => ({ status: 403, body: { statusCode: 403, message: 'recipient is not on this agent\'s list' } }));
  const r: any = await h.client.callTool({ name: 'send_payment', arguments: { token: 'USDC', recipient: '0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC', amount: '10', chain_id: 1337, purpose: 'test' } });
  assert.equal(r.isError, true);
  assert.equal(r.structuredContent.outcome, 'refused');
  assert.match(text(r), /REFUSED: recipient is not on this agent's list/);
  assert.match(text(r), /Do not split the amount/);
  await h.close();
});

test('a payment held for approval tells the agent a person must act, and not to retry', async () => {
  const h = await harness(() => ({ status: 403, body: { error: 'approval required', requiresApproval: true, reasons: ['high-value transfer'] } }));
  const r: any = await h.client.callTool({ name: 'send_payment', arguments: { token: 'USDC', recipient: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', amount: '10', chain_id: 1337, purpose: 'test' } });
  assert.equal(r.structuredContent.outcome, 'approval_required');
  assert.match(text(r), /HELD FOR APPROVAL/);
  await h.close();
});

test('a malformed recipient never reaches the gateway', async () => {
  const h = await harness(() => ({ status: 200, body: {} }));
  const r: any = await h.client.callTool({ name: 'send_payment', arguments: { token: 'USDC', recipient: 'alice.eth', amount: '10', chain_id: 1, purpose: 'test' } }).catch((e) => ({ isError: true, content: [{ text: String(e) }] }));
  assert.equal(r.isError, true);
  assert.equal(h.seen.length, 0);
  await h.close();
});

test('checks one payment, asking the gateway for on-chain confirmation when given the chain', async () => {
  const h = await harness(() => ({ status: 200, body: { requestId: 'inv 9', status: 'spent', txHash: '0xabc', onChain: { state: 'confirmed', blockNumber: 42 } } }));
  const r: any = await h.client.callTool({ name: 'get_payment', arguments: { idempotency_key: 'inv 9', chain_id: 11155111 } });
  assert.equal(h.seen[0].url, '/agent/transfers/inv%209?chainId=11155111');
  assert.match(text(r), /spent\. On-chain: confirmed in block 42/);
  await h.close();
});

test('a gateway it cannot reach is reported as unavailable, pointing at get_payment before any retry', async () => {
  const mcp = createServer(gatewayClient('http://127.0.0.1:1', 'ofb_agent_test', 2000));
  const [a, b] = InMemoryTransport.createLinkedPair();
  const client = new Client({ name: 'test', version: '0' });
  await Promise.all([mcp.connect(a), client.connect(b)]);
  const r: any = await client.callTool({ name: 'send_payment', arguments: { token: 'USDC', recipient: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', amount: '1', chain_id: 1337, purpose: 'test' } });
  assert.equal(r.structuredContent.outcome, 'unavailable');
  assert.match(text(r), /check get_payment|Check get_payment/);
  await client.close(); await mcp.close();
});

test('refuses to start with anything but an agent key, or over plain http to a remote host', () => {
  const entry = fileURLToPath(new URL('../src/index.js', import.meta.url));
  const run = (env: Record<string, string>) => spawnSync(process.execPath, [entry], { env: { PATH: process.env.PATH ?? '', ...env }, encoding: 'utf8', timeout: 10_000 });
  assert.match(run({ FORGE_API_URL: 'https://x.example', FORGE_AGENT_KEY: 'ofb_live_customerkey' }).stderr, /must be an agent key/);
  assert.match(run({ FORGE_API_URL: 'http://custody.example.com', FORGE_AGENT_KEY: 'ofb_agent_x' }).stderr, /must be https/);
  assert.match(run({}).stderr, /must be set/);
});
