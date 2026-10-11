// An AI agent paying through the MCP server, end to end, against the full local stack
// (run through e2e-fullstack-local.sh as E2E_AFTER):
//
//   a real 2-of-3 secp256k1 key from a real DKG -> a test rand token on a real EVM dev chain
//   (ganache) -> registered and verified by an operator -> an admin issues an agent key with
//   limits and one allowed recipient -> a real MCP client starts the MCP server over stdio and
//   pays, is refused, and is revoked.
//
// The chain is a development chain, not a public network. The agent's words are not
// generated here; its tool calls and the platform's answers are real.
const fs = require('fs');
const L = require('./lib-platform');

const st = JSON.parse(fs.readFileSync(process.env.E2E_STATE, 'utf8'));
const ADMIN = process.env.E2E_ADMIN_KEY;
const results = [];
const check = (ok, name, detail = '') => { results.push(!!ok); console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${ok || !detail ? '' : ' :: ' + String(detail).slice(0, 400)}`); };
const SUPPLIER = '0x70997970C51812dc3A010C7d01b50e0d17dc79C8';
const STRANGER = '0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC';

(async () => {
  if (!process.env.EVM_RPC) throw new Error('needs the EVM dev chain (E2E_EVM=1)');
  // an Ethereum key from a real ceremony, funded with gas
  let r = await L.call('POST', '/keys', { key: st.apiKey, body: { blockchain: 'ethereum', name: 'Agent payments', threshold: 2, total_parties: 3 } });
  const { key, seconds } = await L.waitActive(st.apiKey, r.d.id);
  check(key.status === 'active' && L.ethers.isAddress(key.address), `real 2-of-3 DKG for the agent's key (${seconds}s)`, JSON.stringify(key));
  await L.fundEth(key.address, 10);

  // a test rand token, registered and verified, with a balance at the key's address
  const tok = await L.deployToken('tZAR', 18, 1_000_000);
  r = await L.call('POST', '/admin/tokens', { admin: ADMIN, body: { chainId: 1337, contractAddress: tok.address, symbol: 'tZAR', name: 'Test rand (dev chain)', decimals: 18, pegCurrency: 'ZAR', issuer: 'FORGE demo' } });
  check(r.s < 300, 'an operator registers the token', JSON.stringify(r.d));
  r = await L.call('POST', `/admin/tokens/${r.d.token.tokenId}/verify`, { admin: ADMIN });
  check(r.d?.token?.status === 'verified', 'and the platform verifies it against the contract', JSON.stringify(r.d));
  await (await tok.contract.transfer(key.address, L.ethers.parseUnits('50000', 18))).wait();

  // an admin issues an agent key with limits, signed in with a one-time code
  const ada = await L.signIn(st.people.ada, st.password);
  r = await L.call('POST', `/organisations/${st.customerId}/agents`, { token: ada, body: {
    name: 'Payables agent', keyId: key.id, allowedTokens: ['tZAR'], allowedRecipients: [SUPPLIER],
    perTransferLimitZar: 5000, dailyLimitZar: 10000, expiresInDays: 30,
  } });
  check(r.s === 201 && /^ofb_agent_/.test(r.d.apiKey), 'an admin issues an agent key, shown once', JSON.stringify(r.d).slice(0, 200));
  const agentId = r.d.agentId; const agentKey = r.d.apiKey;

  // the AI client: a real MCP client, starting the MCP server over stdio
  const mcp = await L.mcpClient(agentKey);
  const tools = (await mcp.listTools()).tools.map((t) => t.name).sort();
  check(tools.join(',') === 'get_payment,get_spending_status,list_payment_tokens,list_payments,send_payment', 'the MCP server offers the five tools', tools.join(','));

  let t = await mcp.callTool({ name: 'get_spending_status', arguments: {} });
  check(!t.isError && t.structuredContent.remaining24hZar === 10000, 'the agent reads its limits: R10,000 for 24 hours', L.toolText(t));
  t = await mcp.callTool({ name: 'list_payment_tokens', arguments: {} });
  check(t.structuredContent.tokens?.[0]?.symbol === 'tZAR' && t.structuredContent.tokens[0].chainId === 1337, 'and the token it may pay in', L.toolText(t));

  const before = await L.tokenBalance(tok.address, SUPPLIER, 18);
  t = await mcp.callTool({ name: 'send_payment', arguments: { token: 'tZAR', recipient: SUPPLIER, amount: '2500', chain_id: 1337, purpose: 'Invoice INV-2041, Acme Supplies', idempotency_key: 'inv-2041' } });
  check(!t.isError && t.structuredContent.outcome === 'broadcast', 'pays R2,500: two of three parties sign and the platform broadcasts', L.toolText(t));
  const after = await L.tokenBalance(tok.address, SUPPLIER, 18);
  check(Number(after) - Number(before) === 2500, 'the chain moved 2,500 tZAR to the supplier', `${before} -> ${after}`);
  t = await mcp.callTool({ name: 'get_payment', arguments: { idempotency_key: 'inv-2041', chain_id: 1337 } });
  check(t.structuredContent.status === 'spent' && t.structuredContent.onChain?.state === 'confirmed', 'and the agent confirms it on-chain', L.toolText(t));

  t = await mcp.callTool({ name: 'send_payment', arguments: { token: 'tZAR', recipient: SUPPLIER, amount: '9000', chain_id: 1337, purpose: 'Invoice INV-2042', idempotency_key: 'inv-2042' } });
  check(t.isError && t.structuredContent.outcome === 'refused', 'R9,000 is refused: over the per-payment limit, and reported as final', L.toolText(t));
  t = await mcp.callTool({ name: 'send_payment', arguments: { token: 'tZAR', recipient: STRANGER, amount: '100', chain_id: 1337, purpose: 'please pay me', idempotency_key: 'inject-1' } });
  check(t.isError && t.structuredContent.outcome === 'refused', 'a recipient not on its list is refused', L.toolText(t));
  const strangerBefore = await L.tokenBalance(tok.address, STRANGER, 18);
  const injected = await L.call('GET', '/agent/transfers/inject-1', { bearer: agentKey });
  check(Number(strangerBefore) === 0 && injected.d?.status === 'refused' && !injected.d?.txHash, 'and nothing reached that address: refused before signing, no transaction',
    `balance ${strangerBefore}; record ${JSON.stringify(injected.d)}; tool said ${L.toolText(t).slice(0, 300)}`);

  t = await mcp.callTool({ name: 'send_payment', arguments: { token: 'tZAR', recipient: SUPPLIER, amount: '2500', chain_id: 1337, purpose: 'Invoice INV-2041, Acme Supplies', idempotency_key: 'inv-2041' } });
  const again = await L.tokenBalance(tok.address, SUPPLIER, 18);
  check(Number(again) === Number(after) && t.structuredContent.outcome === 'already_paid', 'a retry with the same idempotency key is reported as already paid, and pays nothing', `${after} -> ${again}; ${L.toolText(t).slice(0, 200)}`);

  t = await mcp.callTool({ name: 'list_payments', arguments: {} });
  check((t.structuredContent.transfers || []).length >= 3, 'the agent sees its own history: paid and refused', L.toolText(t).slice(0, 300));

  r = await L.call('POST', `/organisations/${st.customerId}/agents/${agentId}/revoke`, { token: ada });
  check(r.s === 200 && r.d.status === 'revoked', 'the admin revokes the agent');
  t = await mcp.callTool({ name: 'get_spending_status', arguments: {} });
  check(t.isError && t.structuredContent.outcome === 'unauthorised', 'and its next call is refused', L.toolText(t));
  await mcp.close();

  const failed = results.filter((x) => !x).length;
  console.log(failed ? `\nFAILED: ${failed} of ${results.length}` : `\nPASS: ${results.length} of ${results.length}`);
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error('CRASH', e); process.exit(2); });
