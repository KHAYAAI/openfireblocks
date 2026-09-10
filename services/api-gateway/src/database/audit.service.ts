import { Inject, Injectable, Logger } from '@nestjs/common';
import { Pool, PoolClient } from 'pg';
import { PG_POOL } from './pg-pool.token';

export interface AuditEvent {
  type: string;
  requestId: string;
  customerId?: string; // tenant the event belongs to; 'system' actor when absent
  message?: string;
  signature?: string;
  hash?: string;
  chain?: string;
  status: string;
  errorMessage?: string;
}

// Writes audit events into the audit.events table. This is the PostgreSQL
// half of the dual audit trail; the MPC signer additionally records events in
// immudb for cryptographic tamper-proofing.
//
// audit.events has row-level security enforced (migration 012): a write or
// read only applies to whichever customer_id the current transaction set via
// `SET LOCAL app.current_customer_id`, exactly like PostgresService. A
// customerId-less event (event.customerId undefined) has nowhere to get a
// tenant context from, so it's written through app_admin's bypass instead of
// the tenant-scoped `app` pool -- see adminPool below. Real callers
// (SignService) always pass a customerId, so this path is a genuine "system
// actor, no tenant" fallback, not the common case.
@Injectable()
export class AuditService {
  private readonly logger = new Logger(AuditService.name);
  private readonly adminPool: Pool;

  constructor(@Inject(PG_POOL) private readonly pool: Pool) {
    this.adminPool = new Pool({
      connectionString:
        process.env.DATABASE_ADMIN_URL ?? 'postgresql://app_admin:dev-only@localhost:5432/openfireblocks',
    });
  }

  private async withTenant<T>(
    customerId: string,
    fn: (client: PoolClient) => Promise<T>,
  ): Promise<T> {
    const client = await this.pool.connect();
    try {
      await client.query('BEGIN');
      // set_config(), not SET LOCAL, because SET does not accept bind
      // parameters -- see the identical comment in postgres.service.ts.
      await client.query("SELECT set_config('app.current_customer_id', $1, true)", [customerId]);
      const result = await fn(client);
      await client.query('COMMIT');
      return result;
    } catch (err) {
      await client.query('ROLLBACK').catch(() => undefined);
      throw err;
    } finally {
      client.release();
    }
  }

  // Returns the inserted row id, or null if the write failed (auditing must
  // never block the signing path on its own failure).
  async logEvent(event: AuditEvent): Promise<number | null> {
    const query = `
      INSERT INTO audit.events (
        event_type, actor, customer_id, request_id, message, signature, hash, chain, status, error_message
      ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
      RETURNING id
    `;
    const params = [
      event.type,
      event.customerId ?? 'system',
      event.customerId ?? null,
      event.requestId,
      event.message ?? null,
      event.signature ?? null,
      event.hash ?? null,
      event.chain ?? 'ethereum',
      event.status,
      event.errorMessage ?? null,
    ];

    try {
      const result = event.customerId
        ? await this.withTenant(event.customerId, (client) => client.query(query, params))
        : await this.adminPool.query(query, params);
      return result.rows[0].id;
    } catch (err) {
      this.logger.error(
        `failed to write audit event ${event.type} for ${event.requestId}: ${
          (err as Error).message
        }`,
      );
      return null;
    }
  }

  async getAuditTrail(requestId: string, customerId?: string) {
    if (!customerId) {
      return [];
    }
    const result = await this.withTenant(customerId, (client) =>
      client.query(
        `SELECT * FROM audit.events WHERE request_id = $1 AND customer_id = $2 ORDER BY timestamp ASC`,
        [requestId, customerId],
      ),
    );
    return result.rows;
  }
}
