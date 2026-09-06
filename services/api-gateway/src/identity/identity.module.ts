import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PassportModule } from '@nestjs/passport';
import { AuthController } from './auth.controller';
import { AuthService } from './auth.service';
import { UsersService } from './users.service';
import { MfaChallengesService } from './mfa-challenges.service';
import { JwtAuthStrategy, jwtSecret } from './jwt.strategy';
import { WorkosSsoController } from './workos-sso.controller';
import { WorkosSsoService } from './workos-sso.service';

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
  ],
  exports: [AuthService, UsersService, WorkosSsoService],
})
export class IdentityModule {}
