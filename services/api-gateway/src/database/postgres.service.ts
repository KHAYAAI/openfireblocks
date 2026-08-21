import { Inject, Injectable } from '@nestjs/common';
import { Pool, PoolClient } from 'pg';
import { PG_POOL } from './pg-pool.token';

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

// Reads and writes rows in the signing.transactions / key_pairs /
// dkg_ceremonies / signing_requests / key_shares tables. Every one of
// these tables has row-level security enforced (migration 011/012): a
// query only sees rows for whichever customer_id the current transaction
// set via `SET LOCAL app.current_customer_id`. withTenant() below is how
// every tenant-scoped method here does that -- there is no way to
// accidentally read another tenant's row through this class.
@Injectable()
export class PostgresService {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  // Runs fn against a single pooled connection inside a transaction with
  // app.current_customer_id set to customerId for that transaction's
  // duration. SET LOCAL is transaction-scoped by design (resets on
  // COMMIT/ROLLBACK), so it can never leak onto a later request that
  // happens to reuse the same physical pooled connection.
  private async withTenant<T>(
    customerId: string,
    fn: (client: PoolClient) => Promise<T>,
  ): Promise<T> {
    const client = await this.pool.connect();
    try {
      await client.query('BEGIN');
      // SET/SET LOCAL do not accept bind parameters ($1) in Postgres --
      // only set_config() does, whose third argument (true) makes it
      // transaction-local exactly like SET LOCAL. Using string
      // interpolation here instead would reopen the SQL-injection hole
      // this parameterization exists to close.
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

    await this.withTenant(tx.customerId, (client) =>
      client.query(query, [
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
      ]),
    );
  }

  // Updates only the lifecycle status (e.g. signed -> broadcasted -> confirmed).
  // customerId is required (not optional) because RLS needs it to scope the
  // UPDATE -- every real caller (SignService) already has it in hand from the
  // sign request it's updating.
  async updateStatus(requestId: string, customerId: string, status: string, txHash?: string) {
    const query = `
      UPDATE signing.transactions
      SET status = $2, tx_hash = COALESCE($3, tx_hash), updated_at = NOW()
      WHERE request_id = $1
    `;
    await this.withTenant(customerId, (client) =>
      client.query(query, [requestId, status, txHash ?? null]),
    );
  }

  // Fetches a transaction, scoped to a tenant when customerId is provided so a
  // customer can never read another tenant's transaction. Without a
  // customerId, RLS's default (no session variable set) means this returns
  // nothing rather than falling back to an unscoped admin lookup -- there is
  // no admin bypass available through this pool's role.
  async getTransaction(requestId: string, customerId?: string) {
    if (!customerId) {
      return null;
    }
    const result = await this.withTenant(customerId, (client) =>
      client.query(
        `SELECT * FROM signing.transactions WHERE request_id = $1 AND customer_id = $2`,
        [requestId, customerId],
      ),
    );
    return result.rows[0] ?? null;
  }

  // Lists a tenant's transactions, most recent first.
  async listTransactions(customerId: string, limit = 100) {
    const result = await this.withTenant(customerId, (client) =>
      client.query(
        `SELECT * FROM signing.transactions
         WHERE customer_id = $1 ORDER BY id DESC LIMIT $2`,
        [customerId, limit],
      ),
    );
    return result.rows;
  }

  // -- key_pairs / dkg_ceremonies / signing_requests / key_shares (KeysService) --

  async createKey(key: {
    key_id: string;
    customer_id: string;
    name: string;
    blockchain: string;
    threshold: number;
    total_parties: number;
    status: string;
  }) {
    await this.withTenant(key.customer_id, (client) =>
      client.query(
        `INSERT INTO key_pairs (key_id, customer_id, name, blockchain, threshold, total_parties, status)
         VALUES ($1, $2, $3, $4, $5, $6, $7)`,
        [
          key.key_id,
          key.customer_id,
          key.name,
          key.blockchain,
          key.threshold,
          key.total_parties,
          key.status,
        ],
      ),
    );
  }

  async listKeys(customerId: string) {
    const result = await this.withTenant(customerId, (client) =>
      client.query(
        `SELECT key_id, name, blockchain, threshold, total_parties, address, public_key, status, created_at
         FROM key_pairs WHERE customer_id = $1 ORDER BY created_at DESC`,
        [customerId],
      ),
    );
    return result.rows;
  }

  // Scoped to customerId (when provided) so a tenant can never read another
  // tenant's key, mirroring getTransaction's tenant-scoping pattern above.
  async getKey(keyId: string, customerId?: string) {
    if (!customerId) {
      return null;
    }
    const result = await this.withTenant(customerId, (client) =>
      client.query(`SELECT * FROM key_pairs WHERE key_id = $1 AND customer_id = $2`, [
        keyId,
        customerId,
      ]),
    );
    return result.rows[0] ?? null;
  }

  // The four methods below now take an explicit customerId (they didn't
  // before RLS) -- every real caller already has the customer in hand
  // from the authenticated request, since these all hang off a specific
  // tenant's key.
  async getCeremoniesForKey(keyId: string, customerId: string) {
    const result = await this.withTenant(customerId, (client) =>
      client.query(
        `SELECT ceremony_id, status, current_round, started_at, completed_at, error_message
         FROM dkg_ceremonies WHERE key_id = $1 ORDER BY started_at DESC`,
        [keyId],
      ),
    );
    return result.rows;
  }

  async getSigningRequestsForKey(keyId: string, customerId: string, limit = 50) {
    const result = await this.withTenant(customerId, (client) =>
      client.query(
        `SELECT request_id, status, blockchain, transaction_hash, created_at, completed_at
         FROM signing_requests WHERE key_id = $1 ORDER BY created_at DESC LIMIT $2`,
        [keyId, limit],
      ),
    );
    return result.rows;
  }

  async countSignaturesForKey(keyId: string, customerId: string): Promise<number> {
    const result = await this.withTenant(customerId, (client) =>
      client.query(
        `SELECT COUNT(*)::int AS count FROM signing_requests WHERE key_id = $1 AND status = 'completed'`,
        [keyId],
      ),
    );
    return result.rows[0]?.count ?? 0;
  }

  async getKeyShares(keyId: string, customerId: string) {
    const result = await this.withTenant(customerId, (client) =>
      client.query(
        `SELECT party_id, status, backed_up_at FROM key_shares WHERE key_id = $1 ORDER BY party_id`,
        [keyId],
      ),
    );
    return result.rows;
  }
}
