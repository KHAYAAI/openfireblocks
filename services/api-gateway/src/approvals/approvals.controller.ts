import {
  BadGatewayException,
  Body,
  Controller,
  Delete,
  ForbiddenException,
  Get,
  HttpCode,
  HttpStatus,
  Logger,
  Param,
  Post,
  Put,
  Query,
  UnauthorizedException,
  UseGuards,
} from '@nestjs/common';
import { IsIn, IsInt, IsOptional, IsString, Matches, MaxLength } from 'class-validator';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { UsersService } from '../identity/users.service';
import { verifyTotpCode } from '../identity/mfa.util';
import { TemporalService } from '../settlements/temporal.service';
import { CustomerService } from '../customers/customer.service';
import { SignRequestDto } from '../sign/dto/sign-request.dto';
import { AuditService } from '../database/audit.service';
import { ApprovalsService, Decision } from './approvals.service';
import { RequireTenantRole, TenantRoleGuard } from './tenant-role.guard';
import { ALL_ROLES, CAN_DECIDE, CAN_INITIATE, CAN_MANAGE, CAN_READ_APPROVALS, TenantRole } from './roles';
import { v4 as uuid } from 'uuid';

export class DecisionDto {
  @IsIn(['approve', 'reject'])
  decision: Decision;

  @IsOptional()
  @IsString()
  @MaxLength(1000)
  reason?: string;

  // A current one-time code from the approver's authenticator. Required
  // for password accounts on every decision, not just at sign-in: the
  // decision is the moment that matters, and a session left open on a
  // desk should not be enough to move money.
  @IsOptional()
  @Matches(/^\d{6}$/)
  totpCode?: string;
}

export class PolicyDto {
  @IsInt()
  requiredApprovals: number;

  @IsInt()
  windowMinutes: number;
}

export class MemberDto {
  @IsString()
  @MaxLength(255)
  email: string;

  @IsIn(ALL_ROLES as TenantRole[])
  role: TenantRole;
}

