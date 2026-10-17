// The console, driven in a real browser against the real stack: the people
// who run the platform, doing their jobs. Run by e2e-fullstack-local.sh after
// the API scenario, from the state it leaves (E2E_STATE), when Playwright's
// Chromium is available.
const fs = require('fs');
const { authenticator } = require(process.env.OTPLIB_PATH || 'otplib');
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const st = JSON.parse(fs.readFileSync(process.env.E2E_STATE, 'utf8'));
const B = st.gateway + '/console';
const NODE = process.env.SOLANA_MOCK || 'http://127.0.0.1:18899';
const DEST = '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM';
const results = [];
const check = (ok, name, detail = '') => { results.push(ok); console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${ok || !detail ? '' : ' :: ' + detail}`); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const sent = async () => (await (await fetch(NODE + '/__sent')).json());
const used = {};
async function code(who) { let c = authenticator.generate(st.people[who].secret); while (used[who] === c) { await sleep(1000); c = authenticator.generate(st.people[who].secret); } used[who] = c; return c; }
let lastLogin = 0;
async function signIn(browser, who) {
  // The login route allows 5 a minute.
  const wait = lastLogin + 13000 - Date.now(); if (wait > 0) await sleep(wait); lastLogin = Date.now();
  const page = await (await browser.newContext({ viewport: { width: 1280, height: 800 } })).newPage(); page.errs = [];
  page.on('pageerror', (e) => page.errs.push(e.message));
  await page.goto(B); await page.waitForSelector('#email');
  await page.fill('#email', st.people[who].email); await page.fill('#password', st.password); await page.click('button[type=submit]');
  await page.waitForSelector('#code'); await page.fill('#code', await code(who)); await page.click('button[type=submit]');
  await page.waitForSelector('.sidebar'); return page;
}
async function startTransfer(page, amount) {
  await page.click('[data-nav=keys]'); await page.waitForSelector('tr.link');
  await page.click(`tr.link:has-text("${st.keyName}")`); await page.waitForSelector('text=New transfer');
  await page.click('text=New transfer'); await page.waitForSelector('#s-dest');
  await page.fill('#s-dest', DEST); await page.fill('#s-amt', amount);
  await page.click('summary'); await page.fill('#tr-oaddr', '1 Main St, Cape Town'); await page.fill('#tr-ocountry', 'za'); await page.fill('#tr-bname', 'External Counterparty Ltd');
  await page.click('button:has-text("Review")'); await page.waitForSelector('text=Review this transfer');
  await page.click('text=Confirm and send');
}
async function decide(browser, who, hash) {
  const page = await signIn(browser, who);
  await page.goto(B + hash); await page.waitForSelector('#totp');
  await page.fill('#totp', await code(who)); await page.click('button.approve'); await page.waitForSelector('.notice.ok');
  return page;
}

async function seen(page, what, sel, opts) {
  try { await page.waitForSelector(sel, opts); } catch (e) {
    console.log(`  (while waiting for ${what}, the page said: ${(await page.textContent('#content').catch(() => '?')).slice(0, 300)})`);
    throw e;
  }
}

(async () => {
  const browser = await chromium.launch();
  const before = (await sent()).length;

  // An operator sends a routine transfer; the platform really signs and sends it.
  const oscar = await signIn(browser, 'oscar');
  await oscar.click('[data-nav=keys]'); await oscar.waitForSelector(`tr.link:has-text("${st.keyName}")`);
  check(!!(await oscar.$(`tr.link:has-text("${st.keyName}") >> text=active`)), 'the key made by the real ceremony shows as active');
  await startTransfer(oscar, '0.5');
  await seen(oscar, 'the transfer to be sent', 'text=on its way', { timeout: 60000 });
  let a = await sent();
  check(a.length === before + 1 && a.at(-1).payer === st.keyAddress, 'a routine transfer from the console is signed and accepted by the node');
  const txt = await oscar.textContent('#content');
  check(a.length > 0 && txt.includes(a.at(-1).signature.slice(0, 16)), 'the console shows the transaction id the node recorded');

  // A large one is held; two approvers release it from their own screens.
  await startTransfer(oscar, '15');
  await seen(oscar, 'the transfer to be held', 'text=Nothing has been signed', { timeout: 60000 });
  check((await sent()).length === before + 1, 'a high-value transfer is held and nothing reaches the chain');
  await oscar.click('text=Open the approval'); await oscar.waitForSelector('text=Why this needs approval');
  const hash = await oscar.evaluate(() => location.hash);
  check(/cannot approve or reject/.test(await oscar.textContent('#content')), 'the person who asked is told they cannot decide it');

  const alice = await decide(browser, 'alice', hash);
  check(/still needs 1 more/.test(await alice.textContent('#content')), 'the first approver is told one more is needed');
  const bob = await decide(browser, 'bob', hash);
  await bob.waitForSelector('h2:has-text("Result")', { timeout: 60000 });
  const res = await bob.textContent('#content');
  a = await sent();
  check(/Sent\./.test(res) && a.length === before + 2 && a.at(-1).payer === st.keyAddress, 'the second approval releases it: signed by the parties, accepted by the node, shown as sent');
  check(res.includes(a.at(-1).signature.slice(0, 16)), 'with the transaction id the node recorded');

  // The emergency freeze, from the screen.
  await bob.click('[data-nav=safety]'); await bob.waitForSelector('#fz-reason');
  await bob.fill('#fz-reason', 'console e2e'); await bob.click('button:has-text("Freeze everything")'); await bob.waitForSelector('text=Organisation frozen');
  await startTransfer(oscar, '0.5');
  await oscar.waitForSelector('.notice.error', { timeout: 30000 });
  check(/frozen/.test(await oscar.textContent('#content')) && (await sent()).length === before + 2, 'while frozen, the operator\'s send is refused with the reason');
  const ada = await signIn(browser, 'ada');
  await ada.click('[data-nav=safety]'); await ada.waitForSelector('button:has-text("Lift the freeze")');
  await ada.click('button:has-text("Lift the freeze")'); await ada.waitForSelector('#fz-reason');
  check(true, 'an admin lifts the freeze from the screen');

  for (const [n, p] of [['oscar', oscar], ['alice', alice], ['bob', bob], ['ada', ada]]) check(p.errs.length === 0, `no JavaScript errors for ${n}`, JSON.stringify(p.errs));
  await browser.close();
  const failed = results.filter((x) => !x).length;
  console.log(failed ? `\nFAILED: ${failed} of ${results.length}` : `\nPASS: ${results.length} of ${results.length}`);
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error('CRASH', e.message); process.exit(2); });
