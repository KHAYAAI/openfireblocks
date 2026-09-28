import {
  Injectable,
  Logger,
  ServiceUnavailableException,
  UnauthorizedException,
} from '@nestjs/common';
import { JwtService } from '@nestjs/jwt';
import { WorkOS } from '@workos-inc/node';
import { createHmac, randomBytes, timingSafeEqual } from 'crypto';
import { UsersService, User } from './users.service';
import { JwtClaims, PublicUser } from './auth.service';

const ACCESS_TOKEN_TTL_SECONDS = 3600;
const STATE_TTL_MS = 10 * 60 * 1000; // 10 minutes -- an authorization round trip

export interface SsoLoginResult {
  accessToken: string;
  expiresIn: number;
  user: PublicUser;
}

function toPublicUser(user: User): PublicUser {
  return {
    id: user.id,
    email: user.email,
    fullName: user.full_name,
    role: user.role,
    mfaEnabled: user.mfa_enabled,
  };
}

// Enterprise SSO via WorkOS AuthKit, alongside (not replacing) the local
// password + TOTP flow in AuthService.
//
// Deliberate design choice: after WorkOS verifies the identity, this issues
// THIS platform's own session JWT -- the same claims AuthService.issueToken
// produces -- rather than handing WorkOS's access token to the rest of the
// system. Every protected route already trusts JwtAuthStrategy; swapping
// that for WorkOS token verification everywhere would be a far larger
// change, and two parallel session models is exactly the kind of split this
// codebase has been bitten by before (see the two customers schemas, and
// ceremony-orchestrator's dead parallel ceremony API). One session model,
// two ways to prove who you are.
//
// Unconfigured is a valid state: with WORKOS_API_KEY/WORKOS_CLIENT_ID unset
// the SSO routes fail closed with 503 and the password flow is unaffected.
@Injectable()
export class WorkosSsoService {
  private readonly logger = new Logger(WorkosSsoService.name);
  private client: WorkOS | null = null;

  constructor(
    private readonly users: UsersService,
    private readonly jwt: JwtService,
  ) {}

  isConfigured(): boolean {
    return Boolean(process.env.WORKOS_API_KEY && process.env.WORKOS_CLIENT_ID);
  }

  private workos(): WorkOS {
    if (!this.isConfigured()) {
      throw new ServiceUnavailableException(
        'WorkOS SSO not configured (set WORKOS_API_KEY and WORKOS_CLIENT_ID)',
      );
    }
    if (!this.client) {
      this.client = new WorkOS(process.env.WORKOS_API_KEY as string, {
        clientId: process.env.WORKOS_CLIENT_ID,
      });
    }
    return this.client;
  }

  private redirectUri(): string {
    const uri = process.env.WORKOS_REDIRECT_URI;
    if (!uri) {
      throw new ServiceUnavailableException(
        'WorkOS SSO not configured (set WORKOS_REDIRECT_URI to this gateway\'s /auth/sso/callback URL)',
      );
    }
    return uri;
  }

  // The `state` parameter is the OAuth CSRF defense: without it, an attacker
  // can feed a victim's browser their own authorization code and silently log
  // the victim into the attacker's account. Signed with JWT_SECRET (already
  // required in production) and time-boxed, so a state value can't be forged
  // or replayed indefinitely, and no server-side session store is needed.
  private signState(nonce: string, issuedAt: number): string {
    const payload = `${nonce}.${issuedAt}`;
    const mac = createHmac('sha256', this.stateSecret()).update(payload).digest('hex');
    return `${payload}.${mac}`;
  }

  private stateSecret(): string {
    // Reuses the session-signing secret rather than introducing a second
    // one to rotate and forget -- see jwt.strategy.ts's jwtSecret().
    const secret = process.env.JWT_SECRET;
    if (!secret) {
      if (process.env.NODE_ENV === 'production') {
        throw new Error('JWT_SECRET must be set in production');
      }
      return 'dev-only-insecure-jwt-secret-do-not-use-in-production';
    }
    return secret;
  }

  createState(): string {
    return this.signState(randomBytes(16).toString('hex'), Date.now());
  }

