import { Inject, Injectable, Logger, OnModuleDestroy } from '@nestjs/common';
import type { Redis } from 'ioredis';
import { AUTH_REDIS_CLIENT } from './auth-redis-client.token';

// Minimal Redis surface this needs (keeps it unit-testable), mirroring
// risk.service.ts's RiskRedis.
export interface RevocationRedis {
  set(key: string, value: string, mode: 'EX', ttlSeconds: number): Promise<unknown>;
  exists(key: string): Promise<number>;
  quit(): Promise<unknown>;
}

const KEY_PREFIX = 'auth:revoked:';

// A server-side denylist for JWT access tokens (AUTH-03) and dashboard
// session cookies (AUTH-04). Both used to be purely stateless bearer
// credentials: once issued, a JWT authenticated for its full hour-long
// TTL and a dashboard cookie for its full eight hours, with nothing any
// endpoint -- including a deliberate logout -- could do to invalidate one
// early. A captured token or cookie was proven to keep working across
// repeated calls with no revocation surface at all (every candidate
// logout/revoke/refresh route returned 404).
//
// No Redis configured -> this is a no-op: isRevoked always answers false,
// which is the same behaviour as before this existed (a captured
// credential works until it expires). That is a real, logged gap, not a
// silent one, and it is the same operational requirement MISC-02 already
// put on this deployment: configure REDIS_URL, and this starts working
// for free from the same client.
@Injectable()
export class TokenRevocationService implements OnModuleDestroy {
  private readonly logger = new Logger(TokenRevocationService.name);

  constructor(@Inject(AUTH_REDIS_CLIENT) private readonly redis: RevocationRedis | null) {
    if (!redis) {
      this.logger.warn('REDIS_URL not set; token/session revocation (logout) is a no-op');
    }
  }

  // Marks jti revoked for ttlSeconds (the remaining lifetime of the
  // credential it names) -- never longer, so the denylist entry expires
  // itself at the same moment the credential it is blocking would have
  // expired anyway, instead of growing forever.
  async revoke(jti: string, ttlSeconds: number): Promise<void> {
    if (!this.redis || ttlSeconds <= 0) return;
    try {
      await this.redis.set(KEY_PREFIX + jti, '1', 'EX', Math.ceil(ttlSeconds));
    } catch (err) {
      this.logger.error(`failed to record revocation of ${jti}: ${(err as Error).message}`);
    }
  }

  async isRevoked(jti: string | undefined): Promise<boolean> {
    if (!jti || !this.redis) return false;
    try {
      return (await this.redis.exists(KEY_PREFIX + jti)) > 0;
    } catch (err) {
      // Fails open: a Redis outage degrades revocation checking (a
      // logged-out token might still work until it naturally expires),
      // it does not lock every signed-in user out of a service that is
      // otherwise healthy.
      this.logger.error(`revocation check failed for ${jti}: ${(err as Error).message}`);
      return false;
    }
  }

  async onModuleDestroy() {
    await (this.redis as Redis | null)?.quit().catch(() => undefined);
  }
}
