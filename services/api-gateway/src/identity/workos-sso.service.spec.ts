import { JwtService } from '@nestjs/jwt';
import { ServiceUnavailableException, UnauthorizedException } from '@nestjs/common';
import { WorkosSsoService } from './workos-sso.service';
import { UsersService, User } from './users.service';

const passwordUser: User = {
  id: 'user-1',
  email: 'existing@corp.example',
  password_hash: '$2b$12$hash',
  full_name: 'Existing User',
  auth_provider: 'password',
  workos_user_id: null,
  workos_organization_id: null,
  role: 'user',
  status: 'active',
  mfa_secret: null,
  mfa_enabled: false,
  failed_login_count: 0,
  locked_until: null,
  last_login_at: null,
};

function makeService(users: Partial<UsersService>) {
  const jwt = new JwtService({ secret: 'test-secret' });
  return new WorkosSsoService(users as UsersService, jwt);
}

describe('WorkosSsoService', () => {
  const originalEnv = { ...process.env };

  afterEach(() => {
    process.env = { ...originalEnv };
  });

  describe('configuration', () => {
    it('reports unconfigured and fails closed when WorkOS env vars are absent', () => {
      delete process.env.WORKOS_API_KEY;
      delete process.env.WORKOS_CLIENT_ID;
      const service = makeService({});
      expect(service.isConfigured()).toBe(false);
      expect(() => service.authorizationUrl()).toThrow(ServiceUnavailableException);
    });

    it('reports configured once both credentials are present', () => {
      process.env.WORKOS_API_KEY = 'sk_test';
      process.env.WORKOS_CLIENT_ID = 'client_test';
      expect(makeService({}).isConfigured()).toBe(true);
    });
  });

  // The state parameter is the OAuth CSRF defense -- without a valid one,
  // an attacker can feed a victim their own authorization code and log the
  // victim into the attacker's account.
  describe('state validation', () => {
    it('accepts a state it just issued', () => {
      const service = makeService({});
      expect(() => service.verifyState(service.createState())).not.toThrow();
    });

    it('rejects a missing, malformed, or tampered state', () => {
      const service = makeService({});
      expect(() => service.verifyState(undefined)).toThrow(UnauthorizedException);
      expect(() => service.verifyState('not-a-state')).toThrow(UnauthorizedException);

      const valid = service.createState();
      const [nonce, issuedAt] = valid.split('.');
      const forged = `${nonce}.${issuedAt}.${'0'.repeat(64)}`;
      expect(() => service.verifyState(forged)).toThrow(UnauthorizedException);
    });

    it('rejects a state signed with a different secret', () => {
      process.env.JWT_SECRET = 'secret-a';
      const stateFromA = makeService({}).createState();
      process.env.JWT_SECRET = 'secret-b';
      expect(() => makeService({}).verifyState(stateFromA)).toThrow(UnauthorizedException);
    });

    it('rejects an expired state', () => {
      const service = makeService({});
      const state = service.createState();
      const [nonce, , mac] = state.split('.');
      const stale = `${nonce}.${Date.now() - 60 * 60 * 1000}.${mac}`;
      expect(() => service.verifyState(stale)).toThrow(UnauthorizedException);
    });
  });

  // resolveLocalUser is private; exercised through completeLogin with the
  // WorkOS code exchange stubbed, since the account-linking rule is the
  // security-relevant part and must not silently regress.
  describe('account linking on first SSO login', () => {
    function serviceWithStubbedExchange(
      users: Partial<UsersService>,
      workosUser: { id: string; email: string; emailVerified: boolean; name?: string },
    ) {
      process.env.WORKOS_API_KEY = 'sk_test';
      process.env.WORKOS_CLIENT_ID = 'client_test';
      process.env.WORKOS_REDIRECT_URI = 'https://api.example/auth/sso/callback';
      const service = makeService(users);
      // Stub the WorkOS client so no network call happens; everything after
      // the exchange is this service's own logic, which is what's under test.
      (service as unknown as { workos: () => unknown }).workos = () => ({
        userManagement: {
          authenticateWithCode: async () => ({
            user: {
              id: workosUser.id,
              email: workosUser.email,
              emailVerified: workosUser.emailVerified,
              name: workosUser.name ?? null,
              firstName: null,
              lastName: null,
            },
            organizationId: 'org_123',
          }),
        },
      });
      return service;
    }

    it('provisions a new local user when nothing matches', async () => {
      const createSsoUser = jest.fn().mockResolvedValue({
        ...passwordUser,
        id: 'user-new',
        email: 'new@corp.example',
        password_hash: null,
        auth_provider: 'workos_sso',
        workos_user_id: 'workos_user_new',
      });
      const service = serviceWithStubbedExchange(
        {
          findByWorkosUserId: jest.fn().mockResolvedValue(null),
          findByEmail: jest.fn().mockResolvedValue(null),
          createSsoUser,
          recordSuccessfulLogin: jest.fn().mockResolvedValue(undefined),
        },
        { id: 'workos_user_new', email: 'new@corp.example', emailVerified: true },
      );

      const result = await service.completeLogin('code_1', service.createState());
      expect(createSsoUser).toHaveBeenCalledWith(
        expect.objectContaining({ email: 'new@corp.example', workosUserId: 'workos_user_new' }),
      );
      expect(result.accessToken).toBeTruthy();
      expect(result.user.email).toBe('new@corp.example');
    });

    it('links an existing password account only when the IdP verified the email', async () => {
      const linkWorkosIdentity = jest
        .fn()
        .mockResolvedValue({ ...passwordUser, workos_user_id: 'workos_user_2' });
      const service = serviceWithStubbedExchange(
        {
          findByWorkosUserId: jest.fn().mockResolvedValue(null),
          findByEmail: jest.fn().mockResolvedValue(passwordUser),
          linkWorkosIdentity,
          recordSuccessfulLogin: jest.fn().mockResolvedValue(undefined),
        },
        { id: 'workos_user_2', email: passwordUser.email, emailVerified: true },
      );

      await service.completeLogin('code_2', service.createState());
      expect(linkWorkosIdentity).toHaveBeenCalledWith(
        passwordUser.id,
        expect.objectContaining({ workosUserId: 'workos_user_2' }),
      );
    });

    // The takeover case: anyone who can make an IdP assert an arbitrary
    // address must not thereby inherit an existing account.
    it('refuses to link an existing account when the email is unverified', async () => {
      const linkWorkosIdentity = jest.fn();
      const service = serviceWithStubbedExchange(
        {
          findByWorkosUserId: jest.fn().mockResolvedValue(null),
          findByEmail: jest.fn().mockResolvedValue(passwordUser),
          linkWorkosIdentity,
          recordSuccessfulLogin: jest.fn().mockResolvedValue(undefined),
        },
        { id: 'workos_user_3', email: passwordUser.email, emailVerified: false },
      );

      await expect(service.completeLogin('code_3', service.createState())).rejects.toBeInstanceOf(
        UnauthorizedException,
      );
      expect(linkWorkosIdentity).not.toHaveBeenCalled();
    });

    it('refuses login for a suspended account', async () => {
      const service = serviceWithStubbedExchange(
        {
          findByWorkosUserId: jest
            .fn()
            .mockResolvedValue({ ...passwordUser, status: 'suspended' }),
          recordSuccessfulLogin: jest.fn(),
        },
        { id: 'workos_user_4', email: passwordUser.email, emailVerified: true },
      );

      await expect(service.completeLogin('code_4', service.createState())).rejects.toBeInstanceOf(
        UnauthorizedException,
      );
    });
  });
});
