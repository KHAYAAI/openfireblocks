import { clearSessionCookie, issueSession, readSession } from './session';
import { h, shortHex, statusPill, table, when } from './views';
import { overviewPage, transactionsPage, TransactionRow } from './pages';

// The dashboard's two security-relevant surfaces: the cookie that says who
// is signed in, and the escaping of everything rendered into a page.
//
// Both are the kind of thing that works in every manual test and fails
// exactly once, against someone who is trying.

describe('session cookies', () => {
  const customerId = '11111111-1111-4111-8111-111111111111';

  it('round-trips a signed-in customer', () => {
    const { cookie } = issueSession(customerId);
    const session = readSession(cookie.split(';')[0]);

    expect(session).not.toBeNull();
    expect(session!.customerId).toBe(customerId);
    expect(session!.expiresAt).toBeGreaterThan(Date.now());
  });

  // The whole point of signing it. Without verification, a cookie is a
  // text field in which a visitor writes whichever customer they would
  // like to be.
  it('rejects a cookie whose customer id was edited', () => {
    const { cookie } = issueSession(customerId);
    const value = cookie.split(';')[0];
    const tampered = value.replace(customerId, '22222222-2222-4222-8222-222222222222');

    expect(readSession(tampered)).toBeNull();
  });

  it('rejects a cookie whose expiry was extended', () => {
    const { cookie } = issueSession(customerId);
    const value = cookie.split(';')[0];
    const [name, payload] = value.split('=');
    const [id, , sig] = payload.split('.');
    const forged = `${name}=${id}.${Date.now() + 10 ** 9}.${sig}`;

    expect(readSession(forged)).toBeNull();
  });

  it('rejects a cookie with no signature at all', () => {
    expect(readSession(`ofb_session=${customerId}.${Date.now() + 1000}`)).toBeNull();
    expect(readSession(`ofb_session=${customerId}`)).toBeNull();
  });

  it('rejects an expired session', () => {
    const { cookie } = issueSession(customerId);
    const value = cookie.split(';')[0];
    // Rewind the clock past the eight-hour TTL.
    const realNow = Date.now;
    Date.now = () => realNow() + 9 * 60 * 60 * 1000;
    try {
      expect(readSession(value)).toBeNull();
    } finally {
      Date.now = realNow;
    }
  });

  it('ignores unrelated cookies and missing headers', () => {
    expect(readSession(undefined)).toBeNull();
    expect(readSession('')).toBeNull();
    expect(readSession('other=1; another=2')).toBeNull();
  });

  it('finds its cookie among others', () => {
    const { cookie } = issueSession(customerId);
    const value = cookie.split(';')[0];

    expect(readSession(`a=1; ${value}; b=2`)?.customerId).toBe(customerId);
  });

  // HttpOnly so injected script cannot read it; SameSite=Strict because
  // the dashboard performs state-changing requests and a cross-site post
  // must not carry the session.
  it('sets the flags that make the cookie worth having', () => {
    const { cookie } = issueSession(customerId);

    expect(cookie).toContain('HttpOnly');
    expect(cookie).toContain('SameSite=Strict');
    expect(cookie).toContain('Path=/dashboard');
    expect(cookie).toContain('Secure');
  });

  it('can drop Secure only when explicitly running without TLS', () => {
    process.env.DASHBOARD_INSECURE_COOKIES = '1';
    try {
      expect(issueSession(customerId).cookie).not.toContain('Secure');
    } finally {
      delete process.env.DASHBOARD_INSECURE_COOKIES;
    }
  });

  it('clears by expiry rather than by hoping the browser forgets', () => {
    expect(clearSessionCookie()).toContain('Max-Age=0');
  });
});

describe('escaping', () => {
  it('neutralises the characters that end an attribute or open a tag', () => {
    expect(h(`<script>alert(1)</script>`)).toBe(
      '&lt;script&gt;alert(1)&lt;/script&gt;',
    );
    expect(h(`" onload="x`)).toBe('&quot; onload=&quot;x');
    expect(h(`' onload='x`)).toBe('&#39; onload=&#39;x');
    expect(h('a & b')).toBe('a &amp; b');
  });

  it('escapes the ampersand first, so escapes are not double-decoded', () => {
    // If & were escaped last, "&lt;" would already be in the string and
    // become "&amp;lt;" -- and if it were not escaped at all, a payload of
    // "&lt;script&gt;" would be decoded by the browser into a real tag.
    expect(h('&lt;')).toBe('&amp;lt;');
  });

  it('renders null and undefined as nothing rather than as words', () => {
    expect(h(null)).toBe('');
    expect(h(undefined)).toBe('');
  });

  // The fields most likely to carry a payload are the customer-supplied
  // ones: a key name, a webhook URL, a token symbol.
  it('escapes a hostile key name in a rendered page', () => {
    const page = transactionsPage(
      { active: 'transactions', customerName: `<img src=x onerror=alert(1)>`, tier: 'pro' },
      [],
    );

    expect(page).not.toContain('<img src=x');
    expect(page).toContain('&lt;img src=x');
  });

  it('escapes hostile data inside a table cell', () => {
    const tx: TransactionRow = {
      request_id: 'r1',
      chain: `</td><script>alert(1)</script>`,
      status: 'signed',
      asset_symbol: `"><b>`,
      effective_to: '0xabc',
      effective_amount: '1000000',
      asset_decimals: 6,
      asset_peg: 'USD',
      to_address: '0xdef',
      amount: '0',
      tx_hash: null,
      created_at: new Date().toISOString(),
    };

    const page = transactionsPage(
      { active: 'transactions', customerName: 'demo', tier: 'pro' },
      [tx],
    );

    expect(page).not.toContain('<script>alert(1)</script>');
    expect(page).not.toContain('"><b>');
    expect(page).toContain('&lt;script&gt;');
  });
});

