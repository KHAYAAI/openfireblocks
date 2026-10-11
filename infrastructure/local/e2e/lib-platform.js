// Shared by the MCP scenario (mcp-agent.js) and the recordings (record-v2.js): talking to
// the gateway as an app, a person and an operator would, and setting up an EVM dev chain
// with a test token. Everything here goes through the platform's public routes; the only
// direct chain actions are the ones a counterparty would take (funding an address,
// deploying a token, sending tokens to a deposit address).
const fs = require('fs');
const path = require('path');
const { authenticator } = require(process.env.OTPLIB_PATH || 'otplib');
const { ethers } = require(process.env.ETHERS_PATH || 'ethers');

const ROOT = path.resolve(__dirname, '../../..');
const G = process.env.GATEWAY || 'http://127.0.0.1:13999';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function call(method, p, { body, key, token, admin, bearer } = {}) {
  const headers = { 'content-type': 'application/json' };
  if (key) headers['x-api-key'] = key;
  if (token) headers.authorization = `Bearer ${token}`;
  if (bearer) headers.authorization = `Bearer ${bearer}`;
  if (admin) headers['x-admin-key'] = admin;
  const r = await fetch(G + p, { method, headers, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text(); let d; try { d = JSON.parse(t); } catch { d = t; }
  return { s: r.status, d };
}

// One-time codes cannot be reused, including codes another process used: wait for a fresh
// 30-second window when the current one may already have been spent.
const lastCode = {};
async function totp(secret, fresh = false) {
  if (fresh) { const left = 30 - (Math.floor(Date.now() / 1000) % 30); await sleep((left + 1) * 1000); }
  let c = authenticator.generate(secret);
  while (lastCode[secret] === c) { await sleep(1000); c = authenticator.generate(secret); }
  lastCode[secret] = c; return c;
}

async function signIn(person, password) {
  for (let attempt = 0; attempt < 4; attempt++) {
    let r = await call('POST', '/v1/auth/login', { body: { email: person.email, password } });
    if (r.s === 429) { await sleep(20000); continue; }
    r = await call('POST', '/v1/auth/mfa/verify', { body: { email: person.email, challengeToken: r.d.challengeToken, code: await totp(person.secret, attempt > 0) } });
    if (r.s < 300 && r.d.accessToken) return r.d.accessToken;
    await sleep(2000);
  }
  throw new Error(`could not sign ${person.email} in`);
}

async function waitActive(apiKey, keyId, ms = 300000) {
  const t0 = Date.now(); let k;
  while (Date.now() - t0 < ms) {
    k = (await call('GET', '/keys/' + keyId, { key: apiKey })).d;
    if (!['pending', 'pending_dkg'].includes(k.status)) return { key: { ...k, id: k.id || k.key_id || keyId }, seconds: Math.round((Date.now() - t0) / 1000) };
    await sleep(2000);
  }
  throw new Error('key did not activate: ' + JSON.stringify(k));
}

function provider() {
  return new ethers.JsonRpcProvider(process.env.EVM_RPC, 1337, { staticNetwork: ethers.Network.from(1337), cacheTimeout: -1 });
}

// A test token on the dev chain (DrillToken: symbol and decimals from the constructor).
async function deployToken(symbol, decimals, supplyWhole) {
  const funder = new ethers.Wallet(process.env.EVM_FUNDER_KEY, provider());
  const bin = fs.readFileSync(path.join(ROOT, 'infrastructure/kind/erc20/DrillToken.bin'), 'utf8').trim();
  const abi = ['constructor(string _symbol, uint8 _decimals, uint256 _supply)', 'function transfer(address to, uint256 value) returns (bool)', 'function balanceOf(address) view returns (uint256)'];
  const f = new ethers.ContractFactory(abi, bin.startsWith('0x') ? bin : '0x' + bin, funder);
  const c = await f.deploy(symbol, decimals, ethers.parseUnits(String(supplyWhole), decimals));
  await c.waitForDeployment();
  return { address: await c.getAddress(), contract: c };
}

async function fundEth(to, eth) {
  const funder = new ethers.Wallet(process.env.EVM_FUNDER_KEY, provider());
  await (await funder.sendTransaction({ to, value: ethers.parseEther(String(eth)) })).wait();
}

async function tokenBalance(tokenAddress, holder, decimals) {
  const c = new ethers.Contract(tokenAddress, ['function balanceOf(address) view returns (uint256)'], provider());
  return ethers.formatUnits(await c.balanceOf(holder), decimals);
}

// The MCP SDK is ESM; the scenarios are CommonJS.
async function mcpClient(agentKey, extraEnv = {}) {
  const sdk = path.join(ROOT, 'services/mcp-server/node_modules/@modelcontextprotocol/sdk/dist/esm');
  const { Client } = await import(path.join(sdk, 'client/index.js'));
  const { StdioClientTransport } = await import(path.join(sdk, 'client/stdio.js'));
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: [path.join(ROOT, 'services/mcp-server/dist/src/index.js')],
    env: { PATH: process.env.PATH, FORGE_API_URL: G, FORGE_AGENT_KEY: agentKey, ...extraEnv },
  });
  const client = new Client({ name: 'forge-demo-agent', version: '1.0.0' });
  await client.connect(transport);
  return client;
}

const toolText = (r) => (r.content || []).map((c) => c.text).join('\n');

module.exports = { G, ROOT, sleep, call, totp, signIn, waitActive, provider, deployToken, fundEth, tokenBalance, mcpClient, toolText, ethers };
