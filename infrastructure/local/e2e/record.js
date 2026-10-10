// Records the REAL console for three customer stories, against the full local
// stack (run it through e2e-fullstack-local.sh):
//
//   E2E_AFTER="node infrastructure/local/e2e/record.js" ./infrastructure/local/e2e-fullstack-local.sh
//
// Nothing on screen is scripted animation: every page is the running product,
// backed by a real DKG and real threshold signatures. The only stand-ins are the
// chain (mock-solana-node.js checks the signature but is not Solana), Stripe
// (mock-billing.js) and single sign-on (not shown). The overlays only add
// titles and captions.
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const { authenticator } = require(process.env.OTPLIB_PATH || 'otplib');
const fs = require('fs');
const st = JSON.parse(fs.readFileSync(process.env.E2E_STATE, 'utf8'));
const seed = { PW: st.password, cust: st.customerId, key: st.apiKey, sol: st.keyId, secrets: Object.fromEntries(Object.entries(st.people).map(([n, p]) => [n, p.secret])) };
const G = st.gateway; const B = G + '/console';
const OUT = process.env.OUT_DIR || (__dirname + '/../../../docs/showcase'); fs.mkdirSync(OUT, { recursive: true });
const DEST = '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM';
const sleep = (ms) => new Promise(r => setTimeout(r, ms));
const lastCode = {};
async function code(name) { // never reuse a one-time code: wait for the next 30s window
  let c = authenticator.generate(seed.secrets[name]);
  while (lastCode[name] === c) { await sleep(1000); c = authenticator.generate(seed.secrets[name]); }
  lastCode[name] = c; return c;
}

