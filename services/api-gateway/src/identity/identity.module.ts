import { Module } from '@nestjs/common';
import { JwtModule } from '@nestjs/jwt';
import { PassportModule } from '@nestjs/passport';
import { AuthController } from './auth.controller';
import { AuthService } from './auth.service';
import { UsersService } from './users.service';
import { MfaChallengesService } from './mfa-challenges.service';
import { JwtAuthStrategy, jwtSecret } from './jwt.strategy';

// Human dashboard identity: registration, password + TOTP MFA login, and the
// JWT strategy/guard other modules use to protect user-facing routes.
@Module({
  imports: [
    PassportModule,
    JwtModule.register({
      secret: jwtSecret(),
      signOptions: { issuer: 'openfireblocks' },
    }),
  ],
  controllers: [AuthController],
  providers: [AuthService, UsersService, MfaChallengesService, JwtAuthStrategy],
  exports: [AuthService, UsersService],
})
export class IdentityModule {}
