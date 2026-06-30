import { Inject, Injectable, Logger, OnModuleDestroy } from '@nestjs/common';
import type { Redis } from 'ioredis';
import { REDIS_CLIENT } from './risk.module';

export interface VelocityDecision {
  allowed: boolean;
  count: number;
  limit: number;
  reason?: string;
}

// Minimal Redis surface the risk control needs (keeps it unit-testable).
export interface RiskRedis {
  incr(key: string): Promise<number>;
  expire(key: string, seconds: number): Promise<unknown>;
  quit(): Promise<unknown>;
}

// Per-tenant velocity limits (transactions per rolling hour) by tier.
const TIER_HOURLY_LIMITS: Record<string, number> = {
  free: 10,
  pro: 100,
  enterprise: 1000,
};

// Velocity / rate-of-spend risk control backed by Redis. Counts a tenant's
// transactions in the current hour window and denies once the tier limit is hit.
//
// Behaviour:
//   - no Redis client  → disabled (no-op, always allowed).
//   - Redis error      → fail-open (allow) with a warning, so a Redis outage
//     degrades the control rather than halting all signing. Set
//     RISK_FAIL_CLOSED=true to fail closed instead.
@Injectable()
export class RiskService implements OnModuleDestroy {
  private readonly logger = new Logger(RiskService.name);
  private readonly failClosed = process.env.RISK_FAIL_CLOSED === 'true';

  constructor(
    @Inject(REDIS_CLIENT) private readonly redis: RiskRedis | null,
  ) {
    if (!redis) {
      this.logger.warn('REDIS_URL not set; velocity limiting disabled');
    }
  }

  limitForTier(tier: string): number {
    return TIER_HOURLY_LIMITS[tier] ?? TIER_HOURLY_LIMITS.free;
  }

  // Atomically increments and checks the tenant's hourly counter.
  async checkAndRecord(customerId: string, tier: string): Promise<VelocityDecision> {
    const limit = this.limitForTier(tier);
    if (!this.redis) {
      return { allowed: true, count: 0, limit };
    }

    const hourBucket = Math.floor(Date.now() / 3_600_000);
    const key = `risk:velocity:${customerId}:${hourBucket}`;

    try {
      const count = await this.redis.incr(key);
      if (count === 1) {
        // First write in this window: expire after just over an hour.
        await this.redis.expire(key, 3700);
      }
      if (count > limit) {
        return {
          allowed: false,
          count,
          limit,
          reason: `velocity limit exceeded: ${count - 1}/${limit} transactions this hour for tier ${tier}`,
        };
      }
      return { allowed: true, count, limit };
    } catch (err) {
      this.logger.error(`velocity check failed: ${(err as Error).message}`);
      if (this.failClosed) {
        return {
          allowed: false,
          count: -1,
          limit,
          reason: 'risk service unavailable (fail-closed)',
        };
      }
      return { allowed: true, count: -1, limit };
    }
  }

  async onModuleDestroy() {
    await (this.redis as Redis | null)?.quit().catch(() => undefined);
  }
}