const OVERLAY_JS = () => {
  if (document.getElementById('rec-style')) return;
  const st = document.createElement('style'); st.id = 'rec-style';
  st.textContent = `
  html{height:720px!important}body{height:720px!important;overflow:hidden}.shell,.auth-shell{min-height:720px!important;height:720px!important}.sidebar{height:720px!important}.main{height:720px;overflow-y:auto}#rec-cap{position:fixed;left:0;right:0;top:720px;height:80px;z-index:99998;background:#0A0A0A;color:#F4F2EE;padding:0 28px;display:flex;flex-direction:column;justify-content:center;font:400 15px/1.45 'Inter Tight',sans-serif}
  #rec-cap b{display:block;font:500 11px 'JetBrains Mono',monospace;letter-spacing:1.6px;text-transform:uppercase;color:#8B8579;margin-bottom:4px}
  #rec-card{position:fixed;left:0;right:0;top:0;bottom:0;height:800px;z-index:99999;background:#F4F2EE;color:#0A0A0A;display:flex;flex-direction:column;justify-content:center;padding:0 9vw;font-family:'Inter Tight',sans-serif}
  #rec-card .e{font:500 13px 'JetBrains Mono',monospace;letter-spacing:2px;text-transform:uppercase;color:#6F6A62}
  #rec-card h1{font:600 56px/1.05 'Inter Tight',sans-serif;letter-spacing:-2px;margin:14px 0 0;max-width:900px}
  #rec-card h1 i{font-weight:300}
  #rec-card p{font:400 20px/1.5 'Inter Tight',sans-serif;color:#6F6A62;max-width:760px;margin:20px 0 0}
  #rec-card ul{margin:22px 0 0;padding:0;list-style:none;font:400 17px/1.7 'Inter Tight',sans-serif;max-width:860px}
  #rec-card li:before{content:'— ';color:#8B8579}
  #rec-api{position:fixed;right:24px;top:70px;width:560px;z-index:99997;background:#0A0A0A;color:#D8D4CC;border-radius:2px;padding:18px 20px;font:400 12.5px/1.6 'JetBrains Mono',monospace;white-space:pre-wrap;word-break:break-all}
  #rec-api b{display:block;color:#8B8579;font-weight:500;letter-spacing:1.6px;text-transform:uppercase;font-size:11px;margin-bottom:8px}
  #rec-cur{position:fixed;width:16px;height:16px;border:2px solid #0A0A0A;background:rgba(244,242,238,.85);border-radius:50%;z-index:100000;pointer-events:none;transform:translate(-8px,-8px);left:-50px;top:-50px}`;
  document.head.appendChild(st);
  const strip = document.createElement('div'); strip.id = 'rec-cap'; document.body.appendChild(strip);
  const cur = document.createElement('div'); cur.id = 'rec-cur'; document.body.appendChild(cur);
  addEventListener('mousemove', e => { cur.style.left = e.clientX + 'px'; cur.style.top = e.clientY + 'px'; }, true);
};
async function prep(p) { await p.evaluate(OVERLAY_JS).catch(() => {}); }
async function caption(p, eyebrow, text) {
  await prep(p);
  await p.evaluate(([e, t]) => { let c = document.getElementById('rec-cap'); if (!c) { c = document.createElement('div'); c.id = 'rec-cap'; document.body.appendChild(c); }
    c.replaceChildren(); const b = document.createElement('b'); b.textContent = e; c.appendChild(b); c.appendChild(document.createTextNode(t)); }, [eyebrow, text]);
  if (process.env.SHOTS) { fs.mkdirSync(OUT + '/shots', { recursive: true }); await sleep(700); await p.screenshot({ path: `${OUT}/shots/${CUR}-${String(++SHOTN).padStart(2, '0')}.png` }); }
}
let SHOTN = 0, CUR = '';
async function card(p, eyebrow, titleParts, para, bullets, ms) {
  await prep(p);
  await p.evaluate(([e, tp, pa, bu]) => { document.getElementById('rec-card')?.remove(); const d = document.createElement('div'); d.id = 'rec-card';
    const E = document.createElement('div'); E.className = 'e'; E.textContent = e; d.appendChild(E);
    const h = document.createElement('h1'); tp.forEach(x => { if (x.i) { const i = document.createElement('i'); i.textContent = x.i; h.appendChild(i); } else h.appendChild(document.createTextNode(x)); }); d.appendChild(h);
    if (pa) { const q = document.createElement('p'); q.textContent = pa; d.appendChild(q); }
    if (bu && bu.length) { const u = document.createElement('ul'); bu.forEach(t => { const l = document.createElement('li'); l.textContent = t; u.appendChild(l); }); d.appendChild(u); }
    document.body.appendChild(d); }, [eyebrow, titleParts, para, bullets || []]);
  await sleep(ms);
}
async function dropCard(p) { await p.evaluate(() => document.getElementById('rec-card')?.remove()).catch(() => {}); }
async function move(p, sel) { const el = await p.waitForSelector(sel, { timeout: 30000 }); await el.scrollIntoViewIfNeeded(); const bb = await el.boundingBox(); await p.mouse.move(bb.x + bb.width / 2, bb.y + bb.height / 2, { steps: 18 }); return el; }
async function click(p, sel) { await move(p, sel); await sleep(350); await p.click(sel); await sleep(500); }
async function type(p, sel, text, d = 45) { await move(p, sel); await p.click(sel); await p.type(sel, text, { delay: d }); await sleep(300); }

async function login(p, name, note) {
  await p.goto(B); await p.waitForSelector('#email'); await prep(p);
  if (note) await caption(p, 'Sign in', note);
  await sleep(900);
  await type(p, '#email', st.people[name].email); await type(p, '#password', seed.PW, 30);
  await click(p, 'button[type=submit]'); await p.waitForSelector('#code');
  await caption(p, 'Step-up', 'Second factor: a one-time code from the person\'s authenticator app.');
  await type(p, '#code', await code(name), 90); await click(p, 'button[type=submit]'); await p.waitForSelector('.sidebar'); await prep(p); await sleep(900);
}
async function logout(p) { await click(p, '.signout'); await p.waitForSelector('#email'); await sleep(500); }
async function nav(p, id, cap) { await click(p, `[data-nav=${id}]`); await sleep(1200); await prep(p); if (cap) await caption(p, cap[0], cap[1]); await sleep(1800); }

