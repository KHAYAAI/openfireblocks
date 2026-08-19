import {
  ForbiddenException,
  Injectable,
  UnauthorizedException,
} from '@nestjs/common';
import { JwtService } from '@nestjs/jwt';
import { UsersService, User } from './users.service';
import { MfaChallengesService } from './mfa-challenges.service';
import {
  generateMfaSecret,
  mfaEnrollmentUri,
  verifyTotpCode,
} from './mfa.util';

export interface JwtClaims {
  sub: string;
  email: string;
  role: string;
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
  ) {}

  async register(input: { email: string; password: string; fullName: string }): Promise<PublicUser> {
    const user = await this.users.create(input);
    return toPublicUser(user);
  }

  async login(email: string, password: string): Promise<LoginResult> {
    const user = await this.users.findByEmail(email);
    // Constant-shape failure: whether the email exists or the password is
    // wrong, the caller sees the same 401 so login can't enumerate accounts.
    if (!user || user.status !== 'active') {
      throw new UnauthorizedException('invalid credentials');
    }
    if (this.users.isLocked(user)) {
      throw new ForbiddenException('account temporarily locked; try again later');
    }

    const valid = await this.users.verifyPassword(user, password);
    if (!valid) {
      await this.users.recordFailedLogin(user.id);
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

  // Step 1 of MFA enrollment: generate a secret and enrollment URI, but do not
  // enable MFA yet (see confirmMfaEnrollment).
  async beginMfaEnrollment(userId: string): Promise<{ secret: string; enrollmentUri: string }> {
    const user = await this.users.findById(userId);
    if (!user) throw new UnauthorizedException();
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

  async disableMfa(userId: string): Promise<void> {
    await this.users.disableMfa(userId);
  }

  private async issueToken(user: User): Promise<{ accessToken: string; expiresIn: number }> {
    const claims: JwtClaims = { sub: user.id, email: user.email, role: user.role };
    const accessToken = await this.jwt.signAsync(claims, { expiresIn: ACCESS_TOKEN_TTL_SECONDS });
    return { accessToken, expiresIn: ACCESS_TOKEN_TTL_SECONDS };
  }
}
