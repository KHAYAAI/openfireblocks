import { ConflictException, UnauthorizedException } from '@nestjs/common';
import { AuthService } from './auth.service';
import { UsersService } from './users.service';
import { MfaChallengesService } from './mfa-challenges.service';
import { JwtService } from '@nestjs/jwt';
import { authenticator } from 'otplib';

function mockUsers(overrides: Partial<jest.Mocked<UsersService>> = {}) {
  return {
    create: jest.fn(),
    findByEmail: jest.fn(),
    findById: jest.fn(),
    verifyPassword: jest.fn(),
    isLocked: jest.fn().mockReturnValue(false),
    recordFailedLogin: jest.fn(),
    recordSuccessfulLogin: jest.fn(),
    setMfaSecret: jest.fn(),
    enableMfa: jest.fn(),
    disableMfa: jest.fn(),
    ...overrides,
  } as unknown as jest.Mocked<UsersService>;
}

function mockMfaChallenges(overrides: Partial<jest.Mocked<MfaChallengesService>> = {}) {
  return {
    create: jest.fn().mockResolvedValue('challenge-token'),
    consume: jest.fn().mockResolvedValue(true),
    ...overrides,
  } as unknown as jest.Mocked<MfaChallengesService>;
}

const jwt = new JwtService({ secret: 'test-secret' });

const activeUser = {
  id: 'u1',
  email: 'alice@example.com',
  password_hash: 'hash',
  full_name: 'Alice',
  role: 'user',
  status: 'active',
  mfa_secret: null,
  mfa_enabled: false,
  failed_login_count: 0,
  locked_until: null,
  last_login_at: null,
};

