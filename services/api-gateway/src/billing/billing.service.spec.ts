import { Pool } from 'pg';
import { BillingService } from './billing.service';

describe('BillingService', () => {
  function svcWith(query: jest.Mock) {
    return new BillingService({ query } as unknown as Pool);
  }

  it('records a signed transaction for the current month', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [] });
    await svcWith(query).recordSigned('demo');

    const [sql, params] = query.mock.calls[0];
    expect(sql).toContain('signed_count');
    expect(params[0]).toBe('demo');
    expect(params[1]).toMatch(/^\d{4}-\d{2}$/); // YYYY-MM period
  });

  it('records a broadcast on the broadcast_count column', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [] });
    await svcWith(query).recordBroadcast('demo');
    expect(query.mock.calls[0][0]).toContain('broadcast_count');
  });

  it('never throws if metering fails (best-effort)', async () => {
    const query = jest.fn().mockRejectedValue(new Error('db down'));
    await expect(svcWith(query).recordSigned('demo')).resolves.toBeUndefined();
  });

  it('reads usage rows for a tenant', async () => {
    const rows = [{ period: '2026-06', signed_count: 5, broadcast_count: 2 }];
    const query = jest.fn().mockResolvedValue({ rows });
    const out = await svcWith(query).getUsage('demo');
    expect(out).toEqual(rows);
    expect(query.mock.calls[0][1]).toEqual(['demo']);
  });
});