async function startTransfer(p, amount, withTravel = true) {
  await nav(p, 'keys');
  await click(p, `tr.link:has-text("${st.keyName}")`); await p.waitForSelector('text=New transfer'); await sleep(800);
  await click(p, 'text=New transfer'); await p.waitForSelector('#s-dest');
  await type(p, '#s-dest', DEST, 25); await type(p, '#s-amt', amount, 90);
  if (withTravel) {
    await caption(p, 'Travel Rule', 'Above the reporting threshold the sender and recipient details are required before anything is signed.');
    await click(p, 'summary'); await type(p, '#tr-oaddr', '1 Main St, Cape Town', 30); await type(p, '#tr-ocountry', 'za', 80); await type(p, '#tr-bname', 'External Counterparty Ltd', 30);
  }
  await click(p, 'button:has-text("Review")'); await p.waitForSelector('text=Review this transfer'); await prep(p);
  await caption(p, 'Review', 'The last look before money moves. Policy is checked when this is confirmed.'); await sleep(2800);
  await click(p, 'text=Confirm and send'); await p.waitForSelector('text=Nothing has been signed', { timeout: 30000 }); await prep(p);
  await caption(p, 'Held', 'Policy says this needs approval, so nothing has been signed. It waits for other people.'); await sleep(3200);
}
async function approve(p, who, label) {
  await caption(p, 'Approval', label);
  await p.waitForSelector('#totp'); await sleep(1500);
  await type(p, '#totp', await code(who), 90); await click(p, 'button.approve'); await p.waitForSelector('.notice.ok', { timeout: 30000 }); await sleep(2200);
}
async function openFirstPending(p) {
  await nav(p, 'approvals'); await click(p, '.card.link'); await p.waitForSelector('text=Why this needs approval'); await prep(p); await sleep(1200);
}

const NOTE = 'Signed by the three parties and accepted by the chain stand-in, which checked the signature against the key.';

async function reconcile(p) {
  await nav(p, 'reconciliation', ['Reconciliation', 'The ledger is checked against the chain, and anything signed elsewhere is flagged.']);
  await click(p, 'text=Run a reconciliation'); await p.waitForSelector('#rc-kind'); await sleep(600);
  await p.selectOption('#rc-kind', 'solana'); await sleep(600);
  await click(p, 'button:has-text("Run chain check")'); await p.waitForSelector('.grid-stats, dl.facts', { timeout: 30000 }); await sleep(3000);
}
async function resultPanel(p, cap) { await p.waitForSelector('h2:has-text("Result")', { timeout: 30000 }); await prep(p); await caption(p, 'Result', cap); await p.evaluate(() => document.querySelector('h2')?.scrollIntoView?.()); await sleep(1500); const el = await p.$('h2:has-text("Result")'); if (el) await el.scrollIntoViewIfNeeded(); await sleep(4200); }

