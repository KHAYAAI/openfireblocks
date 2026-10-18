import { Pool, PoolClient } from 'pg';

// Runs fn in a transaction with the tenant's id set for row-level
// security, the same way PostgresService.withTenant does. set_config with
// is_local=true scopes it to this transaction, so a pooled connection
// never carries one tenant's id into the next request.
export async function withTenant<T>(
  pool: Pool,
  customerId: string,
  fn: (client: PoolClient) => Promise<T>,
): Promise<T> {
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    await client.query("SELECT set_config('app.current_customer_id', $1, true)", [customerId]);
    const out = await fn(client);
    await client.query('COMMIT');
    return out;
  } catch (err) {
    await client.query('ROLLBACK').catch(() => undefined);
    throw err;
  } finally {
    client.release();
  }
}

export const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
