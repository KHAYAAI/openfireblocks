import { Module } from '@nestjs/common';
import { APP_INTERCEPTOR } from '@nestjs/core';
import { AppController } from './app.controller';
import { SignModule } from './sign/sign.module';
import { DatabaseModule } from './database/database.module';
import { CustomersModule } from './customers/customers.module';
import { MetricsModule } from './monitoring/metrics.module';
import { MetricsInterceptor } from './monitoring/metrics.interceptor';

// Root module. Phase 1 wires multi-tenancy (CustomersModule), Prometheus
// metrics (MetricsModule + global interceptor), the tenant-facing SignModule and
// shared database access. Phase 2 adds Temporal client + billing modules here.
@Module({
  imports: [
    DatabaseModule,
    MetricsModule,
    CustomersModule,
    SignModule,
  ],
  controllers: [AppController],
  providers: [{ provide: APP_INTERCEPTOR, useClass: MetricsInterceptor }],
})
export class AppModule {}
