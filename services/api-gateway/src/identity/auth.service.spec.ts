import { ForbiddenException, UnauthorizedException } from '@nestjs/common';
import { AuthService } from './auth.service';
import { UsersService } from './users.service';
import { MfaChallengesService } from './mfa-challenges.service';
import { JwtService } from '@nestjs/jwt';

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

  it('rejects a locked account with 403 before even checking the password', async () => {
    const users = mockUsers({
      findByEmail: jest.fn().mockResolvedValue(activeUser),
      isLocked: jest.fn().mockReturnValue(true),
    });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await expect(auth.login(activeUser.email, 'x')).rejects.toBeInstanceOf(ForbiddenException);
    expect(users.verifyPassword).not.toHaveBeenCalled();
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

describe('AuthService MFA enrollment', () => {
  it('does not enable MFA until the enrollment code is confirmed', async () => {
    const users = mockUsers({ findById: jest.fn().mockResolvedValue(activeUser) });
    const auth = new AuthService(users, mockMfaChallenges(), jwt);
    await auth.beginMfaEnrollment('u1');
    expect(users.setMfaSecret).toHaveBeenCalled();
    expect(users.enableMfa).not.toHaveBeenCalled();
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