  verifyState(state: string | undefined): void {
    if (!state) {
      throw new UnauthorizedException('missing SSO state');
    }
    const parts = state.split('.');
    if (parts.length !== 3) {
      throw new UnauthorizedException('malformed SSO state');
    }
    const [nonce, issuedAtRaw, mac] = parts;
    const expected = createHmac('sha256', this.stateSecret())
      .update(`${nonce}.${issuedAtRaw}`)
      .digest('hex');

    const given = Buffer.from(mac, 'hex');
    const want = Buffer.from(expected, 'hex');
    if (given.length !== want.length || !timingSafeEqual(given, want)) {
      throw new UnauthorizedException('invalid SSO state');
    }

    const issuedAt = Number(issuedAtRaw);
    if (!Number.isFinite(issuedAt) || Date.now() - issuedAt > STATE_TTL_MS) {
      throw new UnauthorizedException('expired SSO state');
    }
  }

  // Step 1: where to send the browser. organizationId scopes the login to a
  // specific customer's IdP connection; omitting it lets AuthKit route by
  // the email domain the user enters.
  authorizationUrl(options?: {
    organizationId?: string;
    loginHint?: string;
    screenHint?: 'sign-in' | 'sign-up';
  }): { url: string; state: string } {
    const state = this.createState();
    const url = this.workos().userManagement.getAuthorizationUrl({
      provider: 'authkit',
      clientId: process.env.WORKOS_CLIENT_ID as string,
      redirectUri: this.redirectUri(),
      state,
      organizationId: options?.organizationId,
      loginHint: options?.loginHint,
      screenHint: options?.screenHint,
    });
    return { url, state };
  }

  // Step 2: exchange the authorization code, resolve it to a local user
  // (provisioning one on first login), and issue our own session token.
  async completeLogin(code: string, state: string | undefined): Promise<SsoLoginResult> {
    this.verifyState(state);

    let authentication;
    try {
      authentication = await this.workos().userManagement.authenticateWithCode({
        code,
        clientId: process.env.WORKOS_CLIENT_ID as string,
      });
    } catch (err) {
      this.logger.warn(`WorkOS code exchange failed: ${(err as Error).message}`);
      throw new UnauthorizedException('SSO authentication failed');
    }

    const { user: workosUser, organizationId } = authentication;
    const user = await this.resolveLocalUser({
      workosUserId: workosUser.id,
      email: workosUser.email,
      emailVerified: workosUser.emailVerified,
      fullName:
        workosUser.name ??
        [workosUser.firstName, workosUser.lastName].filter(Boolean).join(' ') ??
        workosUser.email,
      organizationId,
    });

    await this.users.recordSuccessfulLogin(user.id);
    const claims: JwtClaims = { sub: user.id, email: user.email, role: user.role };
    const accessToken = await this.jwt.signAsync(claims, {
      expiresIn: ACCESS_TOKEN_TTL_SECONDS,
    });

    return {
      accessToken,
      expiresIn: ACCESS_TOKEN_TTL_SECONDS,
      user: toPublicUser(user),
    };
  }

  // Just-in-time provisioning, with the account-linking decision made
  // explicitly rather than by accident:
  //
  //   - Known WorkOS user id -> that local user. The stable link.
  //   - Unknown WorkOS id, no local account with that email -> provision.
  //   - Unknown WorkOS id, but a local PASSWORD account owns that email ->
  //     link the two ONLY if the IdP asserts the email is verified.
  //     Linking on an unverified email would let anyone who can make an IdP
  //     assert an arbitrary address take over an existing account.
  private async resolveLocalUser(input: {
    workosUserId: string;
    email: string;
    emailVerified: boolean;
    fullName: string;
    organizationId?: string;
  }): Promise<User> {
    const linked = await this.users.findByWorkosUserId(input.workosUserId);
    if (linked) {
      if (linked.status !== 'active') {
        throw new UnauthorizedException('account is not active');
      }
      return linked;
    }

    const byEmail = await this.users.findByEmail(input.email);
    if (byEmail) {
      if (byEmail.status !== 'active') {
        throw new UnauthorizedException('account is not active');
      }
      if (!input.emailVerified) {
        this.logger.warn(
          `refusing to link WorkOS user ${input.workosUserId} to existing account ${byEmail.id}: email not verified by the IdP`,
        );
        throw new UnauthorizedException(
          'an account with this email already exists and the identity provider has not verified this address',
        );
      }
      return this.users.linkWorkosIdentity(byEmail.id, {
        workosUserId: input.workosUserId,
        workosOrganizationId: input.organizationId,
      });
    }

    return this.users.createSsoUser({
      email: input.email,
      fullName: input.fullName || input.email,
      workosUserId: input.workosUserId,
      workosOrganizationId: input.organizationId,
    });
  }
}
