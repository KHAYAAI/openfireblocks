import { Inject, Injectable } from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from './database.module';

// Transaction metadata to persist alongside the immutable audit trail.
export interface TransactionRecord {
  requestId: string;
  customerId: string;
  chain: string;
  to: string;
  data: string;
  value: string;
  gasLimit: number;
  gasPrice: string;
  nonce: number;
  signedTx: string;
  txHash: string;
  status: string;
}

// Reads and writes rows in the signing.transactions table.
@Injectable()
export class PostgresService {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  // Upserts a transaction keyed by request_id so retries update in place.
  async saveTransaction(tx: TransactionRecord) {
    const query = `
      INSERT INTO signing.transactions (
        request_id, customer_id, chain, to_address, amount, data,
        gas_limit, gas_price, nonce, signed_tx, tx_hash, status
      ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
      ON CONFLICT (request_id)
      DO UPDATE SET
        signed_tx = EXCLUDED.signed_tx,
        tx_hash = EXCLUDED.tx_hash,
        status = EXCLUDED.status,
        updated_at = NOW()
    `;

    await this.pool.query(query, [
      tx.requestId,
      tx.customerId,
      tx.chain,
      tx.to,
      tx.value,
      tx.data,
      tx.gasLimit,
      tx.gasPrice,
      tx.nonce,
      tx.signedTx,
      tx.txHash,
      tx.status,
    ]);
  }

  // Updates only the lifecycle status (e.g. signed -> broadcasted -> confirmed).
  async updateStatus(requestId: string, status: string, txHash?: string) {
    const query = `
      UPDATE signing.transactions
      SET status = $2, tx_hash = COALESCE($3, tx_hash), updated_at = NOW()
      WHERE request_id = $1
    `;
    await this.pool.query(query, [requestId, status, txHash ?? null]);
  }

  // Fetches a transaction, scoped to a tenant when customerId is provided so a
  // customer can never read another tenant's transaction.
  async getTransaction(requestId: string, customerId?: string) {
    const result = customerId
      ? await this.pool.query(
          `SELECT * FROM signing.transactions WHERE request_id = $1 AND customer_id = $2`,
          [requestId, customerId],
        )
      : await this.pool.query(
          `SELECT * FROM signing.transactions WHERE request_id = $1`,
          [requestId],
        );
    return result.rows[0] ?? null;
  }

  // Lists a tenant's transactions, most recent first.
  async listTransactions(customerId: string, limit = 100) {
    const result = await this.pool.query(
      `SELECT * FROM signing.transactions
       WHERE customer_id = $1 ORDER BY id DESC LIMIT $2`,
      [customerId, limit],
    );
    return result.rows;
  }
}
