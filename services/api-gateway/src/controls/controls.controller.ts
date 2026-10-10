import { Body, Controller, Delete, Get, HttpCode, HttpStatus, Param, Post, Put, UseGuards } from '@nestjs/common';
import { IsBoolean, IsInt, IsOptional, IsString, Max, MaxLength, Min } from 'class-validator';
import { CustomerService } from '../customers/customer.service';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_DECIDE, CAN_MANAGE, CAN_READ_APPROVALS } from '../approvals/roles';
import { ControlsService } from './controls.service';

class FreezeDto { @IsString() @MaxLength(500) reason: string; }
class WhitelistModeDto {
  @IsBoolean() enforced: boolean;
  @IsInt() @Min(0) @Max(10080) cooldownMinutes: number;
}
class WhitelistEntryDto {
  @IsString() blockchain: string;
  @IsString() @MaxLength(128) address: string;
  @IsOptional() @IsString() @MaxLength(120) label?: string;
}

// The organisation's safety controls, for named people in the console.
//
// Stopping is easy and starting again is not: anyone who can decide on
// transfers can freeze (a stop is always safe), and only an admin can lift
// the freeze or change the whitelist.
@Controller('organisations/:customerId/controls')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class ControlsController {
  constructor(private readonly controls: ControlsService, private readonly customers: CustomerService) {}

  private async org(customerId: string) { return (await this.customers.getByCustomerId(customerId)).name; }

  @Get()
  @RequireTenantRole(...CAN_READ_APPROVALS)
  async get(@Param('customerId') customerId: string) {
    return { ...(await this.controls.get(customerId)), whitelist: await this.controls.listWhitelist(customerId) };
  }

  @Post('freeze')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_DECIDE)
  async freeze(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims, @Body() dto: FreezeDto) {
    return this.controls.freeze(customerId, await this.org(customerId), u.sub, u.email, dto.reason);
  }

  @Post('unfreeze')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_MANAGE)
  async unfreeze(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims) {
    return this.controls.unfreeze(customerId, await this.org(customerId), u.email);
  }

  @Put('whitelist-mode')
  @RequireTenantRole(...CAN_MANAGE)
  async mode(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims, @Body() dto: WhitelistModeDto) {
    return this.controls.setWhitelistMode(customerId, await this.org(customerId), u.email, dto.enforced, dto.cooldownMinutes);
  }

  @Post('whitelist')
  @RequireTenantRole(...CAN_MANAGE)
  async add(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims, @Body() dto: WhitelistEntryDto) {
    return this.controls.addToWhitelist(customerId, await this.org(customerId), u.sub, u.email, dto);
  }

  @Delete('whitelist/:entryId')
  @HttpCode(HttpStatus.NO_CONTENT)
  @RequireTenantRole(...CAN_MANAGE)
  async remove(@Param('customerId') customerId: string, @Param('entryId') entryId: string, @CurrentUser() u: JwtClaims) {
    await this.controls.removeFromWhitelist(customerId, await this.org(customerId), u.sub, u.email, entryId);
  }
}
