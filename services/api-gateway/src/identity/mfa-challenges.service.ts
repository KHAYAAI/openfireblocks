import { Inject, Injectable } from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { challengeExpiry, generateChallengeToken, hashChallengeToken } from './mfa.util';

// Persists the opaque MFA challenge tokens issued between "password verified"
// and "TOTP verified" so a token can only ever be consumed once, from any
// process instance (a stateless in-memory map would break on the second pod).
@Injectable()
export class MfaChallengesService {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  async create(userId: string): Promise<string> {
    const token = generateChallengeToken();
    await this.pool.query(
      `INSERT INTO mfa_challenges (user_id, challenge_hash, expires_at) VALUES ($1, $2, $3)`,
      [userId, hashChallengeToken(token), challengeExpiry()],
    );
    return token;
  }

  // Atomically marks the challenge consumed and returns its user_id, or null if
  // the token is unknown, expired, or already used. The UPDATE...RETURNING is
  // a single round trip so two concurrent requests can't both succeed.
  async consume(userId: string, token: string): Promise<boolean> {
    const result = await this.pool.query(
      `UPDATE mfa_challenges
       SET consumed_at = NOW()
       WHERE user_id = $1 AND challenge_hash = $2
         AND expires_at > NOW() AND consumed_at IS NULL
       RETURNING id`,
      [userId, hashChallengeToken(token)],
    );
    return (result.rowCount ?? 0) > 0;
  }
}
