// Sign in to the gateway through an independent OpenID Connect provider
// (oidc-provider-server.js wraps node-oidc-provider): the browser's journey,
// without a browser. Authorization code + PKCE; the gateway verifies the ID
// token itself.
const GATEWAY = process.env.GATEWAY || 'http://127.0.0.1:13999';
const results = [];
const check = (ok, name, detail = '') => { results.push(ok); console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${ok || !detail ? '' : ' :: ' + detail}`); };

class Jar {
  constructor() { this.c = new Map(); }
  add(res) { for (const sc of res.headers.getSetCookie?.() ?? []) { const [kv] = sc.split(';'); const i = kv.indexOf('='); this.c.set(kv.slice(0, i), kv.slice(i + 1)); } }
  header() { return [...this.c].map(([k, v]) => `${k}=${v}`).join('; '); }
}
async function get(jar, url, init = {}) {
  const res = await fetch(url, { redirect: 'manual', ...init, headers: { cookie: jar.header(), ...(init.headers || {}) } });
  jar.add(res); return res;
}

// Follow redirects until we reach a page that is not one, or until the next hop
// is to `stopAt` (returned unfollowed).
async function follow(jar, res, base, stopAt) {
  for (let i = 0; i < 15; i++) {
    const loc = res.headers.get('location'); if (!loc || res.status < 300 || res.status > 399) return { res, url: base };
    const next = new URL(loc, base).toString();
    if (stopAt && next.startsWith(stopAt)) return { res, stopped: next };
    base = next; res = await get(jar, next);
    if (process.env.DEBUG) console.log('  ->', res.status, next.slice(0, 120));
  }
  throw new Error('too many redirects');
}

async function login(email) {
  const jar = new Jar();
  let r = await get(jar, GATEWAY + '/auth/sso/authorize');
  if (r.status !== 302) return { error: 'authorize answered ' + r.status + ' ' + (await r.text()).slice(0, 200) };
  const authUrl = r.headers.get('location');
  r = await get(jar, authUrl);
  let { res, url, stopped } = await follow(jar, r, authUrl, GATEWAY + '/auth/sso/callback');
  if (!stopped) {
    // The provider's login form.
    const html = await res.text(); const m = html.match(/<form[^>]*action="([^"]+)"[^>]*>/);
    if (!m) return { error: 'no login form: ' + html.slice(0, 200) };
    const action = new URL(m[1].replace(/&amp;/g, '&'), url).toString();
    if (process.env.DEBUG) console.log('  form action', action, html.slice(html.indexOf('<form'), html.indexOf('<form') + 400));
    const post = await get(jar, action, { method: 'POST', headers: { 'content-type': 'application/x-www-form-urlencoded' }, body: new URLSearchParams({ prompt: 'login', login: email, password: 'any' }).toString() });
    if (process.env.DEBUG) console.log('  post ->', post.status, post.headers.get('location'));
    ({ res, url, stopped } = await follow(jar, post, action, GATEWAY + '/auth/sso/callback'));
    // A consent page, if the provider shows one.
    if (!stopped) {
      const html2 = await res.text(); const m2 = html2.match(/<form[^>]*action="([^"]+)"[^>]*>/);
      if (m2) {
        const a2 = new URL(m2[1].replace(/&amp;/g, '&'), url).toString();
        const p2 = await get(jar, a2, { method: 'POST', headers: { 'content-type': 'application/x-www-form-urlencoded' }, body: 'prompt=consent' });
        ({ res, url, stopped } = await follow(jar, p2, a2, GATEWAY + '/auth/sso/callback'));
      }
    }
  }
  if (!stopped) return { error: 'never reached the gateway callback; last status ' + res.status };
  return { callback: stopped };
}

(async () => {
  const st = await (await fetch(GATEWAY + '/auth/sso/status')).json();
  check(st.enabled && st.provider === 'oidc', 'the gateway reports OIDC sign-in is available', JSON.stringify(st));

  const email = `ada-${Date.now()}@bank.example`;
  const a = await login(email);
  check(!a.error, 'the provider authenticates the person and sends them back with a code', a.error);
  if (a.error) process.exit(1);

  const cb = await fetch(a.callback);
  const body = await cb.json();
  check(cb.status === 200 && typeof body.accessToken === 'string', 'the gateway verifies the ID token and issues its own session', JSON.stringify(body).slice(0, 200));
  check(body.user?.email === email, 'the person is provisioned from the provider\'s claims', JSON.stringify(body.user));

  const me = await fetch(GATEWAY + '/me/organisations', { headers: { authorization: 'Bearer ' + body.accessToken } });
  check(me.status === 200, 'the session works against a protected route', String(me.status));

  const replay = await fetch(a.callback);
  check(replay.status === 401, 'the same authorization code cannot be used twice', String(replay.status));

  const b = await login(email);
  const tampered = b.callback.replace(/state=[^&]+/, (m) => m.slice(0, -2) + 'xx');
  const t = await fetch(tampered);
  check(t.status === 401, 'a callback with a tampered state is refused', String(t.status));

  const c = await login(email);
  const again = await (await fetch(c.callback)).json();
  check(again.user?.id === body.user?.id, 'signing in again finds the same person, not a new account');

  const failed = results.filter((x) => !x).length;
  console.log(failed ? `\nFAILED: ${failed} of ${results.length}` : `\nPASS: ${results.length} of ${results.length}`);
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error('CRASH', e); process.exit(2); });
