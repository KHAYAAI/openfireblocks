import { RiskService, RiskRedis } from './risk.service';

// Fake Redis that counts incr calls per key, mimicking a fresh hourly window.
class FakeRedis implements RiskRedis {
  counts = new Map<string, number>();
  expires: Array<[string, number]> = [];
  async incr(key: string): Promise<number> {
    const n = (this.counts.get(key) ?? 0) + 1;
    this.counts.set(key, n);
    return n;
  }
  async expire(key: string, seconds: number): Promise<unknown> {
    this.expires.push([key, seconds]);
    return 1;
  }
  async quit(): Promise<unknown> {
    return 'OK';
  }
}

describe('RiskService', () => {
  it('is a no-op (always allowed) without a Redis client', async () => {
    const svc = new RiskService(null);
    const d = await svc.checkAndRecord('demo', 'free');
    expect(d.allowed).toBe(true);
  });

  it('allows up to the tier limit then denies', async () => {
    const redis = new FakeRedis();
    const svc = new RiskService(redis);

    // free tier = 10/hour. Calls 1..10 allowed, 11th denied.
    let last;
    for (let i = 0; i < 10; i++) {
      last = await svc.checkAndRecord('demo', 'free');
      expect(last.allowed).toBe(true);
    }
    const denied = await svc.checkAndRecord('demo', 'free');
    expect(denied.allowed).toBe(false);
    expect(denied.limit).toBe(10);
    expect(denied.reason).toMatch(/velocity limit exceeded/);

    // Expiry set exactly once for the window (on the first increment).
    expect(redis.expires.length).toBe(1);
  });

  it('uses the higher enterprise limit', async () => {
    const svc = new RiskService(new FakeRedis());
    expect(svc.limitForTier('enterprise')).toBe(1000);
    expect(svc.limitForTier('unknown-tier')).toBe(10); // defaults to free
  });

  it('fails open on Redis errors by default', async () => {
    const boom: RiskRedis = {
      incr: async () => {
        throw new Error('redis down');
      },
      expire: async () => 1,
      quit: async () => 'OK',
    };
    const svc = new RiskService(boom);
    const d = await svc.checkAndRecord('demo', 'pro');
    expect(d.allowed).toBe(true); // degraded, not blocked
  });
});
