import { Module } from '@nestjs/common';
import Redis from 'ioredis';
import { RiskService } from './risk.service';

// Token for the (optional) Redis client backing velocity limits.
export const REDIS_CLIENT = 'REDIS_CLIENT';

// Velocity / risk controls. The Redis client is created from REDIS_URL, or null
// when unset (velocity limiting disabled). Injecting it keeps RiskService
// unit-testable with a fake client.
@Module({
  providers: [
    {
      provide: REDIS_CLIENT,
      useFactory: () => {
        const url = process.env.REDIS_URL;
        if (!url) return null;
        const client = new Redis(url, {
          lazyConnect: true,
          maxRetriesPerRequest: 1,
        });
        client.connect().catch(() => undefined);
        return client;
      },
    },
    RiskService,
  ],
  exports: [RiskService],
})
export class RiskModule {}