const FLOWS = {
  async fintech(p) {
    await p.goto(B); await p.waitForSelector('#email');
    await card(p, 'Fintech · recorded on the running platform', ['A payments app ', { i: 'moves value from its own custody.' }], 'Northwind Pay (fictional) pays out through the API; its finance team keeps the controls in the console.', ['The app calls the API with a key', 'Policy decides what is released and what is held', 'Two named people approve a large payment', 'Then the ledger is checked against the chain'], 7500); await dropCard(p);
    await login(p, 'ada', 'The finance team signs in to the console. The app itself uses an API key.');
    await nav(p, 'keys', ['Keys', 'Each key is held by three signing parties; any two sign. This one is a Solana key, 2-of-3.']);
    // the real API call, made now, shown beside the console
    const body = { destination: DEST, amount: '15000000000', idempotencyKey: 'north-' + Date.now(), travelRule: { originator: { legalPerson: { name: 'Forge Treasury Co', geographicAddress: { addressLine: ['1 Main St, Cape Town'], country: 'ZA' } } }, beneficiary: { legalPerson: { name: 'External Counterparty Ltd' } }, beneficiaryUnhosted: true } };
    await caption(p, 'The app calls the API', 'A real request, sent to this gateway just now. The key behind it came from a real three-party ceremony.');
    const r = await fetch(`${G}/keys/${seed.sol}/transfers`, { method: 'POST', headers: { 'content-type': 'application/json', 'x-api-key': seed.key }, body: JSON.stringify(body) });
    const resp = JSON.parse(await r.text());
    await p.evaluate(([req, status, res]) => { const d = document.createElement('div'); d.id = 'rec-api'; const b = document.createElement('b'); b.textContent = 'POST /keys/:id/transfers  ·  x-api-key'; d.appendChild(b); d.appendChild(document.createTextNode(req + '\n\n→ HTTP ' + status + '\n' + res)); document.body.appendChild(d); },
      [JSON.stringify({ destination: DEST, amount: '15000000000', idempotencyKey: body.idempotencyKey, travelRule: '…' }, null, 1), r.status, JSON.stringify({ status: resp.status, approvalId: (resp.approvalId || '').slice(0, 8) + '…', requiredApprovals: resp.requiredApprovals, reasons: resp.reasons }, null, 1)]);
    await caption(p, 'Held, not signed', 'Over the threshold the API answers 202. Nothing is signed; the request is stored exactly as sent.'); await sleep(6500);
    await p.evaluate(() => document.getElementById('rec-api')?.remove());
    await openFirstPending(p);
    await caption(p, 'The finance team', 'The request arrives from the API key. Two people other than the requester must decide.'); await sleep(2500);
    await approve(p, 'ada', 'First approver: Ada, verified with a fresh one-time code.');
    await logout(p); await login(p, 'alice', null); await openFirstPending(p);
    await approve(p, 'alice', 'Second approver: Alice. The quorum is met.');
    await resultPanel(p, 'After the last approval the stored request runs once. ' + NOTE);
    await logout(p); await login(p, 'ada', null);
    await nav(p, 'billing', ['Billing', 'The platform bills its own customers. The card is saved on Stripe\'s hosted page; the console never sees the number.']);
    await click(p, 'button:has-text("Add card")'); await p.waitForURL(/checkout\.stripe\.com/, { timeout: 15000 }); await sleep(2500);
    await p.goto(B + '#/billing?card=saved'); await p.waitForSelector('text=visa'); await prep(p); await caption(p, 'Billing', 'Back from Stripe: the console shows brand and last four only. (Stripe is a stand-in here.)'); await sleep(3500);
    await reconcile(p);
    await card(p, 'What this recording is', ['Real platform, ', { i: 'stand-in chain.' }], null, ['Real: the console, sign-in and one-time codes, policy, approvals, a real 2-of-3 key ceremony and real threshold signatures', 'Stand-ins: the chain (a Solana RPC stand-in that verifies the signature; it is not Solana), Stripe', 'Not shown: a transaction confirming on a live network', 'A fintech can start as a pilot on testnet or capped funds'], 10000);
  },

  async bank(p) {
    await p.goto(B); await p.waitForSelector('#email');
    await card(p, 'Bank · recorded on the running platform', ['A digital-asset desk, ', { i: 'under bank controls.' }], 'Meridian Bank (fictional). Named people, dual control and a record the audit committee can test.', ['Roles and who may do what', 'An operator asks; others decide', 'Travel Rule details before signing', 'Reconciliation against the chain'], 7500); await dropCard(p);
    await login(p, 'ada', 'Single sign-on through the bank\'s identity provider is built; this recording uses password and one-time code, as it has no provider to connect to.');
    await nav(p, 'people', ['People', 'Every person has one role: admin, approver, operator, auditor, viewer or billing admin.']);
    await nav(p, 'policy', ['Approval policy', 'Dual control by default. The requester can never approve; each person decides once.']);
    await logout(p); await login(p, 'oscar', 'Oscar is an operator: he can ask for a transfer but not approve one.');
    await caption(p, 'Operator', 'An operator cannot create keys or change policy.'); await nav(p, 'keys');
    await startTransfer(p, '15');
    await click(p, 'text=Open the approval'); await p.waitForSelector('text=Why this needs approval'); await prep(p);
    await caption(p, 'Segregation of duties', 'Oscar can see his request but is told he cannot approve or reject it.'); await sleep(4000);
    await logout(p); await login(p, 'alice', null); await openFirstPending(p);
    await approve(p, 'alice', 'Approver one: Alice, verified with a one-time code.');
    await logout(p); await login(p, 'bob', null); await openFirstPending(p);
    await approve(p, 'bob', 'Approver two: Bob. The quorum is met and the stored request runs once.');
    await resultPanel(p, 'Both decisions are recorded with who, when and how each was verified. ' + NOTE);
    await logout(p); await login(p, 'ada', 'The administrator reviews the evidence afterwards.');
    await nav(p, 'travel-rule', ['Travel Rule', 'The originator and beneficiary details are held with the transfer.']);
    await reconcile(p);
    await card(p, 'What this recording is', ['Real platform, ', { i: 'stand-in chain.' }], null, ['Real: roles, one-time codes, policy, approvals, Travel Rule capture, a real key ceremony and real threshold signatures', 'Stand-ins: single sign-on (built, not shown here) and the chain (a signature-checking stand-in, not Solana)', 'Offered to banks as a pilot with milestones while SOC 2 Type II, an independent audit and a penetration test are pending'], 10000);
  },

  async government(p) {
    await p.goto(B); await p.waitForSelector('#email');
    await card(p, 'Government · recorded on the running platform', ['A treasury that ', { i: 'keeps its own keys.' }], 'A national treasury (fictional). Three named officials must agree before a disbursement runs.', ['The quorum is the customer\'s to set', 'The request cannot be changed once parked', 'The record cannot be edited or deleted', 'Self-hosting is a deployment choice; it is not shown in a console'], 7500); await dropCard(p);
    await login(p, 'ada', 'The treasury administrator sets the controls.');
    await nav(p, 'policy', ['Approval policy', 'The administrator raises the quorum to three approvals.']);
    await type(p, '#req', '', 10); await p.fill('#req', ''); await type(p, '#req', '3', 120); await click(p, 'button:has-text("Save policy")'); await p.waitForSelector('text=Saved', { timeout: 15000 }); await sleep(2800);
    await logout(p); await login(p, 'oscar', 'A finance clerk asks for a disbursement.');
    await startTransfer(p, '15');
    await logout(p);
    for (const [who, label] of [['alice', 'First official: Alice, with a fresh one-time code.'], ['bob', 'Second official: Bob.']]) {
      await login(p, who, null); await openFirstPending(p); await approve(p, who, label); await logout(p);
    }
    await login(p, 'ada', null); await openFirstPending(p);
    await caption(p, 'Third official', 'Two of three are in. The request still waits.'); await sleep(2500);
    await approve(p, 'ada', 'Third official: the administrator. Three of three; the stored request now runs once.');
    await resultPanel(p, 'Three decisions, each with who, when and how verified. ' + NOTE);
    await nav(p, 'policy', null);
    await reconcile(p);
    // put the organisation's policy back before the closing card covers the screen
    await logout(p); await login(p, 'ada', null); await nav(p, 'policy', null);
    await p.fill('#req', '2'); await click(p, 'button:has-text("Save policy")'); await p.waitForSelector('text=Saved', { timeout: 15000 });
    await card(p, 'What this recording is', ['Real platform, ', { i: 'stand-in chain.' }], null, ['Real: the quorum, one-time codes, approvals, the audit record, a real key ceremony and real threshold signatures', 'Stand-ins: the chain (a signature-checking stand-in, not Solana)', 'Not shown: separate-owner hosting, a hardware HSM, a live network', 'Offered to governments as a bounded proof of concept while the audit, penetration test and SOC 2 Type II are pending'], 10000);
    // put the organisation's policy back
  },
};

