import { Pool } from 'pg';
import { PostgresService } from './postgres.service';

describe('PostgresService (key_pairs / dkg_ceremonies / signing_requests / key_shares)', () => {
  // Every tenant-scoped method now runs its real query inside a
  // BEGIN / SET LOCAL app.current_customer_id / ... / COMMIT transaction
  // (see withTenant in postgres.service.ts, added for row-level security --
  // migration 011). This mock captures that whole sequence on one fake
  // client and gives tests a way to pull out just the real query.
  function svcWith(query: jest.Mock) {
    const client = { query, release: jest.fn() };
    const pool = { connect: jest.fn().mockResolvedValue(client) };
    return { service: new PostgresService(pool as unknown as Pool), client };
  }

  // The real query is whichever call isn't BEGIN/set_config/COMMIT/ROLLBACK.
  function realQuery(query: jest.Mock) {
    const call = query.mock.calls.find(
      ([sql]) => !/^(BEGIN|COMMIT|ROLLBACK|SELECT set_config)/.test(sql),
    );
    if (!call) throw new Error('no real query found among mock calls');
    return call as [string, unknown[]];
  }

  it('scopes getKey to the given customer so one tenant cannot read another\'s key', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [{ key_id: 'k1' }] });
    const { service } = svcWith(query);
    await service.getKey('k1', 'cust-1');
    const [sql, params] = realQuery(query);
    expect(sql).toContain('customer_id = $2');
    expect(params).toEqual(['k1', 'cust-1']);
    // And the tenant context itself was set for this transaction.
    expect(
      query.mock.calls.some(([s, p]) => s.includes('set_config') && p?.[0] === 'cust-1'),
    ).toBe(true);
  });

  it('getKey without a customerId returns null without querying (no unscoped admin path through RLS)', async () => {
    const query = jest.fn();
    const { service } = svcWith(query);
    await expect(service.getKey('k1')).resolves.toBeNull();
    expect(query).not.toHaveBeenCalled();
  });

  it('returns null rather than throwing when a key is not found', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [] });
    const { service } = svcWith(query);
    await expect(service.getKey('missing', 'cust-1')).resolves.toBeNull();
  });

  it('countSignaturesForKey counts only completed signing requests, scoped to the tenant', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [{ count: 3 }] });
    const { service } = svcWith(query);
    const count = await service.countSignaturesForKey('k1', 'cust-1');
    expect(count).toBe(3);
    const [sql] = realQuery(query);
    expect(sql).toContain("status = 'completed'");
  });

  it('getKeyShares lists shares ordered by party, scoped to the tenant', async () => {
    const rows = [{ party_id: 1, status: 'sealed', backed_up_at: null }];
    const query = jest.fn().mockResolvedValue({ rows });
    const { service } = svcWith(query);
    await expect(service.getKeyShares('k1', 'cust-1')).resolves.toEqual(rows);
    const [sql] = realQuery(query);
    expect(sql).toContain('ORDER BY party_id');
  });

  it('rolls back and releases the client if the real query throws', async () => {
    const query = jest.fn().mockImplementation((sql: string) => {
      if (sql.startsWith('SELECT')) throw new Error('boom');
      return Promise.resolve({ rows: [] });
    });
    const client = { query, release: jest.fn() };
    const pool = { connect: jest.fn().mockResolvedValue(client) };
    const service = new PostgresService(pool as unknown as Pool);

    await expect(service.getKey('k1', 'cust-1')).rejects.toThrow('boom');
    expect(query.mock.calls.some(([s]) => s === 'ROLLBACK')).toBe(true);
    expect(client.release).toHaveBeenCalled();
  });
});