// People acting inside one organisation: the approval queue, decisions,
// the approval policy, membership, and starting a transfer as a named
// person.
//
// Every route needs a signed-in person (JwtAuthGuard: password + TOTP, or
// SSO) holding a role in the organisation (TenantRoleGuard). API keys do
// not work here, by design: an API key is a machine, and this is where
// people are held accountable by name.
@Controller('organisations/:customerId')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class ApprovalsController {
  private readonly logger = new Logger(ApprovalsController.name);

  constructor(
    private readonly approvals: ApprovalsService,
    private readonly users: UsersService,
    private readonly temporal: TemporalService,
    private readonly customers: CustomerService,
    private readonly audit: AuditService,
  ) {}

  // ------------------------------------------------------------ approvals

  @Get('approvals')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  list(@Param('customerId') customerId: string, @Query('status') status?: string) {
    return this.approvals.list(customerId, status);
  }

  @Get('approvals/:approvalId')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  get(@Param('customerId') customerId: string, @Param('approvalId') approvalId: string) {
    return this.approvals.get(customerId, approvalId);
  }

  @Post('approvals/:approvalId/decisions')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_DECIDE)
  async decide(
    @Param('customerId') customerId: string,
    @Param('approvalId') approvalId: string,
    @CurrentUser() claims: JwtClaims,
    @Body() dto: DecisionDto,
  ) {
    const stepUp = await this.stepUp(claims.sub, dto.totpCode);

    const { request, alreadyRecorded } = await this.approvals.recordDecision({
      customerId,
      approvalId,
      userId: claims.sub,
      decision: dto.decision,
      reason: dto.reason,
      stepUp,
    });

    // Recorded first, delivered second. If delivery fails the decision
    // still stands; sending the same request again re-delivers it.
    try {
      await this.temporal.signalDecision(customerId, request.workflowId, {
        approverUserId: claims.sub,
        decision: dto.decision,
      });
    } catch (err) {
      this.logger.error(`decision recorded but not delivered to ${request.workflowId}: ${(err as Error).message}`);
      throw new BadGatewayException(
        'your decision is recorded but could not be delivered to the settlement; send the same request again to retry',
      );
    }

    if (!alreadyRecorded) {
      await this.audit.logEvent({
        type: `approval.${dto.decision}`,
        requestId: approvalId,
        customerId,
        message: `${claims.email} decided ${dto.decision} (${stepUp}); now ${request.approvals}/${request.requiredApprovals}, ${request.status}`,
        status: request.status,
      });
    }
    return request;
  }

  // Password accounts prove presence with a fresh one-time code on each
  // decision. SSO accounts rely on the identity provider, which is where
  // an enterprise enforces its own MFA; the decision records which.
  private async stepUp(userId: string, totpCode?: string): Promise<'totp' | 'sso'> {
    const user = await this.users.findById(userId);
    if (!user || user.status !== 'active') throw new UnauthorizedException();
    if (user.auth_provider === 'workos_sso') return 'sso';
    if (!user.mfa_enabled || !user.mfa_secret) {
      throw new ForbiddenException('turn on two-factor authentication before approving or rejecting transfers');
    }
    if (!totpCode || !verifyTotpCode(user.mfa_secret, totpCode)) {
      throw new UnauthorizedException('a current one-time code (totpCode) is required to decide on a transfer');
    }
    return 'totp';
  }

  // --------------------------------------------------------------- policy

  @Get('approval-policy')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  getPolicy(@Param('customerId') customerId: string) {
    return this.approvals.getPolicy(customerId);
  }

  @Put('approval-policy')
  @RequireTenantRole(...CAN_MANAGE)
  async setPolicy(@Param('customerId') customerId: string, @CurrentUser() claims: JwtClaims, @Body() dto: PolicyDto) {
    const before = await this.approvals.getPolicy(customerId);
    const after = await this.approvals.setPolicy(customerId, claims.sub, dto.requiredApprovals, dto.windowMinutes);
    await this.audit.logEvent({
      type: 'approval.policy_changed',
      requestId: uuid(),
      customerId,
      message: `${claims.email} changed the approval policy from ${before.requiredApprovals} approvals/${before.windowMinutes}min to ${after.requiredApprovals}/${after.windowMinutes}min`,
      status: 'changed',
    });
    return after;
  }

  // -------------------------------------------------------------- members

  @Get('members')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  members(@Param('customerId') customerId: string) {
    return this.approvals.listMembers(customerId);
  }

  @Put('members')
  @RequireTenantRole(...CAN_MANAGE)
  async setMember(@Param('customerId') customerId: string, @CurrentUser() claims: JwtClaims, @Body() dto: MemberDto) {
    const member = await this.approvals.setMember(customerId, dto.email, dto.role);
    await this.audit.logEvent({
      type: 'membership.changed',
      requestId: uuid(),
      customerId,
      message: `${claims.email} set ${member.email} to ${member.role}`,
      status: 'changed',
    });
    return member;
  }

  @Delete('members/:userId')
  @HttpCode(HttpStatus.NO_CONTENT)
  @RequireTenantRole(...CAN_MANAGE)
  async removeMember(@Param('customerId') customerId: string, @Param('userId') userId: string, @CurrentUser() claims: JwtClaims) {
    await this.approvals.removeMember(customerId, userId);
    await this.audit.logEvent({
      type: 'membership.removed',
      requestId: uuid(),
      customerId,
      message: `${claims.email} removed user ${userId}`,
      status: 'changed',
    });
  }

  // ---------------------------------------------------------- settlements

  // Starting a transfer as a named person, so the approval step knows
  // whom to exclude.
  @Post('settlements')
  @HttpCode(HttpStatus.ACCEPTED)
  @RequireTenantRole(...CAN_INITIATE)
  async startSettlement(@Param('customerId') customerId: string, @CurrentUser() claims: JwtClaims, @Body() req: SignRequestDto) {
    const customer = await this.customers.getByCustomerId(customerId);
    return this.temporal.start(customerId, customer.tier, req, { userId: claims.sub, label: claims.email });
  }
}
