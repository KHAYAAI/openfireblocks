import { Controller, Get, Inject, ServiceUnavailableException } from '@nestjs/common';
import { SkipThrottle } from '@nestjs/throttler';
import { Pool } from 'pg';
import { PG_POOL } from './database/database.module';

// Liveness + readiness endpoints used by Docker Compose and Kubernetes probes.
@Controller()
@SkipThrottle()
export class AppController {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  // Liveness: process is up.
  @Get('health')
  health() {
    return { status: 'ok', service: 'api-gateway' };
  }

  // Readiness: dependencies (PostgreSQL) are reachable. Returns 503 otherwise
  // so Kubernetes stops routing traffic to a pod that cannot serve.
  @Get('health/ready')
  async ready() {
    try {
      await this.pool.query('SELECT 1');
      return { status: 'ready', checks: { postgres: 'ok' } };
    } catch (err) {
      throw new ServiceUnavailableException({
        status: 'not-ready',
        checks: { postgres: (err as Error).message },
      });
    }
  }
}
