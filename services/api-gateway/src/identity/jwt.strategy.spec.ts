import { UnauthorizedException } from '@nestjs/common';
import { JwtAuthStrategy } from './jwt.strategy';
import { TokenRevocationService } from './token-revocation.service';

// AUTH-03 regression: a bearer JWT with a valid signature and an
// unexpired exp used to authenticate unconditionally. It must now also
// be checked against the revocation denylist, which is what lets a
// logged-out token actually stop working before it naturally expires.
describe('JwtAuthStrategy.validate', () => {
  const claims = { sub: 'u1', email: 'alice@example.com', role: 'user', jti: 'token-1' };

  function mockRevocation(isRevoked: boolean) {
    return { isRevoked: jest.fn().mockResolvedValue(isRevoked) } as unknown as TokenRevocationService;
  }

  it('accepts a well-formed, non-revoked token', async () => {
    const strategy = new JwtAuthStrategy(mockRevocation(false));
    await expect(strategy.validate(claims)).resolves.toEqual(claims);
  });

  it('rejects a token whose jti is on the revocation denylist', async () => {
    const strategy = new JwtAuthStrategy(mockRevocation(true));
    await expect(strategy.validate(claims)).rejects.toBeInstanceOf(UnauthorizedException);
  });

  it('rejects a malformed payload regardless of revocation state', async () => {
    const strategy = new JwtAuthStrategy(mockRevocation(false));
    await expect(strategy.validate({ sub: '', email: '', role: 'user' })).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
  });

  it('still works with no TokenRevocationService configured (degrades, does not crash)', async () => {
    const strategy = new JwtAuthStrategy(undefined);
    await expect(strategy.validate(claims)).resolves.toEqual(claims);
  });
});
