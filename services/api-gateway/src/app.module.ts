import { Module } from '@nestjs/common';
import { APP_INTERCEPTOR, APP_GUARD } from '@nestjs/core';
import { ThrottlerModule, ThrottlerGuard } from '@nestjs/throttler';
import { AppController } from './app.controller';
import { SignModule } from './sign/sign.module';
import { DatabaseModule } from './database/database.module';
import { CustomersModule } from './customers/customers.module';
import { BillingModule } from './billing/billing.module';
import { MetricsModule } from './monitoring/metrics.module';
import { MetricsInterceptor } from './monitoring/metrics.interceptor';

// Root module. Phase 1 wires multi-tenancy (CustomersModule), Prometheus
// metrics (MetricsModule + global interceptor), the tenant-facing SignModule and
// shared database access. Phase 2 adds Temporal client + billing modules here.
@Module({
  imports: [
    // Per-IP rate limiting (default: 100 requests / 60s; override via env).
    ThrottlerModule.forRoot([
      {
        ttl: Number(process.env.THROTTLE_TTL_MS ?? 60000),
        limit: Number(process.env.THROTTLE_LIMIT ?? 100),
      },
    ]),
    DatabaseModule,
    MetricsModule,
    CustomersModule,
    BillingModule,
    SignModule,
  ],
  controllers: [AppController],
  providers: [
    { provide: APP_INTERCEPTOR, useClass: MetricsInterceptor },
    { provide: APP_GUARD, useClass: ThrottlerGuard },
  ],
})
export class AppModule {}
