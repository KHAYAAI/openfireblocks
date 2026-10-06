// The platform, end to end, with real processes and no stubs between them:
//
//   API key -> gateway -> Temporal -> worker -> 3 real mpc-party processes
//   (real tss-lib DKG, real threshold signing) -> mpc-signer -> a Solana
//   JSON-RPC stand-in that verifies the Ed25519 signature against the key.
//
// Plus the controls around it: a transfer over the approval threshold is held,
// two named people with one-time codes release it, a freeze stops everything.
//
// What is a stand-in, said plainly: the chain (mock-solana-node.js verifies
// signatures; it is not Solana), and there is no mTLS between the parties.
// Run via e2e-fullstack-local.sh, which starts everything.
const { execSync } = require('child_process');
const crypto = require('crypto');
const { authenticator } = require(process.env.OTPLIB_PATH || 'otplib');

const G = process.env.GATEWAY || 'http://127.0.0.1:3999';
const NODE = process.env.SOLANA_MOCK || 'http://127.0.0.1:18899';
const PSQL = process.env.PSQL || `PGPASSWORD=dev-only psql -h 127.0.0.1 -p 55432 -U app_admin -d openfireblocks -Atc`;
const PW = 'Correct-Horse-9-battery';
const DEST = '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM';
const TRAVEL = { originator: { legalPerson: { name: 'E2E Treasury Co', geographicAddress: { addressLine: ['1 Main St, Cape Town'], country: 'ZA' } } }, beneficiary: { legalPerson: { name: 'External Counterparty Ltd' } }, beneficiaryUnhosted: true };

const results = [];
const check = (ok, name, detail = '') => { results.push(ok); console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${ok || !detail ? '' : ' :: ' + detail}`); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const psql = (q) => execSync(`${PSQL} "${q.replace(/"/g, '\\"')}"`).toString().trim().split('\n')[0];

