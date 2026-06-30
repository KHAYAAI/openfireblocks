import { UnauthorizedException, ExecutionContext } from '@nestjs/common';
import { AdminGuard } from './admin.guard';

function ctxWith(headers: Record<string, string>): ExecutionContext {
  return {
    switchToHttp: () => ({ getRequest: () => ({ headers }) }),
  } as unknown as ExecutionContext;
}

describe('AdminGuard', () => {
  const guard = new AdminGuard();
  const original = process.env.ADMIN_API_KEY;

  afterEach(() => {
    if (original === undefined) delete process.env.ADMIN_API_KEY;
    else process.env.ADMIN_API_KEY = original;
  });

  it('fails closed when ADMIN_API_KEY is unset', () => {
    delete process.env.ADMIN_API_KEY;
    expect(() => guard.canActivate(ctxWith({ authorization: 'Bearer x' }))).toThrow(
      UnauthorizedException,
    );
  });

  it('rejects a wrong admin token', () => {
    process.env.ADMIN_API_KEY = 'secret';
    expect(() =>
      guard.canActivate(ctxWith({ authorization: 'Bearer wrong' })),
    ).toThrow(UnauthorizedException);
  });

  it('accepts the correct admin token (Bearer)', () => {
    process.env.ADMIN_API_KEY = 'secret';
    expect(guard.canActivate(ctxWith({ authorization: 'Bearer secret' }))).toBe(
      true,
    );
  });

  it('accepts the correct admin token (X-Admin-Key)', () => {
    process.env.ADMIN_API_KEY = 'secret';
    expect(guard.canActivate(ctxWith({ 'x-admin-key': 'secret' }))).toBe(true);
  });
});
