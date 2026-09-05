import { UnauthorizedException, ExecutionContext } from '@nestjs/common';
import { ApiKeyGuard } from './api-key.guard';
import { CustomerService, Customer } from '../customers/customer.service';

// Builds a minimal ExecutionContext wrapping a fake request with the given headers.
function ctxWith(headers: Record<string, string>): {
  ctx: ExecutionContext;
  req: any;
} {
  const req: any = { headers };
  const ctx = {
    switchToHttp: () => ({ getRequest: () => req }),
  } as unknown as ExecutionContext;
  return { ctx, req };
}

const activeCustomer: Customer = {
  customer_id: 'demo',
  name: 'demo',
  email: 'demo@x.io',
  api_key: 'hash',
  status: 'active',
  tier: 'pro',
  policies: {},
};

describe('ApiKeyGuard', () => {
  const makeGuard = (lookup: jest.Mock) =>
    new ApiKeyGuard({ getByApiKey: lookup } as unknown as CustomerService);

  it('rejects when no API key is present', async () => {
    const guard = makeGuard(jest.fn());
    const { ctx } = ctxWith({});
    await expect(guard.canActivate(ctx)).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
  });

  it('rejects an unknown/suspended key', async () => {
    const lookup = jest.fn().mockResolvedValue(null);
    const guard = makeGuard(lookup);
    const { ctx } = ctxWith({ authorization: 'Bearer nope' });
    await expect(guard.canActivate(ctx)).rejects.toBeInstanceOf(
      UnauthorizedException,
    );
    expect(lookup).toHaveBeenCalledWith('nope');
  });

  it('accepts a valid Bearer key and attaches the customer', async () => {
    const guard = makeGuard(jest.fn().mockResolvedValue(activeCustomer));
    const { ctx, req } = ctxWith({ authorization: 'Bearer good' });
    await expect(guard.canActivate(ctx)).resolves.toBe(true);
    expect(req.customer).toBe(activeCustomer);
  });

  it('accepts a valid X-API-Key header', async () => {
    const guard = makeGuard(jest.fn().mockResolvedValue(activeCustomer));
    const { ctx, req } = ctxWith({ 'x-api-key': 'good' });
    await expect(guard.canActivate(ctx)).resolves.toBe(true);
    expect(req.customer).toBe(activeCustomer);
  });
});