async function call(method, path, { body, key, token } = {}) {
  const headers = { 'content-type': 'application/json' };
  if (key) headers['x-api-key'] = key;
  if (token) headers.authorization = `Bearer ${token}`;
  const r = await fetch(G + path, { method, headers, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text(); let d; try { d = JSON.parse(t); } catch { d = t; }
  return { s: r.status, d };
}

const lastCode = {};
async function totp(secret) { let c = authenticator.generate(secret); while (lastCode[secret] === c) { await sleep(1000); c = authenticator.generate(secret); } lastCode[secret] = c; return c; }

async function person(name, org) {
  const email = `${name}-${crypto.randomBytes(3).toString('hex')}@e2e.example`;
  let r = await call('POST', '/v1/auth/register', { body: { email, password: PW, fullName: name } });
  if (r.s >= 300) throw new Error('register ' + JSON.stringify(r));
  r = await call('POST', '/v1/auth/login', { body: { email, password: PW } });
  const tok = r.d.accessToken; const secret = (await call('POST', '/v1/auth/mfa/enroll', { token: tok })).d.secret;
  await call('POST', '/v1/auth/mfa/enroll/confirm', { token: tok, body: { code: await totp(secret) } });
  return { name, email, secret, token: tok };
}
async function relogin(p) {
  let r = await call('POST', '/v1/auth/login', { body: { email: p.email, password: PW } });
  r = await call('POST', '/v1/auth/mfa/verify', { body: { email: p.email, challengeToken: r.d.challengeToken, code: await totp(p.secret) } });
  p.token = r.d.accessToken; return p;
}

(async () => {
  // --- an organisation, its people, and a key
  const apiKey = 'ofb_' + crypto.randomBytes(24).toString('hex');
  const hash = crypto.createHash('sha256').update(apiKey).digest('hex');
  const cust = psql(`INSERT INTO customers (name,email,api_key_hash,status,tier) VALUES ('E2E Treasury Co','ops@e2e.example',decode('${hash}','hex'),'active','enterprise') RETURNING customer_id`);
  const ada = await person('ada', cust), alice = await person('alice', cust), bob = await person('bob', cust);
  check((await call('POST', '/organisation/first-admin', { key: apiKey, body: { email: ada.email, role: 'admin' } })).s === 201, 'the first admin is appointed with the API key');
  await relogin(ada);
  for (const [p, role] of [[alice, 'approver'], [bob, 'approver']]) await call('PUT', `/organisations/${cust}/members`, { token: ada.token, body: { email: p.email, role } });
  await relogin(alice); await relogin(bob);

  // --- a real key: real DKG over three real parties
  let r = await call('POST', '/keys', { key: apiKey, body: { blockchain: 'solana', name: 'e2e', threshold: 2, total_parties: 3 } });
  check(r.s === 201, 'a key is requested over the API', JSON.stringify(r.d));
  const keyId = r.d.id; let key; const t0 = Date.now();
  while (Date.now() - t0 < 240000) { key = (await call('GET', '/keys/' + keyId, { key: apiKey })).d; if (!['pending', 'pending_dkg'].includes(key.status)) break; await sleep(2000); }
  check(key.status === 'active' && /^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(key.address), `a real 2-of-3 DKG ran and produced a Solana address (${Math.round((Date.now() - t0) / 1000)}s)`, JSON.stringify(key));

  // --- a routine transfer: signed by the parties, accepted by the node
  r = await call('POST', `/keys/${keyId}/transfers`, { key: apiKey, body: { destination: DEST, amount: '1000000', travelRule: TRAVEL } });
  check(r.s === 200 && r.d.status === 'completed', 'a routine transfer is signed and sent', JSON.stringify(r.d).slice(0, 300));
  let sent = await (await fetch(NODE + '/__sent')).json();
  check(sent.length === 1 && sent[0].payer === key.address, 'the node verified the threshold signature against the key\'s own address', JSON.stringify(sent));

  // --- a transfer over the threshold is held, and two people release it
  r = await call('POST', `/keys/${keyId}/transfers`, { key: apiKey, body: { destination: DEST, amount: '15000000000', travelRule: TRAVEL } });
  check(r.s === 202 && r.d.status === 'pending_approval', 'a high-value transfer is held, not signed', JSON.stringify(r.d).slice(0, 200));
  const approvalId = r.d.approvalId;
  check((await (await fetch(NODE + '/__sent')).json()).length === 1, 'nothing reached the chain while it was held');
  let d1 = await call('POST', `/organisations/${cust}/approvals/${approvalId}/decisions`, { token: ada.token, body: { decision: 'approve', totpCode: await totp(ada.secret) } });
  check(d1.s === 200 && d1.d.status === 'pending', 'one approval is not enough', JSON.stringify(d1.d).slice(0, 160));
  let d2 = await call('POST', `/organisations/${cust}/approvals/${approvalId}/decisions`, { token: alice.token, body: { decision: 'approve', totpCode: await totp(alice.secret) } });
  check(d2.s === 200 && d2.d.status === 'approved' && d2.d.execution?.status === 'completed', 'the second approval releases it, and it runs', JSON.stringify(d2.d).slice(0, 300));
  sent = await (await fetch(NODE + '/__sent')).json();
  check(sent.length === 2 && sent[1].payer === key.address, 'the released transfer was signed by the parties and accepted by the node');
  const again = await call('POST', `/organisations/${cust}/approvals/${approvalId}/decisions`, { token: bob.token, body: { decision: 'approve', totpCode: await totp(bob.secret) } });
  check(again.s === 409 && (await (await fetch(NODE + '/__sent')).json()).length === 2, 'a late third decision is refused and nothing is sent twice');

  // --- the freeze
  r = await call('POST', `/organisations/${cust}/controls/freeze`, { token: bob.token, body: { reason: 'e2e: suspected compromise' } });
  check(r.s === 200 && r.d.frozen, 'an approver freezes the organisation');
  r = await call('POST', `/keys/${keyId}/transfers`, { key: apiKey, body: { destination: DEST, amount: '1000000', travelRule: TRAVEL } });
  check(r.s === 403 && /frozen/.test(JSON.stringify(r.d)), 'while frozen, the same routine transfer is refused', JSON.stringify(r.d).slice(0, 160));
  check((await call('POST', `/organisations/${cust}/controls/unfreeze`, { token: bob.token })).s === 403, 'an approver cannot lift the freeze');
  r = await call('POST', `/organisations/${cust}/controls/unfreeze`, { token: ada.token });
  check(r.s === 200 && !r.d.frozen, 'an admin lifts it');
  r = await call('POST', `/keys/${keyId}/transfers`, { key: apiKey, body: { destination: DEST, amount: '1000000', travelRule: TRAVEL } });
  check(r.s === 200, 'and signing works again', JSON.stringify(r.d).slice(0, 160));
  check((await (await fetch(NODE + '/__sent')).json()).length === 3, 'three transfers in all reached the node');

  const failed = results.filter((x) => !x).length;
  console.log(failed ? `\nFAILED: ${failed} of ${results.length}` : `\nPASS: ${results.length} of ${results.length}`);
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error('CRASH', e); process.exit(2); });
