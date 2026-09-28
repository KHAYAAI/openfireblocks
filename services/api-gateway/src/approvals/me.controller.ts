import { Controller, Get, UnauthorizedException, UseGuards } from '@nestjs/common';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { UsersService } from '../identity/users.service';
import { ApprovalsService } from './approvals.service';

// Who am I, and where can I act. What the console needs on sign-in to
// show the right organisations and to know whether a decision will ask
// for a one-time code (password accounts) or not (SSO accounts).
@Controller('me')
@UseGuards(JwtAuthGuard)
export class MeController {
  constructor(
    private readonly users: UsersService,
    private readonly approvals: ApprovalsService,
  ) {}

  @Get('organisations')
  async organisations(@CurrentUser() claims: JwtClaims) {
    const user = await this.users.findById(claims.sub);
    if (!user || user.status !== 'active') throw new UnauthorizedException();
    return {
      user: {
        id: user.id,
        email: user.email,
        fullName: user.full_name,
        authProvider: user.auth_provider ?? 'password',
        mfaEnabled: user.mfa_enabled,
      },
      organisations: await this.approvals.organisationsFor(user.id),
    };
  }
}
