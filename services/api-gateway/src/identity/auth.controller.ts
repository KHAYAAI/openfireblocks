import { Body, Controller, Get, HttpCode, HttpStatus, Post, Req, UseGuards } from '@nestjs/common';
import type { Request } from 'express';
import { Throttle } from '@nestjs/throttler';
import { AuthService } from './auth.service';
import { UsersService } from './users.service';
import { RegisterDto } from './dto/register.dto';
import { LoginDto } from './dto/login.dto';
import { MfaVerifyDto } from './dto/mfa-verify.dto';
import { MfaEnrollConfirmDto } from './dto/mfa-enroll-confirm.dto';
import { MfaStepUpDto } from './dto/mfa-step-up.dto';
import { JwtAuthGuard } from './jwt-auth.guard';
import { CurrentUser } from './current-user.decorator';
import { JwtClaims } from './auth.service';

// Human dashboard auth: register -> login -> (optional MFA) -> JWT -> protected
// route. Distinct from the /v1/sign etc. tenant API, which authenticates with
// ApiKeyGuard instead.
@Controller('v1/auth')
export class AuthController {
  constructor(
    private readonly auth: AuthService,
    private readonly users: UsersService,
  ) {}

  @Post('register')
  @Throttle({ default: { limit: 5, ttl: 60_000 } })
  register(@Body() dto: RegisterDto) {
    return this.auth.register(dto);
  }

  @Post('login')
  @Throttle({ default: { limit: 10, ttl: 60_000 } })
  login(@Body() dto: LoginDto, @Req() req: Request) {
    return this.auth.login(dto.email, dto.password, req.ip);
  }

  @Post('mfa/verify')
  @Throttle({ default: { limit: 10, ttl: 60_000 } })
  verifyMfa(@Body() dto: MfaVerifyDto) {
    return this.auth.verifyMfaAndLogin(dto.email, dto.challengeToken, dto.code);
  }

  // Revokes the presented token early (AUTH-03) -- a bearer JWT used to
  // have no revocation route at all; every candidate (logout, revoke,
  // refresh) returned 404, so a captured token worked until its natural
  // expiry no matter what the victim did.
  @Post('logout')
  @HttpCode(HttpStatus.NO_CONTENT)
  @UseGuards(JwtAuthGuard)
  async logout(@CurrentUser() claims: JwtClaims) {
    await this.auth.logout(claims);
  }

  @Get('me')
  @UseGuards(JwtAuthGuard)
  async me(@CurrentUser() claims: JwtClaims) {
    const user = await this.users.findById(claims.sub);
    if (!user) return null;
    return {
      id: user.id,
      email: user.email,
      fullName: user.full_name,
      role: user.role,
      mfaEnabled: user.mfa_enabled,
    };
  }

  @Post('mfa/enroll')
  @Throttle({ default: { limit: 10, ttl: 60_000 } })
  @UseGuards(JwtAuthGuard)
  beginEnrollment(@CurrentUser() claims: JwtClaims, @Body() dto: MfaStepUpDto) {
    return this.auth.beginMfaEnrollment(claims.sub, dto.password, dto.totpCode);
  }

  @Post('mfa/enroll/confirm')
  @UseGuards(JwtAuthGuard)
  async confirmEnrollment(@CurrentUser() claims: JwtClaims, @Body() dto: MfaEnrollConfirmDto) {
    await this.auth.confirmMfaEnrollment(claims.sub, dto.code);
    return { mfaEnabled: true };
  }

  @Post('mfa/disable')
  @Throttle({ default: { limit: 10, ttl: 60_000 } })
  @UseGuards(JwtAuthGuard)
  async disableMfa(@CurrentUser() claims: JwtClaims, @Body() dto: MfaStepUpDto) {
    await this.auth.disableMfa(claims.sub, dto.password, dto.totpCode);
    return { mfaEnabled: false };
  }
}
