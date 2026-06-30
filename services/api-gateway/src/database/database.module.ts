import { Global, Module } from '@nestjs/common';
import { Pool } from 'pg';
import { PostgresService } from './postgres.service';
import { AuditService } from './audit.service';

// Provides a single shared PostgreSQL connection pool to the whole app, plus
// the transaction-metadata (PostgresService) and audit-trail (AuditService)
// repositories. Marked @Global so any module can inject these without re-importing.
export const PG_POOL = 'PG_POOL';

@Global()
@Module({
  providers: [
    {
      provide: PG_POOL,
      useFactory: () =>
        new Pool({
          connectionString:
            process.env.DATABASE_URL ??
            'postgresql://app:dev-only@localhost:5432/openfireblocks',
        }),
    },
    PostgresService,
    AuditService,
  ],
  exports: [PG_POOL, PostgresService, AuditService],
})
export class DatabaseModule {}
