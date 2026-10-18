import { Injectable, Logger, ServiceUnavailableException, UnauthorizedException } from '@nestjs/common';
import { JwtService } from '@nestjs/jwt';
import { createHash } from 'crypto';
import { createRemoteJWKSet, jwtVerify, JWTVerifyGetKey } from 'jose';
import { JwtClaims } from './auth.service';
import { User, UsersService } from './users.service';
import { createState, derive, verifyState } from './sso-state';
import { SsoLoginResult, toPublicUser } from './workos-sso.service';

const ACCESS_TOKEN_TTL_SECONDS = 3600;
const HTTP_TIMEOUT_MS = 10_000;

interface Discovery {
  issuer: string;
  authorization_endpoint: string;
  token_endpoint: string;
  jwks_uri: string;
  userinfo_endpoint?: string;
}

// Sign-in through any standards-compliant OpenID Connect provider (Keycloak,
// Microsoft Entra ID, Okta, ...), with no third-party broker in the path. A
// bank or a government runs its own identity provider inside its own
// boundary; this is what lets people sign in with it.
//
// Authorization code flow with PKCE. The ID token is verified here (signature
// against the provider's published keys, issuer, audience, expiry and the
// login's nonce) and then, as with the other login paths, exchanged for this
// platform's own session token, so the rest of the system sees one session
// model. Unconfigured is a valid state: the routes fail closed and password
// plus one-time-code login is unaffected.
//
//   OIDC_ISSUER          e.g. https://id.bank.example/realms/treasury
//   OIDC_CLIENT_ID
//   OIDC_CLIENT_SECRET   optional for a public client (PKCE is always used)
//   OIDC_REDIRECT_URI    this gateway's /auth/sso/callback
//   OIDC_SCOPES          default "openid email profile"
//   OIDC_ALLOWED_EMAIL_DOMAINS  optional comma list; others are refused
@Injectable()
export class OidcSsoService {
  private readonly logger = new Logger(OidcSsoService.name);
  private discovery?: Promise<Discovery>;
  private jwks?: JWTVerifyGetKey;

  constructor(private readonly users: UsersService, private readonly jwt: JwtService) {}

  isConfigured(): boolean {
    return Boolean(process.env.OIDC_ISSUER && process.env.OIDC_CLIENT_ID && process.env.OIDC_REDIRECT_URI);
  }

  private issuer(): string {
    if (!this.isConfigured()) {
      throw new ServiceUnavailableException('OIDC sign-in is not configured (OIDC_ISSUER, OIDC_CLIENT_ID, OIDC_REDIRECT_URI)');
    }
    const issuer = (process.env.OIDC_ISSUER as string).replace(/\/+$/, '');
    // Tokens and codes cross this connection: it must be encrypted, except to
    // a provider on this machine (development, tests).
    const u = new URL(issuer);
    if (u.protocol !== 'https:' && !['localhost', '127.0.0.1', '[::1]'].includes(u.hostname)) {
      throw new ServiceUnavailableException('OIDC_ISSUER must be an https URL');
    }
    return issuer;
  }

  private async fetchJson(url: string, init?: RequestInit): Promise<any> {
    const res = await fetch(url, { ...init, signal: AbortSignal.timeout(HTTP_TIMEOUT_MS) });
    const text = await res.text();
    if (!res.ok) throw new Error(`${res.status} ${text.slice(0, 200)}`);
    return JSON.parse(text);
  }

  private async metadata(): Promise<Discovery> {
    const issuer = this.issuer();
    if (!this.discovery) {
      this.discovery = this.fetchJson(`${issuer}/.well-known/openid-configuration`).then((d) => {
        // The document must describe the issuer we asked about; otherwise a
        // misconfigured or hostile endpoint could redirect trust elsewhere.
        if (String(d.issuer).replace(/\/+$/, '') !== issuer) throw new Error(`discovery document issuer ${d.issuer} does not match ${issuer}`);
        for (const k of ['authorization_endpoint', 'token_endpoint', 'jwks_uri']) if (!d[k]) throw new Error(`discovery document has no ${k}`);
        return d as Discovery;
      });
      // A failed discovery must not be cached forever.
      this.discovery.catch(() => { this.discovery = undefined; });
    }
    try {
      return await this.discovery;
    } catch (e) {
      this.logger.warn(`OIDC discovery failed: ${(e as Error).message}`);
      throw new ServiceUnavailableException('the identity provider could not be reached');
    }
  }

  async authorizationUrl(options?: { loginHint?: string }): Promise<{ url: string; state: string }> {
    const meta = await this.metadata();
    const state = createState();
    const u = new URL(meta.authorization_endpoint);
    u.searchParams.set('response_type', 'code');
    u.searchParams.set('client_id', process.env.OIDC_CLIENT_ID as string);
    u.searchParams.set('redirect_uri', process.env.OIDC_REDIRECT_URI as string);
    u.searchParams.set('scope', process.env.OIDC_SCOPES ?? 'openid email profile');
    u.searchParams.set('state', state);
    u.searchParams.set('nonce', derive('nonce', state));
    u.searchParams.set('code_challenge', createHash('sha256').update(derive('pkce', state)).digest('base64url'));
    u.searchParams.set('code_challenge_method', 'S256');
    if (options?.loginHint) u.searchParams.set('login_hint', options.loginHint);
    return { url: u.toString(), state };
  }

