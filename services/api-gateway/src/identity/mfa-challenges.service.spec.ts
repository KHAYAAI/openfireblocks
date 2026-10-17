import { Pool } from 'pg';
import { MfaChallengesService } from './mfa-challenges.service';

describe('MfaChallengesService', () => {
  function svcWith(query: jest.Mock) {
    return new MfaChallengesService({ query } as unknown as Pool);
  }

  it('stores only the hash of the challenge token, never the plaintext', async () => {
    const query = jest.fn().mockResolvedValue({ rows: [] });
    const token = await svcWith(query).create('u1');

    const [sql, params] = query.mock.calls[0];
    expect(sql).toContain('INSERT INTO mfa_challenges');
    expect(params[0]).toBe('u1');
    expect(Buffer.isBuffer(params[1])).toBe(true);
    expect(params[1].toString('hex')).not.toBe(token); // hash, not plaintext
    expect(token).toMatch(/^[0-9a-f]{64}$/); // 32 random bytes, hex-encoded
  });

  it('consume() returns true only when a matching, unexpired, unused row was updated', async () => {
    const query = jest.fn().mockResolvedValue({ rowCount: 1 });
    const ok = await svcWith(query).consume('u1', 'token');
    expect(ok).toBe(true);
    expect(query.mock.calls[0][0]).toContain('consumed_at IS NULL');
  });

  it('consume() returns false when nothing matched (expired, wrong user, or reused)', async () => {
    const query = jest.fn().mockResolvedValue({ rowCount: 0 });
    const ok = await svcWith(query).consume('u1', 'stale-or-reused-token');
    expect(ok).toBe(false);
  });
});
