import { Inject, Injectable, Logger, OnModuleDestroy } from '@nestjs/common';
import type { Redis } from 'ioredis';
import { AUTH_REDIS_CLIENT } from './auth-redis-client.token';

// Minimal Redis surface this needs (keeps it unit-testable).
export interface LoginAttemptsRedis {
  get(key: string): Promise<string | null>;
  incr(key: string): Promise<number>;
  expire(key: string, seconds: number): Promise<unknown>;
  del(key: string): Promise<unknown>;
  quit(): Promise<unknown>;
}

// Failures tolerated from one source address against one account before that
// address (only) is refused for the rest of the window.
export const PAIR_MAX_FAILURES = 5;
export const PAIR_WINDOW_SECONDS = 15 * 60;
const KEY_PREFIX = 'auth:lf:';

// Per-(account, source address) failed-login tracking (AUTH-02, round 2).
//
// The only per-account control used to be a lockout that any caller could
// trip for any known email with five unauthenticated requests, and re-arm
// every fifteen minutes -- a silent, sustained denial of service against
// the account's real owner (the lock response is deliberately identical to
// a wrong-password response, so the owner is never told why). Counting
// failures per account *and* source address means an attacker locks only
// their own address out of the account; the owner, logging in from
// somewhere else, is unaffected. The account-wide lock still exists, but at
// a far higher threshold that one address can no longer reach (it is
// refused after PAIR_MAX_FAILURES, and refused attempts are not counted).
//
// No Redis -> a no-op, and the caller falls back to the old low account-wide
// threshold, which is the previous behaviour: logged, not silent.
@Injectable()
export class LoginAttemptsService implements OnModuleDestroy {
  private readonly logger = new Logger(LoginAttemptsService.name);

  constructor(@Inject(AUTH_REDIS_CLIENT) private readonly redis: LoginAttemptsRedis | null) {
    if (!redis) {
      this.logger.warn('REDIS_URL not set; per-address login tracking is off and the account-wide lockout alone applies');
    }
  }

  get enabled(): boolean {
    return !!this.redis;
  }

  private key(userId: string, ip: string): string {
    return `${KEY_PREFIX}${userId}:${ip}`;
  }

  async isBlocked(userId: string, ip: string | undefined): Promise<boolean> {
    if (!this.redis || !ip) return false;
    try {
      const n = Number(await this.redis.get(this.key(userId, ip)));
      return n >= PAIR_MAX_FAILURES;
    } catch (err) {
      // Fails open, like the revocation check: an outage degrades this
      // control, it does not lock every user out of a healthy service.
      this.logger.error(`login-attempt check failed: ${(err as Error).message}`);
      return false;
    }
  }

  async recordFailure(userId: string, ip: string | undefined): Promise<void> {
    if (!this.redis || !ip) return;
    try {
      const k = this.key(userId, ip);
      const n = await this.redis.incr(k);
      if (n === 1) await this.redis.expire(k, PAIR_WINDOW_SECONDS);
    } catch (err) {
      this.logger.error(`failed to record login failure: ${(err as Error).message}`);
    }
  }

  async clear(userId: string, ip: string | undefined): Promise<void> {
    if (!this.redis || !ip) return;
    try {
      await this.redis.del(this.key(userId, ip));
    } catch (err) {
      this.logger.error(`failed to clear login failures: ${(err as Error).message}`);
    }
  }

  async onModuleDestroy() {
    await (this.redis as Redis | null)?.quit().catch(() => undefined);
  }
}
