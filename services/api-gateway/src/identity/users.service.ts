import { ConflictException, Inject, Injectable } from '@nestjs/common';
import { Pool } from 'pg';
import * as bcrypt from 'bcrypt';
import { PG_POOL } from '../database/pg-pool.token';

export interface User {
  id: string;
  email: string;
  password_hash: string;
  full_name: string;
  role: string;
  status: string;
  mfa_secret: string | null;
  mfa_enabled: boolean;
  failed_login_count: number;
  locked_until: string | null;
  last_login_at: string | null;
}

const BCRYPT_COST = 12;
const MAX_FAILED_LOGINS = 5;
const LOCKOUT_MINUTES = 15;

// Human dashboard users, distinct from the API-key tenants in CustomerService.
// Passwords are bcrypt-hashed (cost 12); the plaintext never reaches the database.
@Injectable()
export class UsersService {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  async create(input: {
    email: string;
    password: string;
    fullName: string;
    role?: string;
  }): Promise<User> {
    const existing = await this.findByEmail(input.email);
    if (existing) {
      throw new ConflictException('an account with this email already exists');
    }
    const passwordHash = await bcrypt.hash(input.password, BCRYPT_COST);
    const result = await this.pool.query(
      `INSERT INTO users (email, password_hash, full_name, role)
       VALUES ($1, $2, $3, $4)
       RETURNING *`,
      [input.email.toLowerCase(), passwordHash, input.fullName, input.role ?? 'user'],
    );
    return result.rows[0];
  }

  async findByEmail(email: string): Promise<User | null> {
    const result = await this.pool.query(`SELECT * FROM users WHERE email = $1`, [
      email.toLowerCase(),
    ]);
    return result.rows[0] ?? null;
  }

  async findById(id: string): Promise<User | null> {
    const result = await this.pool.query(`SELECT * FROM users WHERE id = $1`, [id]);
    return result.rows[0] ?? null;
  }

  verifyPassword(user: User, password: string): Promise<boolean> {
    return bcrypt.compare(password, user.password_hash);
  }

  isLocked(user: User): boolean {
    return !!user.locked_until && new Date(user.locked_until).getTime() > Date.now();
  }

  // Locks the account for LOCKOUT_MINUTES after MAX_FAILED_LOGINS consecutive
  // failures, then resets the counter, so a lockout always requires a fresh
  // run of failures rather than accumulating forever.
  async recordFailedLogin(userId: string): Promise<void> {
    const result = await this.pool.query(
      `UPDATE users SET failed_login_count = failed_login_count + 1
       WHERE id = $1 RETURNING failed_login_count`,
      [userId],
    );
    const count = result.rows[0]?.failed_login_count ?? 0;
    if (count >= MAX_FAILED_LOGINS) {
      await this.pool.query(
        `UPDATE users
         SET locked_until = NOW() + make_interval(mins => $2), failed_login_count = 0
         WHERE id = $1`,
        [userId, LOCKOUT_MINUTES],
      );
    }
  }

  async recordSuccessfulLogin(userId: string): Promise<void> {
    await this.pool.query(
      `UPDATE users SET failed_login_count = 0, last_login_at = NOW() WHERE id = $1`,
      [userId],
    );
  }

  async setMfaSecret(userId: string, secret: string): Promise<void> {
    await this.pool.query(`UPDATE users SET mfa_secret = $2 WHERE id = $1`, [userId, secret]);
  }

  async enableMfa(userId: string): Promise<void> {
    await this.pool.query(`UPDATE users SET mfa_enabled = TRUE WHERE id = $1`, [userId]);
  }

  async disableMfa(userId: string): Promise<void> {
    await this.pool.query(`UPDATE users SET mfa_enabled = FALSE, mfa_secret = NULL WHERE id = $1`, [
      userId,
    ]);
  }
}