(async () => {
  await sleep(32000); // the previous scenario used these people's codes; a code cannot be used twice
  const which = process.argv.slice(2).length ? process.argv.slice(2) : ['fintech', 'bank', 'government'];
  const b = await chromium.launch();
  for (const name of which) {
    CUR = name; SHOTN = 0;
    const ctx = await b.newContext({ viewport: { width: 1280, height: 800 }, recordVideo: { dir: OUT + '/' + name, size: { width: 1280, height: 800 } } });
    const p = await ctx.newPage(); const errs = [];
    p.on('pageerror', e => errs.push(e.message));
    await p.route('https://checkout.stripe.com/**', r => r.fulfill({ status: 200, contentType: 'text/html', body: '<body style="font:20px Inter Tight,sans-serif;background:#F4F2EE;display:grid;place-items:center;height:100vh;margin:0"><div style="text-align:center"><div style="font:500 12px monospace;letter-spacing:2px;color:#6F6A62">STAND-IN FOR STRIPE\'S HOSTED PAGE</div><p>The card would be entered here, on Stripe, not on our console.</p></div></body>' }));
    try { await FLOWS[name](p); await sleep(800); } catch (e) { console.error(name, 'FAILED:', e.message); await p.screenshot({ path: `${OUT}/${name}-fail.png` }); }
    await ctx.close();
    const dir = OUT + '/' + name; const f = fs.readdirSync(dir).find(x => x.endsWith('.webm'));
    fs.renameSync(`${dir}/${f}`, `${OUT}/openfireblocks-${name}-console.webm`); fs.rmSync(dir, { recursive: true });
    console.log(name, 'done', fs.statSync(`${OUT}/openfireblocks-${name}-console.webm`).size, errs.length ? 'JS errors: ' + errs : '');
    if (which.length > 1) await sleep(65000); // login rate limit
  }
  await b.close();
})();
