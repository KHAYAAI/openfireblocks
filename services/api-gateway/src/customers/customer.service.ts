import { Inject, Injectable, NotFoundException } from '@nestjs/common';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { PG_POOL } from '../database/pg-pool.token';
import { generateApiKey, hashApiKey } from '../auth/api-key.util';

export interface Customer {
  id: number;
  customer_id: string;
  email: string;
  api_key: string;
  status: string;
  tier: string;
  policies: Record<string, unknown>;
}

// Manages tenants: creation, API-key lookup (used by the auth guard) and
// per-tenant policy overrides.
@Injectable()
export class CustomerService {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  // Creates a tenant. The plaintext API key is returned exactly once (as
  // `api_key`); only its SHA-256 hash is persisted.
  async createCustomer(input: {
    customerId?: string;
    email: string;
    tier?: string;
  }): Promise<Customer> {
    const customerId = input.customerId ?? randomUUID();
    const plaintextKey = generateApiKey();

    const result = await this.pool.query(
      `INSERT INTO customers (customer_id, email, api_key, status, tier)
       VALUES ($1, $2, $3, 'active', $4)
       RETURNING id, customer_id, email, status, tier, policies`,
      [customerId, input.email, hashApiKey(plaintextKey), input.tier ?? 'free'],
    );
    // Surface the plaintext key to the caller; it is never retrievable again.
    return { ...result.rows[0], api_key: plaintextKey };
  }

  // Looks up an active customer by API key (hashed before lookup). Returns null
  // when missing/suspended so the auth guard can reject without leaking which.
  async getByApiKey(apiKey: string): Promise<Customer | null> {
    const result = await this.pool.query(
      `SELECT id, customer_id, email, api_key, status, tier, policies
       FROM customers WHERE api_key = $1 AND status = 'active'`,
      [hashApiKey(apiKey)],
    );
    return result.rows[0] ?? null;
  }

  async getByCustomerId(customerId: string): Promise<Customer> {
    const result = await this.pool.query(
      `SELECT id, customer_id, email, api_key, status, tier, policies
       FROM customers WHERE customer_id = $1`,
      [customerId],
    );
    if (!result.rows[0]) {
      throw new NotFoundException(`customer ${customerId} not found`);
    }
    return result.rows[0];
  }

  async list(): Promise<Omit<Customer, 'api_key'>[]> {
    const result = await this.pool.query(
      `SELECT id, customer_id, email, status, tier, policies
       FROM customers ORDER BY id`,
    );
    return result.rows;
  }

  async updatePolicies(customerId: string, policies: Record<string, unknown>) {
    await this.getByCustomerId(customerId); // 404 if missing
    await this.pool.query(
      `UPDATE customers SET policies = $1, updated_at = NOW() WHERE customer_id = $2`,
      [JSON.stringify(policies), customerId],
    );
  }

  async setStatus(customerId: string, status: string) {
    await this.getByCustomerId(customerId);
    await this.pool.query(
      `UPDATE customers SET status = $1, updated_at = NOW() WHERE customer_id = $2`,
      [status, customerId],
    );
  }
}
