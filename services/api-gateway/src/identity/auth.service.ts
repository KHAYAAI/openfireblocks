import {
  ConflictException,
  Injectable,
  UnauthorizedException,
} from '@nestjs/common';
import { JwtService } from '@nestjs/jwt';
import * as bcrypt from 'bcrypt';
import { v4 as uuid } from 'uuid';
import { UsersService, User } from './users.service';
import { MfaChallengesService } from './mfa-challenges.service';
import { TokenRevocationService } from './token-revocation.service';
import {
  generateMfaSecret,
  mfaEnrollmentUri,
  verifyTotpCode,
} from './mfa.util';

// Matches UsersService.BCRYPT_COST. Paid here too on the "email already
// registered" path -- see register() -- so an anonymous caller cannot
// time which branch ran.
const BCRYPT_COST = 12;

// A fixed, valid bcrypt hash with no corresponding real password. Compared
// against on login when no user was found, so that branch pays the same
// bcrypt cost a real comparison would -- otherwise "unknown email" returns
// measurably faster than "known email, wrong password", which is itself a
// timing oracle for account existence. Computed once; bcrypt.compare
// against it always returns false and never throws.
const DUMMY_PASSWORD_HASH = bcrypt.hashSync('no-such-password-timing-equalisation-only', BCRYPT_COST);

export interface JwtClaims {
  sub: string;
  email: string;
  role: string;
  // Unique per issued token (AUTH-03): what TokenRevocationService's
  // denylist is keyed on, so a single token can be revoked (logout)
  // without needing to invalidate every token for the user.
  jti?: string;
  // Standard JWT claims passport-jwt attaches to the decoded payload at
  // runtime regardless of whether this interface lists them; declared so
  // callers (step-up age checks, logout) don't need a type assertion to
  // read them.
  iat?: number;
  exp?: number;
}

export type LoginResult =
  | { status: 'mfa_required'; challengeToken: string }
  | { status: 'ok'; accessToken: string; expiresIn: number; user: PublicUser };

export interface PublicUser {
  id: string;
  email: string;
  fullName: string;
  role: string;
  mfaEnabled: boolean;
}

const ACCESS_TOKEN_TTL_SECONDS = 3600;

function toPublicUser(user: User): PublicUser {
  return {
    id: user.id,
    email: user.email,
    fullName: user.full_name,
    role: user.role,
    mfaEnabled: user.mfa_enabled,
  };
}

// Orchestrates the register -> login -> (optional MFA) -> JWT flow. Kept
// framework-light (constructor takes its collaborators directly) so it is
// unit-testable without bootstrapping the Nest DI container, matching
// BillingService/CustomerService elsewhere in this codebase.
@Injectable()
export class AuthService {
  constructor(
    private readonly users: UsersService,
    private readonly mfaChallenges: MfaChallengesService,
    private readonly jwt: JwtService,
    // Optional like elsewhere in this codebase: a test that constructs
    // this service directly without TokenRevocationService gets logout
    // as a no-op (the token still expires normally, just not early).
    private readonly revocation?: TokenRevocationService,
  ) {}

  // Registration used to answer "does this email already have an
  // account" for free: a fresh email returned 201, a registered one
  // returned 409 with "an account with this email already exists" --
  // and the 409 path returned measurably faster, since it skipped the
  // bcrypt hash the success path pays. Both are unauthenticated oracles.
  // Neither is visible here any more: UsersService.create still throws
  // ConflictException (a direct, trusted caller legitimately wants to
  // know), but this -- the only caller reachable from an anonymous HTTP
  // request -- catches it, pays the same hashing cost either way, and
  // returns the same shape regardless of which branch actually ran.
  async register(input: { email: string; password: string; fullName: string }): Promise<PublicUser> {
    try {
      const user = await this.users.create(input);
      return toPublicUser(user);
    } catch (err) {
      if (!(err instanceof ConflictException)) {
        throw err;
      }
      await bcrypt.hash(input.password, BCRYPT_COST);
      return {
        id: uuid(),
        email: input.email.toLowerCase(),
        fullName: input.fullName,
        role: 'user',
        mfaEnabled: false,
      };
    }
  }

  async login(email: string, password: string): Promise<LoginResult> {
    const user = await this.users.findByEmail(email);
    // Constant-shape failure: an unknown email, a locked account and a
    // wrong password all throw the exact same UnauthorizedException below
    // -- same status, same message. A locked account used to throw a
    // distinct 403 ("account temporarily locked"), which told an
    // unauthenticated caller the email was registered and had just failed
    // five logins; five requests per candidate turned that into a working
    // account-existence scan. There is no shortcut once an account is
    // locked either: bcrypt still runs below (or the dummy hash does), so
    // the response also takes the same time.
    if (!user || user.status !== 'active') {
      await bcrypt.compare(password, DUMMY_PASSWORD_HASH);
      throw new UnauthorizedException('invalid credentials');
    }

    const locked = this.users.isLocked(user);
    const valid = await this.users.verifyPassword(user, password);
    if (locked || !valid) {
      // Don't record a failed attempt while already locked: the window
      // is already running, and counting against it here would only let
      // an attacker probe whether a request landed during the lockout
      // without changing anything about when it ends.
      if (!locked) {
        await this.users.recordFailedLogin(user.id);
      }
      throw new UnauthorizedException('invalid credentials');
    }

    if (user.mfa_enabled) {
      const challengeToken = await this.mfaChallenges.create(user.id);
      return { status: 'mfa_required', challengeToken };
    }

    await this.users.recordSuccessfulLogin(user.id);
    return { status: 'ok', ...(await this.issueToken(user)), user: toPublicUser(user) };
  }

