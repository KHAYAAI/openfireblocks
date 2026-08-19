import { Pool } from 'pg';
import { PostgresService } from './postgres.service';

describe('PostgresService (key_pairs / dkg_ceremonies / signing_requests / key_shares)', () => {
  function svcWith(query: jest.Mock) {
    return new PostgresService({ query } as unknown as Pool);
  }

  it('scopes getKey to the given customer so one tenant cannot read another\'s key', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [{ key_id: 'k1' }] });
    await svcWith(query).getKey('k1', 'cust-1');
    const [sql, params] = query.mock.calls[0];
    expect(sql).toContain('customer_id = $2');
    expect(params).toEqual(['k1', 'cust-1']);
  });

  it('getKey without a customerId looks up by key alone (internal/admin use)', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [{ key_id: 'k1' }] });
    await svcWith(query).getKey('k1');
    expect(query.mock.calls[0][0]).not.toContain('customer_id');
  });

  it('returns null rather than throwing when a key is not found', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [] });
    await expect(svcWith(query).getKey('missing', 'cust-1')).resolves.toBeNull();
  });

  it('countSignaturesForKey counts only completed signing requests', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [{ count: 3 }] });
    const count = await svcWith(query).countSignaturesForKey('k1');
    expect(count).toBe(3);
    expect(query.mock.calls[0][0]).toContain("status = 'completed'");
  });

  it('getKeyShares lists shares ordered by party', async () => {
    const rows = [{ party_id: 1, status: 'sealed', backed_up_at: null }];
    const query = jest.fn().mockResolvedValue({ rows });
    await expect(svcWith(query).getKeyShares('k1')).resolves.toEqual(rows);
    expect(query.mock.calls[0][0]).toContain('ORDER BY party_id');
  });
});