describe('rendering', () => {
  const nav = { active: 'overview', customerName: 'demo', tier: 'enterprise' };

  it('shows both ends of a long hash, so it can be matched by eye', () => {
    const rendered = shortHex('0x' + 'ab'.repeat(32), 6);

    expect(rendered).toContain('0xabab');
    expect(rendered).toContain('…');
    expect(rendered).toContain('title=');
  });

  it('leaves a short value alone', () => {
    expect(shortHex('0x1234')).toBe('0x1234');
  });

  it('renders a missing value as a dash rather than as "null"', () => {
    expect(shortHex(null)).toBe('—');
    expect(when(null)).toBe('—');
    expect(when('not a date')).toBe('—');
  });

  it('distinguishes a good status from a bad one', () => {
    expect(statusPill('active')).toContain('pill ok');
    expect(statusPill('failed')).toContain('pill bad');
    expect(statusPill('pending_dkg')).toContain('pill warn');
  });

  it('says what is missing instead of rendering an empty table', () => {
    const rendered = table(['A'], [], 'Nothing here yet.');

    expect(rendered).toContain('Nothing here yet.');
    expect(rendered).not.toContain('<tbody>');
  });

  // Base units mean nothing to a person. The decimals recorded against the
  // transaction are used, not a current registry lookup -- what an old
  // transfer moved cannot be changed by editing the registry today.
  it('formats an amount with the decimals recorded on the transaction', () => {
    const usdc: TransactionRow = {
      request_id: 'r', chain: 'ethereum', status: 'signed',
      asset_symbol: 'USDC', effective_to: '0xabc', effective_amount: '1500250000',
      asset_decimals: 6, asset_peg: 'USD', to_address: '0xtoken', amount: '0',
      tx_hash: null, created_at: new Date().toISOString(),
    };

    const page = transactionsPage({ ...nav, active: 'transactions' }, [usdc]);

    expect(page).toContain('1500.25');
    // And not the raw base units, which would read as a transfer a
    // thousand times larger.
    expect(page).not.toContain('>1500250000<');
  });

  it('marks an undecoded transfer rather than showing its envelope as a payment', () => {
    const unknown: TransactionRow = {
      request_id: 'r', chain: 'ethereum', status: 'signed',
      asset_symbol: null, effective_to: null, effective_amount: null,
      asset_decimals: null, asset_peg: null, to_address: '0xcontract', amount: '0',
      tx_hash: null, created_at: new Date().toISOString(),
    };

    const page = transactionsPage({ ...nav, active: 'transactions' }, [unknown]);

    expect(page).toContain('undecoded');
  });

  // Party isolation belongs on the first page somebody opens, not in a
  // runbook. It is the difference between "2 of 3" and "2 of 3 protects
  // you against a host compromise".
  it('warns on the overview when the parties are not separated', () => {
    const page = overviewPage(nav, {
      keyCount: 3, activeKeys: 3, pendingKeys: 0, failedKeys: 0,
      signaturesThisMonth: 12, recentTransactions: [], isolation: 'simulated',
    });

    expect(page).toContain('Party isolation');
    expect(page).toContain('simulated');
  });

  it('does not warn when the parties are properly separated', () => {
    const page = overviewPage(nav, {
      keyCount: 1, activeKeys: 1, pendingKeys: 0, failedKeys: 0,
      signaturesThisMonth: 0, recentTransactions: [], isolation: 'multi-account',
    });

    expect(page).not.toContain('Party isolation:');
  });

  it('renders a complete document', () => {
    const page = overviewPage(nav, {
      keyCount: 0, activeKeys: 0, pendingKeys: 0, failedKeys: 0,
      signaturesThisMonth: 0, recentTransactions: [], isolation: null,
    });

    expect(page.startsWith('<!DOCTYPE html>')).toBe(true);
    expect(page).toContain('<meta name="viewport"');
    expect(page.trimEnd().endsWith('</html>')).toBe(true);
    // No inline script anywhere: the pages are static HTML, which is what
    // lets a strict content security policy stay strict.
    expect(page).not.toContain('<script');
  });
});