  async verifyMfaAndLogin(
    email: string,
    challengeToken: string,
    code: string,
  ): Promise<{ accessToken: string; expiresIn: number; user: PublicUser }> {
    const user = await this.users.findByEmail(email);
    if (!user || !user.mfa_enabled || !user.mfa_secret) {
      throw new UnauthorizedException('invalid MFA session');
    }

    const consumed = await this.mfaChallenges.consume(user.id, challengeToken);
    if (!consumed) {
      throw new UnauthorizedException('MFA challenge expired or already used');
    }

    if (!verifyTotpCode(user.mfa_secret, code)) {
      await this.users.recordFailedLogin(user.id);
      throw new UnauthorizedException('invalid MFA code');
    }

    await this.users.recordSuccessfulLogin(user.id);
    return { ...(await this.issueToken(user)), user: toPublicUser(user) };
  }

  // Re-authentication shared by every MFA lifecycle change. A bearer JWT
  // proves this is the same session that logged in, possibly hours ago; it
  // does not prove the caller has the password or the device just now, and
  // a stolen token (sessionStorage read via XSS, a shared device, a leaked
  // log) was previously sufficient on its own to re-enroll or disable the
  // second factor. Every path here throws the same UnauthorizedException
  // shape regardless of which check failed, so a caller cannot tell a
  // wrong password from a missing/wrong current TOTP code.
  private async requireMfaStepUp(userId: string, password: string, totpCode?: string): Promise<User> {
    const user = await this.users.findById(userId);
    if (!user || user.status !== 'active') {
      throw new UnauthorizedException('re-authentication failed');
    }
    const validPassword = await this.users.verifyPassword(user, password);
    // When the account already has a second factor, proof of the
    // *current* one is required too -- a password alone (which an
    // attacker holding a stolen JWT might also have obtained) must not be
    // enough to replace or remove a factor that is already protecting
    // the account.
    const validTotp = !user.mfa_enabled || (!!user.mfa_secret && !!totpCode && verifyTotpCode(user.mfa_secret, totpCode));
    if (!validPassword || !validTotp) {
      throw new UnauthorizedException('re-authentication failed');
    }
    return user;
  }

  // Step 1 of MFA enrollment: generate a secret and enrollment URI, but do not
  // enable MFA yet (see confirmMfaEnrollment).
  async beginMfaEnrollment(userId: string, password: string, totpCode?: string): Promise<{ secret: string; enrollmentUri: string }> {
    const user = await this.requireMfaStepUp(userId, password, totpCode);
    const secret = generateMfaSecret();
    await this.users.setMfaSecret(userId, secret);
    return { secret, enrollmentUri: mfaEnrollmentUri(user.email, secret) };
  }

  async confirmMfaEnrollment(userId: string, code: string): Promise<void> {
    const user = await this.users.findById(userId);
    if (!user || !user.mfa_secret) {
      throw new UnauthorizedException('no pending MFA enrollment');
    }
    if (!verifyTotpCode(user.mfa_secret, code)) {
      throw new UnauthorizedException('invalid MFA code');
    }
    await this.users.enableMfa(userId);
  }

  async disableMfa(userId: string, password: string, totpCode?: string): Promise<void> {
    await this.requireMfaStepUp(userId, password, totpCode);
    await this.users.disableMfa(userId);
  }

  private async issueToken(user: User): Promise<{ accessToken: string; expiresIn: number }> {
    // jti identifies this specific token, so logout (below) can revoke
    // just it rather than needing to invalidate every token ever issued
    // to this user.
    const claims: JwtClaims = { sub: user.id, email: user.email, role: user.role, jti: uuid() };
    const accessToken = await this.jwt.signAsync(claims, { expiresIn: ACCESS_TOKEN_TTL_SECONDS });
    return { accessToken, expiresIn: ACCESS_TOKEN_TTL_SECONDS };
  }

  // Revokes the presented token early (AUTH-03). A captured bearer JWT
  // used to remain valid for its full hour-long TTL with no way to
  // invalidate it before then -- POST /v1/auth/logout and every
  // revoke/refresh route returned 404. claims.exp is the token's own
  // expiry; revoking for exactly the time remaining means the denylist
  // entry never outlives the token it blocks.
  async logout(claims: JwtClaims): Promise<void> {
    if (!claims.jti) return;
    const remaining = (claims.exp ?? 0) - Math.floor(Date.now() / 1000);
    await this.revocation?.revoke(claims.jti, remaining);
  }
}
