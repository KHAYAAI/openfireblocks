import { createServer, Server } from 'http';
import { AddressInfo } from 'net';
import { createHash } from 'crypto';
import { JwtService } from '@nestjs/jwt';
import { exportJWK, generateKeyPair, SignJWT } from 'jose';
import { OidcSsoService } from './oidc-sso.service';
import { createState } from './sso-state';

// Against a local stand-in for an identity provider that speaks the real
// protocol (discovery, JWKS, a token endpoint that checks PKCE, RS256 ID
// tokens). It proves this service's checks; it is not a run against Keycloak,
// Entra or Okta.

type Row = Record<string, any>;
class FakeUsers {
  rows: Row[] = [];
  async findByOidcIdentity(iss: string, sub: string) { return this.rows.find((r) => r.oidc_issuer === iss && r.oidc_subject === sub) ?? null; }
  async findByEmail(email: string) { return this.rows.find((r) => r.email === email.toLowerCase()) ?? null; }
  async createOidcUser(i: any) { const r = { id: `u${this.rows.length + 1}`, email: i.email.toLowerCase(), full_name: i.fullName, role: i.role ?? 'user', status: 'active', auth_provider: 'oidc', oidc_issuer: i.issuer, oidc_subject: i.subject, mfa_enabled: false }; this.rows.push(r); return r; }
  async linkOidcIdentity(id: string, iss: string, sub: string) { const r = this.rows.find((x) => x.id === id)!; Object.assign(r, { oidc_issuer: iss, oidc_subject: sub }); return r; }
  async recordSuccessfulLogin() {}
}

