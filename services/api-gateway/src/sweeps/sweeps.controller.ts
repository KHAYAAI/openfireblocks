import { Body, Controller, Delete, Get, HttpCode, HttpStatus, Param, Post, Put, UseGuards } from '@nestjs/common';
import { IsBoolean, IsInt, IsObject, IsOptional, IsString, IsUUID, Matches, Max, MaxLength, Min } from 'class-validator';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_INITIATE, CAN_MANAGE, CAN_READ_APPROVALS } from '../approvals/roles';
import { SweepsService } from './sweeps.service';

class SweepRuleDto {
  @IsString() @MaxLength(120) name: string;
  @IsUUID() keyId: string;
  @IsOptional() @IsInt() @Min(1) chainId?: number;
  @IsString() @MaxLength(128) destination: string;
  @Matches(/^[0-9]{1,78}$/) minAmount: string;
  @IsOptional() @Matches(/^[0-9]{1,78}$/) reserve?: string;
  @IsOptional() @IsInt() @Min(60) @Max(604800) intervalSeconds?: number;
  @IsOptional() @IsObject() travelRule?: Record<string, unknown>;
}
class EnabledDto { @IsBoolean() enabled: boolean; }

// Deposit-sweep rules. Making or changing a rule is an administrator's act (it
// decides where standing money goes); running one is an operator's, and the
// run is an ordinary transfer, so it is held for approval like any other.
@Controller('organisations/:customerId/sweeps')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class SweepsController {
  constructor(private readonly sweeps: SweepsService) {}

  @Get()
  @RequireTenantRole(...CAN_READ_APPROVALS)
  list(@Param('customerId') customerId: string) { return this.sweeps.list(customerId); }

  @Get('runs')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  runs(@Param('customerId') customerId: string) { return this.sweeps.runs(customerId); }

  @Post()
  @HttpCode(HttpStatus.CREATED)
  @RequireTenantRole(...CAN_MANAGE)
  create(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims, @Body() dto: SweepRuleDto) {
    return this.sweeps.create(customerId, u.sub, dto);
  }

  @Put(':ruleId/enabled')
  @RequireTenantRole(...CAN_MANAGE)
  enabled(@Param('customerId') customerId: string, @Param('ruleId') ruleId: string, @Body() dto: EnabledDto) {
    return this.sweeps.setEnabled(customerId, ruleId, dto.enabled);
  }

  @Delete(':ruleId')
  @HttpCode(HttpStatus.NO_CONTENT)
  @RequireTenantRole(...CAN_MANAGE)
  async remove(@Param('customerId') customerId: string, @Param('ruleId') ruleId: string) {
    await this.sweeps.remove(customerId, ruleId);
  }

  @Post(':ruleId/run')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_INITIATE)
  run(@Param('customerId') customerId: string, @Param('ruleId') ruleId: string, @CurrentUser() u: JwtClaims) {
    return this.sweeps.run(customerId, ruleId, 'manual', u.email, u.sub);
  }
}
