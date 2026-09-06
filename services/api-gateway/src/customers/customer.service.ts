import { Injectable, NotFoundException } from '@nestjs/common';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { generateApiKey, hashApiKey } from '../auth/api-key.util';

export interface Customer {
  customer_id: string;
  name: string;
  email: string | null;
  status: string;
  tier: string;
  policies: Record<string, unknown>;
  // Only ever populated on createCustomer's return value -- the plaintext
  // key is shown exactly once and never stored or read back.
  api_key?: string;
}

// Manages tenants: creation, API-key lookup (used by the auth guard), and
// per-tenant policy overrides.
//
// Runs on its own app_admin (BYPASSRLS) connection, not the shared
// RLS-scoped PG_POOL PostgresService uses: customers itself has FORCED
// row-level security (migration 011, policy "tenant_isolation" scoped by
// customer_id), and getByApiKey's entire job is to discover which
// customer_id a request belongs to BEFORE any tenant context exists to
// scope it by -- through the RLS-scoped `app` role, with no
// app.current_customer_id set yet, that query can never return a row,
// for any key, ever (verified directly: `SELECT * FROM customers` as
// `app` returns zero rows regardless of how many exist, since the
// session has no current_customer_id to match the policy against).
// Mirrors AuditService's identical DATABASE_ADMIN_URL pattern for its own
// customerId-less writes, and the resolveCustomerIDForX admin-pool
// convention services/settlement, /billing, /webhooks, /marketplace use
// in Go for exactly the same "must work without a tenant context yet"
// reason.
@Injectable()
export class CustomerService {
  private readonly pool: Pool;

  constructor() {
    this.pool = new Pool({
      connectionString:
        process.env.DATABASE_ADMIN_URL ??
        'postgresql://app_admin:dev-only@localhost:5432/openfireblocks',
    });
  }

  // Creates a tenant. The plaintext API key is returned exactly once (as
  // `api_key`); only its SHA-256 hash is persisted (in api_key_hash,
  // stored as bytea -- hashApiKey returns a hex string, decoded on the
  // way in).
  async createCustomer(input: {
    customerId?: string;
    name?: string;
    email: string;
    tier?: string;
  }): Promise<Customer> {
    const customerId = input.customerId ?? randomUUID();
    const plaintextKey = generateApiKey();
    const hashHex = hashApiKey(plaintextKey);

    const result = await this.pool.query(
      `INSERT INTO customers (customer_id, name, email, api_key_hash, status, tier)
       VALUES ($1::uuid, $2, $3, decode($4, 'hex'), 'active', $5)
       RETURNING customer_id, name, email, status, tier, policies`,
      [customerId, input.name ?? input.email, input.email, hashHex, input.tier ?? 'free'],
    );
    return { ...result.rows[0], api_key: plaintextKey };
  }

  // Looks up an active customer by API key (hashed before lookup). Returns null
  // when missing/suspended so the auth guard can reject without leaking which.
  async getByApiKey(apiKey: string): Promise<Customer | null> {
    const result = await this.pool.query(
      `SELECT customer_id, name, email, status, tier, policies
       FROM customers WHERE api_key_hash = decode($1, 'hex') AND status = 'active'`,
      [hashApiKey(apiKey)],
    );
    return result.rows[0] ?? null;
  }

  async getByCustomerId(customerId: string): Promise<Customer> {
    const result = await this.pool.query(
      `SELECT customer_id, name, email, status, tier, policies
       FROM customers WHERE customer_id = $1::uuid`,
      [customerId],
    );
    if (!result.rows[0]) {
      throw new NotFoundException(`customer ${customerId} not found`);
    }
    return result.rows[0];
  }

  async list(): Promise<Customer[]> {
    const result = await this.pool.query(
      `SELECT customer_id, name, email, status, tier, policies
       FROM customers ORDER BY created_at`,
    );
    return result.rows;
  }

  async updatePolicies(customerId: string, policies: Record<string, unknown>) {
    await this.getByCustomerId(customerId); // 404 if missing
    await this.pool.query(
      `UPDATE customers SET policies = $1, updated_at = NOW() WHERE customer_id = $2::uuid`,
      [JSON.stringify(policies), customerId],
    );
  }

  async setStatus(customerId: string, status: string) {
    await this.getByCustomerId(customerId);
    await this.pool.query(
      `UPDATE customers SET status = $1, updated_at = NOW() WHERE customer_id = $2::uuid`,
      [status, customerId],
    );
  }
}
