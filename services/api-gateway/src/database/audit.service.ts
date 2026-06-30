import { Inject, Injectable, Logger } from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from './database.module';

export interface AuditEvent {
  type: string;
  requestId: string;
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
@Injectable()
export class AuditService {
  private readonly logger = new Logger(AuditService.name);

  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  // Returns the inserted row id, or null if the write failed (auditing must
  // never block the signing path on its own failure).
  async logEvent(event: AuditEvent): Promise<number | null> {
    const query = `
      INSERT INTO audit.events (
        event_type, actor, request_id, message, signature, hash, chain, status, error_message
      ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
      RETURNING id
    `;

    try {
      const result = await this.pool.query(query, [
        event.type,
        'system', // Phase 1 replaces this with the authenticated customer id
        event.requestId,
        event.message ?? null,
        event.signature ?? null,
        event.hash ?? null,
        event.chain ?? 'ethereum',
        event.status,
        event.errorMessage ?? null,
      ]);
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

  async getAuditTrail(requestId: string) {
    const result = await this.pool.query(
      `SELECT * FROM audit.events WHERE request_id = $1 ORDER BY timestamp ASC`,
      [requestId],
    );
    return result.rows;
  }
}
