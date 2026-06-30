import { Injectable, Logger, OnModuleDestroy } from '@nestjs/common';
import Redis from 'ioredis';

export interface VelocityDecision {
  allowed: boolean;
  count: number;
  limit: number;
  reason?: string;
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
//   - REDIS_URL unset  → disabled (no-op, always allowed). Logged once.
//   - Redis error      → fail-open (allow) with a warning + metric, so a Redis
//     outage degrades the control rather than halting all signing. For
//     high-assurance deployments set RISK_FAIL_CLOSED=true.
@Injectable()
export class RiskService implements OnModuleDestroy {
  private readonly logger = new Logger(RiskService.name);
  private readonly redis: Redis | null;
  private readonly failClosed = process.env.RISK_FAIL_CLOSED === 'true';

  constructor() {
    const url = process.env.REDIS_URL;
    if (!url) {
      this.logger.warn('REDIS_URL not set; velocity limiting disabled');
      this.redis = null;
    } else {
      this.redis = new Redis(url, { lazyConnect: true, maxRetriesPerRequest: 1 });
      this.redis.connect().catch((err) =>
        this.logger.error(`redis connect failed: ${err.message}`),
      );
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
    if (this.redis) {
      await this.redis.quit().catch(() => undefined);
    }
  }
}
