import { Body, Controller, Delete, Get, HttpCode, HttpStatus, Param, Post, Put, Query, Res, UseGuards } from '@nestjs/common';
import type { Response } from 'express';
import { IsBoolean, IsInt, IsOptional, IsString, Matches, Max, MaxLength, Min } from 'class-validator';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_INITIATE, CAN_MANAGE, CAN_READ_APPROVALS } from '../approvals/roles';
import { CustodyService } from './custody.service';

class CustodianDto {
  @IsString() @MaxLength(120) name: string;
  @IsString() @MaxLength(500) baseUrl: string;
  @IsString() @MaxLength(80) tokenEnv: string;
}
class EnabledDto { @IsBoolean() enabled: boolean; }
class RouteDto {
  @IsString() @MaxLength(32) asset: string;
  @IsOptional() @Matches(/^[0-9]{1,78}$/) maxAmount?: string;
  @IsString() custodianId: string;
  @IsString() @MaxLength(200) accountId: string;
  @IsOptional() @IsInt() @Min(1) @Max(10000) priority?: number;
}
class TransferDto {
  @IsOptional() @IsString() custodianId?: string;
  @IsOptional() @IsString() @MaxLength(200) accountId?: string;
  @IsOptional() @IsBoolean() route?: boolean;
  @IsString() @MaxLength(128) destination: string;
  @IsString() @MaxLength(32) asset: string;
  @Matches(/^[0-9]{1,78}$/) amount: string;
  @IsOptional() @IsInt() @Min(0) @Max(36) decimals?: number;
  @IsOptional() @IsString() @MaxLength(200) memo?: string;
}

// Assets held at other custodians, seen and governed alongside this platform's own.
// Registering a custodian or a routing rule is an administrator's act; a transfer is an
// operator's and is always held for the approvers.
@Controller('organisations/:customerId/custody')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class CustodyController {
  constructor(private readonly custody: CustodyService) {}

  @Get('overview')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  overview(@Param('customerId') customerId: string, @Query('chainId') chainId?: string) {
    return this.custody.overview(customerId, chainId ? Number(chainId) : undefined);
  }

  @Get('custodians')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  list(@Param('customerId') customerId: string) { return this.custody.listCustodians(customerId); }

  @Post('custodians')
  @HttpCode(HttpStatus.CREATED)
  @RequireTenantRole(...CAN_MANAGE)
  add(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims, @Body() dto: CustodianDto) { return this.custody.addCustodian(customerId, u.sub, dto); }

  @Put('custodians/:id/enabled')
  @RequireTenantRole(...CAN_MANAGE)
  enabled(@Param('customerId') customerId: string, @Param('id') id: string, @Body() dto: EnabledDto) { return this.custody.setEnabled(customerId, id, dto.enabled); }

  @Get('routes')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  routes(@Param('customerId') customerId: string) { return this.custody.listRoutes(customerId); }

  @Post('routes')
  @HttpCode(HttpStatus.CREATED)
  @RequireTenantRole(...CAN_MANAGE)
  addRoute(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims, @Body() dto: RouteDto) { return this.custody.addRoute(customerId, u.sub, dto); }

  @Delete('routes/:routeId')
  @HttpCode(HttpStatus.NO_CONTENT)
  @RequireTenantRole(...CAN_MANAGE)
  async removeRoute(@Param('customerId') customerId: string, @Param('routeId') routeId: string) { await this.custody.removeRoute(customerId, routeId); }

  @Post('transfers')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_INITIATE)
  async transfer(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims, @Body() dto: TransferDto, @Res({ passthrough: true }) res: Response) {
    const out = await this.custody.submitTransfer(customerId, { userId: u.sub, label: u.email }, dto);
    if (out.status === 'pending_approval') res.status(HttpStatus.ACCEPTED);
    return out;
  }

  @Get('transfers/:approvalId')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  status(@Param('customerId') customerId: string, @Param('approvalId') approvalId: string) { return this.custody.transferStatus(customerId, approvalId); }
}
