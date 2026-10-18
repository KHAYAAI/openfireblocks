import { CustomerService } from './customer.service';
import { hashApiKey } from '../auth/api-key.util';

// AUTH-01 (round 2): a tenant's API key had no expiry and no rotation, so a
// captured key worked forever and the only remedy was suspending the tenant.
describe('CustomerService API key lifecycle', () => {
  const oldTtl = process.env.API_KEY_TTL_DAYS;
  afterEach(() => {
    if (oldTtl === undefined) delete process.env.API_KEY_TTL_DAYS;
    else process.env.API_KEY_TTL_DAYS = oldTtl;
  });

  function build(rows: unknown[] = [{ customer_id: 'c1', api_key_expires_at: null }]) {
    const service = new CustomerService();
    const query = jest.fn().mockResolvedValue({ rows });
    (service as any).pool = { query };
    return { service, query };
  }

  it('looks keys up with an expiry condition, so an expired key authenticates nothing', async () => {
    const { service, query } = build([]);
    await service.getByApiKey('ofb_abc');
    const sql = query.mock.calls[0][0] as string;
    expect(sql).toContain('api_key_expires_at IS NULL OR api_key_expires_at > NOW()');
    expect(sql).toContain("status = 'active'");
  });

  it('rotation overwrites the stored hash with the new key and returns it once', async () => {
    const { service, query } = build();
    const out = await service.rotateApiKey('c1');
    expect(out.api_key).toMatch(/^ofb_[0-9a-f]{48}$/);
    const updateCall = query.mock.calls.find((c) => String(c[0]).includes('UPDATE customers'))!;
    expect(updateCall[1][1]).toBe(hashApiKey(out.api_key)); // the new key's hash replaces the old
    expect(updateCall[1][2]).toBeNull(); // no expiry unless asked for
  });

  it('rotation can set an expiry, and a deployment default applies when none is given', async () => {
    const { service, query } = build();
    await service.rotateApiKey('c1', 30);
    expect(query.mock.calls.find((c) => String(c[0]).includes('UPDATE customers'))![1][2]).toBe(30);

    process.env.API_KEY_TTL_DAYS = '90';
    const second = build();
    await second.service.rotateApiKey('c1');
    expect(second.query.mock.calls.find((c) => String(c[0]).includes('UPDATE customers'))![1][2]).toBe(90);
  });

  it('two rotations never produce the same key', async () => {
    const { service } = build();
    const a = await service.rotateApiKey('c1');
    const b = await service.rotateApiKey('c1');
    expect(a.api_key).not.toBe(b.api_key);
  });
});
