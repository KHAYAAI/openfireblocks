import { Inject, Injectable, Logger } from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';

// Meters per-tenant usage (signed + broadcast transaction counts) per calendar
// month. This is the data layer a billing engine (Kill Bill) consumes to drive
// metered invoicing. Metering never blocks the signing path: failures are logged.
@Injectable()
export class BillingService {
  private readonly logger = new Logger(BillingService.name);

  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  private period(date = new Date()): string {
    return date.toISOString().slice(0, 7); // YYYY-MM
  }

  async recordSigned(customerId: string): Promise<void> {
    await this.increment(customerId, 'signed_count');
  }

  async recordBroadcast(customerId: string): Promise<void> {
    await this.increment(customerId, 'broadcast_count');
  }

  // column is an internal constant (never user input), safe to interpolate.
  private async increment(
    customerId: string,
    column: 'signed_count' | 'broadcast_count',
  ): Promise<void> {
    const query = `
      INSERT INTO billing.usage (customer_id, period, ${column})
      VALUES ($1, $2, 1)
      ON CONFLICT (customer_id, period)
      DO UPDATE SET ${column} = billing.usage.${column} + 1, updated_at = NOW()
    `;
    try {
      await this.pool.query(query, [customerId, this.period()]);
    } catch (err) {
      this.logger.error(
        `usage metering failed for ${customerId}/${column}: ${
          (err as Error).message
        }`,
      );
    }
  }

  async getUsage(customerId: string) {
    const result = await this.pool.query(
      `SELECT period, signed_count, broadcast_count, updated_at
       FROM billing.usage WHERE customer_id = $1 ORDER BY period DESC`,
      [customerId],
    );
    return result.rows;
  }
}
