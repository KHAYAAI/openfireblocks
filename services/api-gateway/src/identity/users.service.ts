import { ConflictException, Inject, Injectable } from '@nestjs/common';
import { Pool } from 'pg';
import * as bcrypt from 'bcrypt';
import { PG_POOL } from '../database/pg-pool.token';

export interface User {
  id: string;
  email: string;
  // Null for SSO-provisioned users -- the identity provider holds the
  // credential (migration 015).
  password_hash: string | null;
  full_name: string;
  auth_provider?: string;
  workos_user_id?: string | null;
  workos_organization_id?: string | null;
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

  async findByWorkosUserId(workosUserId: string): Promise<User | null> {
    const result = await this.pool.query(
      `SELECT * FROM users WHERE workos_user_id = $1`,
      [workosUserId],
    );
    return result.rows[0] ?? null;
  }

  // Provisioned on first SSO login (see WorkosSsoService). No password is
  // set -- migration 015 makes password_hash nullable for exactly this, and
  // its CHECK constraint requires an SSO row to carry a workos_user_id.
  async createSsoUser(input: {
    email: string;
    fullName: string;
    workosUserId: string;
    workosOrganizationId?: string;
    role?: string;
  }): Promise<User> {
    const result = await this.pool.query(
      `INSERT INTO users (email, full_name, role, auth_provider, workos_user_id, workos_organization_id)
       VALUES ($1, $2, $3, 'workos_sso', $4, $5)
       RETURNING *`,
      [
        input.email.toLowerCase(),
        input.fullName,
        input.role ?? 'user',
        input.workosUserId,
        input.workosOrganizationId ?? null,
      ],
    );
    return result.rows[0];
  }

  // Links an existing (password) account to a WorkOS identity. The caller
  // is responsible for having established that this is safe -- see
  // WorkosSsoService.resolveLocalUser, which requires the IdP to have
  // verified the email first. The password stays in place: linking adds a
  // second way in, it does not remove the original one.
  async linkWorkosIdentity(
    userId: string,
    input: { workosUserId: string; workosOrganizationId?: string },
  ): Promise<User> {
    const result = await this.pool.query(
      `UPDATE users
       SET workos_user_id = $2, workos_organization_id = $3, updated_at = now()
       WHERE id = $1
       RETURNING *`,
      [userId, input.workosUserId, input.workosOrganizationId ?? null],
    );
    return result.rows[0];
  }

  verifyPassword(user: User, password: string): Promise<boolean> {
    // An SSO-provisioned account has no password to compare against;
    // bcrypt.compare(x, null) would throw, and any fallback that returns
    // true here would be an authentication bypass.
    if (!user.password_hash) return Promise.resolve(false);
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