  async completeLogin(code: string, state: string | undefined): Promise<SsoLoginResult> {
    verifyState(state);
    const meta = await this.metadata();

    const body = new URLSearchParams({
      grant_type: 'authorization_code',
      code,
      redirect_uri: process.env.OIDC_REDIRECT_URI as string,
      client_id: process.env.OIDC_CLIENT_ID as string,
      code_verifier: derive('pkce', state as string),
    });
    if (process.env.OIDC_CLIENT_SECRET) body.set('client_secret', process.env.OIDC_CLIENT_SECRET);

    let idToken: string;
    let providerAccessToken: string | undefined;
    try {
      const tok = await this.fetchJson(meta.token_endpoint, {
        method: 'POST',
        headers: { 'content-type': 'application/x-www-form-urlencoded', accept: 'application/json' },
        body,
      });
      idToken = tok.id_token;
      providerAccessToken = tok.access_token;
      if (!idToken) throw new Error('no id_token in the token response');
    } catch (e) {
      this.logger.warn(`OIDC code exchange failed: ${(e as Error).message}`);
      throw new UnauthorizedException('SSO authentication failed');
    }

    this.jwks ??= createRemoteJWKSet(new URL(meta.jwks_uri), { timeoutDuration: HTTP_TIMEOUT_MS });
    let claims: Record<string, any>;
    try {
      const { payload } = await jwtVerify(idToken, this.jwks, {
        issuer: meta.issuer,
        audience: process.env.OIDC_CLIENT_ID as string,
        // Only asymmetric algorithms: never accept "none" or an HMAC keyed
        // with something a client could know.
        algorithms: ['RS256', 'RS384', 'RS512', 'PS256', 'ES256', 'ES384', 'EdDSA'],
      });
      claims = payload;
    } catch (e) {
      this.logger.warn(`OIDC ID token rejected: ${(e as Error).message}`);
      throw new UnauthorizedException('SSO authentication failed');
    }
    if (claims.nonce !== derive('nonce', state as string)) {
      this.logger.warn('OIDC ID token nonce does not match this login');
      throw new UnauthorizedException('SSO authentication failed');
    }
    // The specification lets a provider leave profile claims out of the ID
    // token and serve them from the UserInfo endpoint instead (certified
    // implementations do exactly that when the client also receives an access
    // token). Ask for them there, and only trust that answer if it is about the
    // same person: its subject must equal the verified ID token's.
    if (claims.sub && typeof claims.email !== 'string' && meta.userinfo_endpoint && providerAccessToken) {
      try {
        const info = await this.fetchJson(meta.userinfo_endpoint, { headers: { authorization: `Bearer ${providerAccessToken}` } });
        if (info.sub !== claims.sub) throw new Error('UserInfo describes a different subject than the ID token');
        claims = { ...claims, email: info.email, email_verified: info.email_verified, name: info.name ?? claims.name, given_name: info.given_name, family_name: info.family_name };
      } catch (e) {
        this.logger.warn(`OIDC UserInfo lookup failed: ${(e as Error).message}`);
        throw new UnauthorizedException('SSO authentication failed');
      }
    }
    if (!claims.sub || typeof claims.email !== 'string') {
      throw new UnauthorizedException('the identity provider did not return an email address; request the "email" scope');
    }
    this.assertDomainAllowed(claims.email);

    const user = await this.resolveLocalUser({
      issuer: meta.issuer,
      subject: String(claims.sub),
      email: claims.email,
      emailVerified: claims.email_verified === true,
      fullName: String(claims.name ?? ([claims.given_name, claims.family_name].filter(Boolean).join(' ') || claims.email)),
    });
    await this.users.recordSuccessfulLogin(user.id);
    const session: JwtClaims = { sub: user.id, email: user.email, role: user.role };
    const accessToken = await this.jwt.signAsync(session, { expiresIn: ACCESS_TOKEN_TTL_SECONDS });
    return { accessToken, expiresIn: ACCESS_TOKEN_TTL_SECONDS, user: toPublicUser(user) };
  }

  private assertDomainAllowed(email: string): void {
    const allowed = (process.env.OIDC_ALLOWED_EMAIL_DOMAINS ?? '').split(',').map((d) => d.trim().toLowerCase()).filter(Boolean);
    if (allowed.length === 0) return;
    const domain = email.toLowerCase().split('@')[1];
    if (!allowed.includes(domain)) throw new UnauthorizedException('this email domain is not allowed to sign in');
  }

  // Same account-linking rule as the WorkOS path: the (issuer, subject) pair
  // is the stable link; an existing password account is linked only if the
  // provider asserts the address is verified, because linking on an
  // unverified email would let anyone who can make a provider assert an
  // arbitrary address take over that account.
  private async resolveLocalUser(i: { issuer: string; subject: string; email: string; emailVerified: boolean; fullName: string }): Promise<User> {
    const linked = await this.users.findByOidcIdentity(i.issuer, i.subject);
    if (linked) {
      if (linked.status !== 'active') throw new UnauthorizedException('account is not active');
      return linked;
    }
    const byEmail = await this.users.findByEmail(i.email);
    if (byEmail) {
      if (byEmail.status !== 'active') throw new UnauthorizedException('account is not active');
      if (!i.emailVerified) {
        this.logger.warn(`refusing to link OIDC subject ${i.subject} to account ${byEmail.id}: email not verified by the provider`);
        throw new UnauthorizedException('an account with this email already exists and the identity provider has not verified this address');
      }
      if (byEmail.auth_provider === 'oidc') {
        // Already bound to a different OIDC identity: never rebind silently.
        throw new UnauthorizedException('this account is already linked to a different identity');
      }
      return this.users.linkOidcIdentity(byEmail.id, i.issuer, i.subject);
    }
    return this.users.createOidcUser({ email: i.email, fullName: i.fullName, issuer: i.issuer, subject: i.subject });
  }
}
