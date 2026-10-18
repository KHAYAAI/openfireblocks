import { ServiceUnavailableException } from '@nestjs/common';
import { Pool } from 'pg';

// One transfer at a time per (chain, key address), from reading the nonce until the
// transaction is in the mempool. Two transfers prepared together would otherwise both read
// the same nonce, and the second would be rejected by the chain after it had been signed --
// and a signature is replayed by idempotency key, so it could not be redone.
//
// A Postgres advisory lock, so it holds across gateway replicas. Every path that reads a
// nonce for a key (approved transfers, agent payments) takes the same lock name, so they
// exclude each other too. Without a pool (unit tests) nothing is serialised and only the
// chain's own rejection protects against a duplicate nonce.
export async function withEvmNonceLock<T>(
  pool: Pool | null | undefined,
  chainId: number,
  address: string,
  fn: () => Promise<T>,
  waitMs = 60_000,
): Promise<T> {
  if (!pool) return fn();
  const lockKey = `evm-nonce:${chainId}:${address.toLowerCase()}`;
  const client = await pool.connect();
  try {
    const deadline = Date.now() + waitMs;
    for (;;) {
      const r = await client.query('SELECT pg_try_advisory_lock(hashtextextended($1, 0)) AS got', [lockKey]);
      if (r.rows[0].got) break;
      if (Date.now() > deadline) throw new ServiceUnavailableException('another transfer from this key is still in progress; try again shortly');
      await new Promise((res) => setTimeout(res, 100));
    }
    try { return await fn(); }
    finally { await client.query('SELECT pg_advisory_unlock(hashtextextended($1, 0))', [lockKey]).catch(() => undefined); }
  } finally { client.release(); }
}
