import { DashboardController } from './dashboard.controller';
import { issueSession } from './session';
import type { CustomerService } from '../customers/customer.service';
import type { PostgresService } from '../database/postgres.service';
import type { KeysService } from '../keys/keys.service';
import type { DashboardService } from './dashboard.service';
import type { TokenRevocationService } from '../identity/token-revocation.service';

// AUTH-04 regression: ofb_session used to be a purely self-contained
// bearer cookie with no server-side record at all, so a captured cookie
// kept authenticating for the remainder of its 8-hour TTL regardless of
// anything the victim did -- sign-out only cleared the browser's own
// copy, and the exact same cookie value replayed right back in still
// worked. Sign-out must now revoke it server-side, and a revoked session
// must stop authenticating immediately, even though its signature and
// expiry are both still perfectly valid.
describe('DashboardController session revocation (AUTH-04)', () => {
  const customerId = '11111111-1111-4111-8111-111111111111';

  function fakeReq(cookie?: string) {
    return { headers: { cookie } } as any;
  }

  function fakeRes() {
    const res: any = {
      headers: {} as Record<string, string>,
      redirectedTo: undefined as string | undefined,
      setHeader(name: string, value: string) {
        this.headers[name] = value;
      },
      redirect(...args: any[]) {
        this.redirectedTo = args[args.length - 1];
        return this;
      },
      statusCode: undefined as number | undefined,
      status(code: number) {
        this.statusCode = code;
        return this;
      },
      send() {
        return this;
      },
    };
    return res;
  }

  function build(isRevoked: boolean) {
    const customers = {
      getByCustomerId: jest.fn().mockResolvedValue({ customer_id: customerId, name: 'demo', tier: 'pro' }),
    } as unknown as jest.Mocked<CustomerService>;
    const revocation = {
      isRevoked: jest.fn().mockResolvedValue(isRevoked),
      revoke: jest.fn().mockResolvedValue(undefined),
    } as unknown as jest.Mocked<TokenRevocationService>;
    const controller = new DashboardController(
      customers,
      {} as PostgresService,
      {} as KeysService,
      {} as DashboardService,
      revocation,
    );
    return { controller, customers, revocation };
  }

  it('authenticates a session that is not revoked', async () => {
    const { controller } = build(false);
    const { cookie } = issueSession(customerId);
    const res = fakeRes();
    await controller.root(fakeReq(cookie.split(';')[0]), res);
    expect(res.redirectedTo).toBe('/dashboard/overview');
  });

  it('treats a revoked session as signed out, even with a valid signature and expiry', async () => {
    const { controller } = build(true);
    const { cookie } = issueSession(customerId);
    const res = fakeRes();
    await controller.root(fakeReq(cookie.split(';')[0]), res);
    expect(res.redirectedTo).toBe('/dashboard/sign-in');
  });

  it('sign-out revokes the session server-side, not just the browser cookie', async () => {
    const { controller, revocation } = build(false);
    const { cookie } = issueSession(customerId);
    const res = fakeRes();
    await controller.signOut(fakeReq(cookie.split(';')[0]), res);
    expect(revocation.revoke).toHaveBeenCalledWith(expect.any(String), expect.any(Number));
    expect(res.headers['Set-Cookie']).toContain('Max-Age=0');
  });

  it('sign-out with no session cookie does not call revoke', async () => {
    const { controller, revocation } = build(false);
    const res = fakeRes();
    await controller.signOut(fakeReq(undefined), res);
    expect(revocation.revoke).not.toHaveBeenCalled();
  });

  // AUTHZ-02 (round 2): sign-out used to be a GET, so any request a third
  // party could get a signed-in browser to make would revoke the session.
  it('a GET to sign-out revokes nothing and only redirects', async () => {
    const { controller, revocation } = build(false);
    const res = fakeRes();
    controller.signOutPage(res);
    expect(revocation.revoke).not.toHaveBeenCalled();
    expect(res.redirectedTo).toBe('/dashboard/overview');
  });

  it('refuses a sign-out POST that comes from another origin', async () => {
    const { controller, revocation } = build(false);
    const { cookie } = issueSession(customerId);
    const res = fakeRes();
    const req: any = { headers: { cookie: cookie.split(';')[0], origin: 'https://evil.example', host: 'staging.example' } };
    await controller.signOut(req, res);
    expect(res.statusCode).toBe(403);
    expect(revocation.revoke).not.toHaveBeenCalled();
  });

  it('accepts a same-origin sign-out POST', async () => {
    const { controller, revocation } = build(false);
    const { cookie } = issueSession(customerId);
    const res = fakeRes();
    const req: any = { headers: { cookie: cookie.split(';')[0], origin: 'https://staging.example', host: 'staging.example' } };
    await controller.signOut(req, res);
    expect(revocation.revoke).toHaveBeenCalled();
  });

  // AUTH-01 (round 3): this exchange of an API key for a session cookie had
  // only the global 100/min limit, ten times the JSON login's.
  it('rate-limits the API-key sign-in to the same budget as the JSON login', () => {
    const limit = Reflect.getMetadata('THROTTLER:LIMITdefault', DashboardController.prototype.signIn);
    const ttl = Reflect.getMetadata('THROTTLER:TTLdefault', DashboardController.prototype.signIn);
    expect(limit).toBe(10);
    expect(ttl).toBe(60_000);
  });
});