describe('OidcSsoService', () => {
  let server: Server; let base: string; let privateKey: CryptoKey; let jwk: any;
  let users: FakeUsers; let svc: OidcSsoService;
  // What the next ID token says; each test adjusts it.
  let claims: Row; let tokenOpts: { aud?: string; iss?: string; expSeconds?: number; badNonce?: boolean; pkceSeen?: string[] };
  const pkceSeen: string[] = [];

  beforeAll(async () => {
    const kp = await generateKeyPair('RS256'); privateKey = kp.privateKey as CryptoKey; jwk = { ...(await exportJWK(kp.publicKey)), kid: 'k1', alg: 'RS256', use: 'sig' };
    server = createServer(async (req, res) => {
      const url = new URL(req.url!, base);
      const send = (o: unknown) => { res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(o)); };
      if (url.pathname === '/realm/.well-known/openid-configuration') {
        return send({ issuer: tokenOpts.iss === 'discovery-mismatch' ? 'https://other.example' : `${base}/realm`, authorization_endpoint: `${base}/realm/auth`, token_endpoint: `${base}/realm/token`, jwks_uri: `${base}/realm/jwks` });
      }
      if (url.pathname === '/realm/jwks') return send({ keys: [jwk] });
      if (url.pathname === '/realm/token') {
        let b = ''; for await (const c of req) b += c;
        const f = new URLSearchParams(b);
        const [nonce, challenge] = Buffer.from(f.get('code')!, 'base64url').toString().split('|');
        // The real check an IdP does: the verifier must hash to the challenge sent at /auth.
        if (createHash('sha256').update(f.get('code_verifier') ?? '').digest('base64url') !== challenge) { res.statusCode = 400; return res.end('invalid_grant'); }
        pkceSeen.push(f.get('code_verifier')!);
        const jwt = await new SignJWT({ ...claims, nonce: tokenOpts.badNonce ? 'wrong' : nonce })
          .setProtectedHeader({ alg: 'RS256', kid: 'k1' }).setIssuer(tokenOpts.iss && tokenOpts.iss !== 'discovery-mismatch' ? tokenOpts.iss : `${base}/realm`)
          .setAudience(tokenOpts.aud ?? 'ofb-console').setIssuedAt().setExpirationTime(`${tokenOpts.expSeconds ?? 300}s`).sign(privateKey);
        return send({ id_token: jwt, access_token: 'x', token_type: 'Bearer' });
      }
      res.statusCode = 404; res.end();
    });
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  });
  afterAll(() => new Promise((r) => server.close(r)));

  beforeEach(() => {
    process.env.OIDC_ISSUER = `${base}/realm`; process.env.OIDC_CLIENT_ID = 'ofb-console'; process.env.OIDC_REDIRECT_URI = 'https://gw.example/auth/sso/callback';
    delete process.env.OIDC_ALLOWED_EMAIL_DOMAINS; delete process.env.OIDC_CLIENT_SECRET;
    claims = { sub: 'kc-1', email: 'Ada@Bank.Example', email_verified: true, name: 'Ada Admin' }; tokenOpts = {};
    users = new FakeUsers(); svc = new OidcSsoService(users as never, new JwtService({ secret: 's', signOptions: { issuer: 'openfireblocks' } }));
  });

  // Plays the browser: ask for the authorization URL, then hand the code back
  // with the state (and, as the IdP would remember, the nonce and challenge).
  async function login() {
    const { url, state } = await svc.authorizationUrl();
    const q = new URL(url).searchParams;
    expect(q.get('code_challenge_method')).toBe('S256');
    const code = Buffer.from(`${q.get('nonce')}|${q.get('code_challenge')}`).toString('base64url');
    return { code, state, q };
  }

  it('is off until configured, and says so', async () => {
    delete process.env.OIDC_ISSUER;
    expect(svc.isConfigured()).toBe(false);
    await expect(svc.authorizationUrl()).rejects.toThrow(/not configured/);
  });

  it('asks the provider for the code flow with PKCE, a nonce and our state', async () => {
    const { q } = await login();
    expect(q.get('response_type')).toBe('code'); expect(q.get('client_id')).toBe('ofb-console');
    expect(q.get('scope')).toBe('openid email profile'); expect(q.get('state')).toBeTruthy(); expect(q.get('nonce')).toBeTruthy();
    expect(q.get('redirect_uri')).toBe('https://gw.example/auth/sso/callback');
  });

  it('signs a person in, provisions them without a password, and issues our own session', async () => {
    const { code, state } = await login();
    const out = await svc.completeLogin(code, state);
    expect(out.accessToken.split('.')).toHaveLength(3);
    expect(users.rows).toHaveLength(1);
    expect(users.rows[0]).toMatchObject({ email: 'ada@bank.example', auth_provider: 'oidc', oidc_subject: 'kc-1', oidc_issuer: `${base}/realm` });
    expect(pkceSeen.length).toBeGreaterThan(0); // the verifier really went to the token endpoint
    // A second login finds the same person.
    const again = await login(); await svc.completeLogin(again.code, again.state);
    expect(users.rows).toHaveLength(1);
  });

  it.each([
    ['an ID token for another client', () => { tokenOpts.aud = 'someone-else'; }],
    ['an ID token from another issuer', () => { tokenOpts.iss = 'https://evil.example/realm'; }],
    ['an expired ID token', () => { tokenOpts.expSeconds = -60; }],
    ['an ID token whose nonce is not this login\'s', () => { tokenOpts.badNonce = true; }],
    ['an ID token with no email', () => { delete claims.email; }],
  ])('refuses %s', async (_n, arrange) => {
    arrange();
    const { code, state } = await login();
    await expect(svc.completeLogin(code, state)).rejects.toThrow();
    expect(users.rows).toHaveLength(0);
  });

  it('refuses a forged, missing or expired state before talking to the provider', async () => {
    const { code } = await login();
    await expect(svc.completeLogin(code, undefined)).rejects.toThrow(/missing/);
    await expect(svc.completeLogin(code, 'a.b.c')).rejects.toThrow(/invalid/);
    await expect(svc.completeLogin(code, createState(Date.now() - 11 * 60_000))).rejects.toThrow(/expired/);
  });

  it('refuses a code exchange whose PKCE verifier does not match', async () => {
    const { code, state } = await login();
    const { state: other } = await svc.authorizationUrl(); // a different login's state => different verifier
    await expect(svc.completeLogin(code, other)).rejects.toThrow();
    void state;
  });

  it('can be limited to the customer\'s own email domains', async () => {
    process.env.OIDC_ALLOWED_EMAIL_DOMAINS = 'treasury.gov.example';
    const { code, state } = await login();
    await expect(svc.completeLogin(code, state)).rejects.toThrow(/domain/);
  });

  describe('linking to an existing password account', () => {
    const existing = () => users.rows.push({ id: 'p1', email: 'ada@bank.example', status: 'active', auth_provider: 'password', role: 'user', full_name: 'Ada', oidc_issuer: null, oidc_subject: null });
    it('links only when the provider has verified the address', async () => {
      existing(); claims.email_verified = true;
      const { code, state } = await login(); await svc.completeLogin(code, state);
      expect(users.rows).toHaveLength(1); expect(users.rows[0].oidc_subject).toBe('kc-1');
    });
    it('refuses to take over the account on an unverified address', async () => {
      existing(); claims.email_verified = false;
      const { code, state } = await login();
      await expect(svc.completeLogin(code, state)).rejects.toThrow(/not verified|has not verified/);
      expect(users.rows[0].oidc_subject).toBeNull();
    });
    it('never rebinds an account that already belongs to a different identity', async () => {
      users.rows.push({ id: 'o1', email: 'ada@bank.example', status: 'active', auth_provider: 'oidc', role: 'user', full_name: 'Ada', oidc_issuer: `${base}/realm`, oidc_subject: 'someone-else' });
      const { code, state } = await login();
      await expect(svc.completeLogin(code, state)).rejects.toThrow(/different identity/);
    });
  });

  it('will not trust a discovery document that describes another issuer', async () => {
    tokenOpts.iss = 'discovery-mismatch';
    await expect(svc.authorizationUrl()).rejects.toThrow(/could not be reached/);
  });
});