describe('AuthService.login', () => {
  it('rejects an unknown email with the same error as a wrong password (no enumeration)', async () => {
    const users = mockUsers({ findByEmail: jest.fn().mockResolvedValue(null) });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(auth.login('nobody@example.com', 'x')).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
  });

  // AUTH-02 regression: a locked account used to throw a distinct
  // ForbiddenException ("account temporarily locked"), which told an
  // unauthenticated caller the account exists and just failed five
  // logins. It must now be indistinguishable from any other failed
  // login -- same exception, same message -- even when the supplied
  // password is actually correct.
  it('rejects a locked account the same way as a wrong password, even with the right password', async () => {
    const users = mockUsers({
      findByEmail: jest.fn().mockResolvedValue(activeUser),
      isLocked: jest.fn().mockReturnValue(true),
      verifyPassword: jest.fn().mockResolvedValue(true),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(auth.login(activeUser.email, 'right')).rejects.toBeInstanceOf(UnauthorizedException);
    await expect(auth.login(activeUser.email, 'right')).rejects.toThrow('invalid credentials');
    // Already locked: does not also record a new failed attempt.
    expect(users.recordFailedLogin).not.toHaveBeenCalled();
  });

  it('does not enumerate accounts by response type: unknown email, wrong password and a locked account all throw the same error', async () => {
    const unknown = mockUsers({ findByEmail: jest.fn().mockResolvedValue(null) });
    const wrongPassword = mockUsers({
      findByEmail: jest.fn().mockResolvedValue(activeUser),
      verifyPassword: jest.fn().mockResolvedValue(false),
    });
    const locked = mockUsers({
      findByEmail: jest.fn().mockResolvedValue(activeUser),
      isLocked: jest.fn().mockReturnValue(true),
      verifyPassword: jest.fn().mockResolvedValue(true),
    });

    for (const users of [unknown, wrongPassword, locked]) {
      const auth = new AuthService(users, mockMfaChallenges(), jwt);
      const promise = auth.login('whoever@example.com', 'x');
      await expect(promise).rejects.toBeInstanceOf(UnauthorizedException);
      await expect(promise).rejects.toThrow('invalid credentials');
    }
  });

  it('records a failed login and rejects on wrong password', async () => {
    const users = mockUsers({
      findByEmail: jest.fn().mockResolvedValue(activeUser),
      verifyPassword: jest.fn().mockResolvedValue(false),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(auth.login(activeUser.email, 'wrong')).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
    expect(users.recordFailedLogin).toHaveBeenCalledWith('u1');
  });

  it('issues a JWT directly when MFA is not enabled', async () => {
    const users = mockUsers({
      findByEmail: jest.fn().mockResolvedValue(activeUser),
      verifyPassword: jest.fn().mockResolvedValue(true),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    const result = await auth.login(activeUser.email, 'right');
    expect(result.status).toBe('ok');
    if (result.status === 'ok') {
      expect(result.accessToken).toEqual(expect.any(String));
      expect(users.recordSuccessfulLogin).toHaveBeenCalledWith('u1');
    }
  });

  it('returns a challenge token instead of a JWT when MFA is enabled', async () => {
    const mfaUser = { ...activeUser, mfa_enabled: true, mfa_secret: 'SECRET' };
    const users = mockUsers({
      findByEmail: jest.fn().mockResolvedValue(mfaUser),
      verifyPassword: jest.fn().mockResolvedValue(true),
    });
    const mfaChallenges = mockMfaChallenges();
    const auth = new AuthService(users, mfaChallenges, jwt);
    const result = await auth.login(mfaUser.email, 'right');
    expect(result).toEqual({ status: 'mfa_required', challengeToken: 'challenge-token' });
    expect(mfaChallenges.create).toHaveBeenCalledWith('u1');
    // No JWT-bearing fields leaked into an mfa_required response.
    expect(result).not.toHaveProperty('accessToken');
  });
});

describe('AuthService.verifyMfaAndLogin', () => {
  const mfaUser = { ...activeUser, mfa_enabled: true, mfa_secret: 'BADSECRETBUTFIXEDFORTEST' };

  it('rejects when the challenge token has already been consumed or is unknown', async () => {
    const users = mockUsers({ findByEmail: jest.fn().mockResolvedValue(mfaUser) });
    const mfaChallenges = mockMfaChallenges({ consume: jest.fn().mockResolvedValue(false) });
    const auth = new AuthService(users, mfaChallenges, jwt);
    await expect(auth.verifyMfaAndLogin(mfaUser.email, 'stale-token', '000000')).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
  });

  it('rejects an invalid TOTP code and records it as a failed login', async () => {
    const users = mockUsers({ findByEmail: jest.fn().mockResolvedValue(mfaUser) });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(
      auth.verifyMfaAndLogin(mfaUser.email, 'token', '000000'),
    ).rejects.toBeInstanceOf(UnauthorizedException);
    expect(users.recordFailedLogin).toHaveBeenCalledWith('u1');
  });

  it('rejects for a user without MFA enabled, even with a valid-looking request', async () => {
    const users = mockUsers({ findByEmail: jest.fn().mockResolvedValue(activeUser) });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(
      auth.verifyMfaAndLogin(activeUser.email, 'token', '123456'),
    ).rejects.toBeInstanceOf(UnauthorizedException);
  });
});

// Regression coverage for AUTH-05: registration used to return 409 with
// "an account with this email already exists" for a registered email and
// 201 for a fresh one -- an unauthenticated account-existence oracle.
describe('AuthService.register', () => {
  it('returns the same shape as a real registration when the email already exists', async () => {
    const users = mockUsers({
      create: jest.fn().mockRejectedValue(new ConflictException('an account with this email already exists')),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    const result = await auth.register({
      email: 'taken@example.com',
      password: 'Str0ng!Passw0rd#2026',
      fullName: 'Someone',
    });
    expect(result).toMatchObject({
      email: 'taken@example.com',
      fullName: 'Someone',
      role: 'user',
      mfaEnabled: false,
    });
    expect(result.id).toEqual(expect.any(String));
  });

  it('propagates an error that is not a conflict', async () => {
    const users = mockUsers({ create: jest.fn().mockRejectedValue(new Error('db is down')) });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(
      auth.register({ email: 'x@example.com', password: 'x', fullName: 'X' }),
    ).rejects.toThrow('db is down');
  });

  it('still returns the real created user on success', async () => {
    const createdUser = {
      id: 'new-id',
      email: 'fresh@example.com',
      password_hash: 'hash',
      full_name: 'Fresh',
      role: 'user',
      status: 'active',
      mfa_secret: null,
      mfa_enabled: false,
      failed_login_count: 0,
      locked_until: null,
      last_login_at: null,
    };
    const users = mockUsers({ create: jest.fn().mockResolvedValue(createdUser) });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    const result = await auth.register({
      email: 'fresh@example.com',
      password: 'x',
      fullName: 'Fresh',
    });
    expect(result.id).toBe('new-id');
  });
});

// AUTH-01 regression: these endpoints used to require only a valid bearer
// JWT. An attacker holding a stolen/leaked token could re-enroll the
// victim's second factor to a secret of their own and later disable MFA
// entirely, with no password and no current TOTP code at any point.
describe('AuthService MFA step-up (AUTH-01)', () => {
  it('does not enable MFA until the enrollment code is confirmed', async () => {
    const users = mockUsers({
      findById: jest.fn().mockResolvedValue(activeUser),
      verifyPassword: jest.fn().mockResolvedValue(true),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await auth.beginMfaEnrollment('u1', 'correct-password');
    expect(users.setMfaSecret).toHaveBeenCalled();
    expect(users.enableMfa).not.toHaveBeenCalled();
  });

  it('refuses to begin enrollment with a bearer JWT alone -- a correct password is required', async () => {
    const users = mockUsers({
      findById: jest.fn().mockResolvedValue(activeUser),
      verifyPassword: jest.fn().mockResolvedValue(false),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(auth.beginMfaEnrollment('u1', 'wrong-password')).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
    expect(users.setMfaSecret).not.toHaveBeenCalled();
  });

  it('requires a valid current TOTP code to re-enroll when MFA is already enabled, even with the correct password', async () => {
    const mfaUser = { ...activeUser, mfa_enabled: true, mfa_secret: 'REALSECRETXXXXXXXXXX' };
    const users = mockUsers({
      findById: jest.fn().mockResolvedValue(mfaUser),
      verifyPassword: jest.fn().mockResolvedValue(true),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(auth.beginMfaEnrollment('u1', 'correct-password')).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
    await expect(auth.beginMfaEnrollment('u1', 'correct-password', '000000')).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
    expect(users.setMfaSecret).not.toHaveBeenCalled();
  });

  it('refuses to disable MFA with a bearer JWT alone -- password and current TOTP are both required', async () => {
    const mfaUser = { ...activeUser, mfa_enabled: true, mfa_secret: 'REALSECRETXXXXXXXXXX' };
    const users = mockUsers({
      findById: jest.fn().mockResolvedValue(mfaUser),
      verifyPassword: jest.fn().mockResolvedValue(true),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    // Correct password, but no TOTP code: still refused.
    await expect(auth.disableMfa('u1', 'correct-password')).rejects.toBeInstanceOf(UnauthorizedException);
    expect(users.disableMfa).not.toHaveBeenCalled();
  });

  it('disables MFA once password and a valid current TOTP code are both supplied', async () => {
    const secret = authenticator.generateSecret();
    const mfaUser = { ...activeUser, mfa_enabled: true, mfa_secret: secret };
    const users = mockUsers({
      findById: jest.fn().mockResolvedValue(mfaUser),
      verifyPassword: jest.fn().mockResolvedValue(true),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await auth.disableMfa('u1', 'correct-password', authenticator.generate(secret));
    expect(users.disableMfa).toHaveBeenCalledWith('u1');
  });

  it('rejects confirmation with a wrong code and never enables MFA', async () => {
    const users = mockUsers({
      findById: jest.fn().mockResolvedValue({ ...activeUser, mfa_secret: 'REALSECRETXXXXXXXXXX' }),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(auth.confirmMfaEnrollment('u1', '000000')).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
    expect(users.enableMfa).not.toHaveBeenCalled();
  });
});

// AUTH-03 regression: a captured bearer JWT used to remain valid for its
// full TTL with no way to invalidate it early -- every revocation route
// returned 404. login() now gives every token a unique jti, and logout()
// revokes it for exactly the time it had left.
describe('AuthService token issuance and logout (AUTH-03)', () => {
  it('issues a token with a unique jti on every login', async () => {
    const users = mockUsers({
      findByEmail: jest.fn().mockResolvedValue(activeUser),
      verifyPassword: jest.fn().mockResolvedValue(true),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    const first = await auth.login(activeUser.email, 'right');
    const second = await auth.login(activeUser.email, 'right');
    if (first.status !== 'ok' || second.status !== 'ok') throw new Error('expected ok');
    const decode = (token: string) => JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString());
    const jti1 = decode(first.accessToken).jti;
    const jti2 = decode(second.accessToken).jti;
    expect(jti1).toEqual(expect.any(String));
    expect(jti1).not.toBe(jti2);
  });

  it('revokes the token for exactly its remaining lifetime', async () => {
    const revoke = jest.fn().mockResolvedValue(undefined);
    const revocation = { revoke } as unknown as import('./token-revocation.service').TokenRevocationService;
    const auth = new AuthService(mockUsers(), mockMfaChallenges(), jwt, revocation);
    const now = Math.floor(Date.now() / 1000);
    await auth.logout({ sub: 'u1', email: 'alice@example.com', role: 'user', jti: 'tok-1', exp: now + 42 });
    expect(revoke).toHaveBeenCalledWith('tok-1', expect.any(Number));
    expect(revoke.mock.calls[0][1]).toBeGreaterThan(40);
    expect(revoke.mock.calls[0][1]).toBeLessThanOrEqual(42);
  });

  it('does nothing for a token with no jti (issued before this existed)', async () => {
    const revoke = jest.fn();
    const revocation = { revoke } as unknown as import('./token-revocation.service').TokenRevocationService;
    const auth = new AuthService(mockUsers(), mockMfaChallenges(), jwt, revocation);
    await auth.logout({ sub: 'u1', email: 'alice@example.com', role: 'user' });
    expect(revoke).not.toHaveBeenCalled();
  });
});
