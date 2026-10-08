import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PassportModule } from '@nestjs/passport';
import Redis from 'ioredis';
import { AuthController } from './auth.controller';
import { AuthService } from './auth.service';
import { UsersService } from './users.service';
import { MfaChallengesService } from './mfa-challenges.service';
import { JwtAuthStrategy, jwtSecret } from './jwt.strategy';
import { WorkosSsoController } from './workos-sso.controller';
import { WorkosSsoService } from './workos-sso.service';
import { OidcSsoService } from './oidc-sso.service';
import { TokenRevocationService } from './token-revocation.service';
import { AUTH_REDIS_CLIENT } from './auth-redis-client.token';

// Human dashboard identity: registration, password + TOTP MFA login,
// enterprise SSO via WorkOS AuthKit, and the JWT strategy/guard other
// modules use to protect user-facing routes. Both login paths issue the
// same session token -- see WorkosSsoService's doc comment.
@Module({
  imports: [
    PassportModule,
    JwtModule.register({
      secret: jwtSecret(),
      signOptions: { issuer: 'openfireblocks' },
    }),
  ],
  controllers: [AuthController, WorkosSsoController],
  providers: [
    AuthService,
    UsersService,
    MfaChallengesService,
    JwtAuthStrategy,
    WorkosSsoService,
    OidcSsoService,
    TokenRevocationService,
    {
      provide: AUTH_REDIS_CLIENT,
      useFactory: () => {
        const url = process.env.REDIS_URL;
        if (!url) return null;
        const client = new Redis(url, { lazyConnect: true, maxRetriesPerRequest: 1 });
        client.connect().catch(() => undefined);
        return client;
      },
    },
  ],
  exports: [AuthService, UsersService, WorkosSsoService, OidcSsoService, TokenRevocationService],
})
export class IdentityModule {}
