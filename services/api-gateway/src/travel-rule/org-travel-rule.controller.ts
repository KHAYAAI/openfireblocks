import { Body, Controller, Get, Param, Post, Query, UseGuards } from '@nestjs/common';
import { IsString, MaxLength } from 'class-validator';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_MANAGE, CAN_READ_APPROVALS } from '../approvals/roles';
import { TravelRuleService } from './travel-rule.service';

class TransmittedDto {
  @IsString()
  @MaxLength(500)
  reference: string;
}

// Travel Rule records, for a named person with a role in the
// organisation -- the same records /travel-rule exposes to an API key,
// readable from the console people actually use.
@Controller('organisations/:customerId/travel-rule')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class OrgTravelRuleController {
  constructor(private readonly travelRule: TravelRuleService) {}

  @Get('records')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  list(@Param('customerId') customerId: string, @Query('status') status?: string) {
    return this.travelRule.list(customerId, status);
  }

  @Get('records/:recordId')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  get(@Param('customerId') customerId: string, @Param('recordId') recordId: string) {
    return this.travelRule.get(customerId, recordId);
  }

  // Send (or send again) through the configured provider.
  @Post('records/:recordId/transmit')
  @RequireTenantRole(...CAN_MANAGE)
  transmit(@Param('customerId') customerId: string, @Param('recordId') recordId: string) {
    return this.travelRule.retransmit(customerId, recordId);
  }

  // Recording that a record was sent outside the platform is a compliance
  // decision, not a read -- restricted to admins like every other write
  // in this console.
  @Post('records/:recordId/transmitted')
  @RequireTenantRole(...CAN_MANAGE)
  transmitted(
    @Param('customerId') customerId: string,
    @Param('recordId') recordId: string,
    @Body() dto: TransmittedDto,
  ) {
    return this.travelRule.markTransmitted(customerId, recordId, dto.reference);
  }
}
