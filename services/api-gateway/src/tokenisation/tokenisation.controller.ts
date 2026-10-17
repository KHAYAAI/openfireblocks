import { Body, Controller, Get, HttpCode, HttpStatus, Param, Post, Res, UseGuards } from '@nestjs/common';
import type { Response } from 'express';
import { IsIn, IsInt, IsObject, IsOptional, IsString, IsUUID, MaxLength, Min } from 'class-validator';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_INITIATE, CAN_MANAGE, CAN_READ_APPROVALS } from '../approvals/roles';
import { TokenisationService } from './tokenisation.service';
import { OPS, TokenOp } from './token-calls';

class RegisterDto {
  @IsUUID() issuerKeyId: string;
  @IsInt() @Min(1) chainId: number;
  @IsString() @MaxLength(42) contractAddress: string;
}
class HolderDto {
  @IsString() @MaxLength(200) displayName: string;
  @IsString() @MaxLength(42) walletAddress: string;
  @IsString() @MaxLength(500) kycReference: string;
}
class OpDto {
  @IsIn(Object.keys(OPS)) op: TokenOp;
  @IsOptional() @IsString() holder?: string;
  @IsOptional() @IsString() to?: string;
  @IsOptional() @IsString() from?: string;
  @IsOptional() @IsString() next?: string;
  @IsOptional() @IsString() amount?: string;
}

// Security tokens: registering one an organisation has deployed, keeping the issuer's
// register of holders, and administering the token. Registering and recording holders is
// an administrator's; asking for an administrative act is an operator's; and every act is
// held for the approvers.
@Controller('organisations/:customerId/securities')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class TokenisationController {
  constructor(private readonly tokens: TokenisationService) {}

  @Get('contract')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  contract() { return this.tokens.contract(); }

  @Get()
  @RequireTenantRole(...CAN_READ_APPROVALS)
  list(@Param('customerId') customerId: string) { return this.tokens.listTokens(customerId); }

  @Post()
  @HttpCode(HttpStatus.CREATED)
  @RequireTenantRole(...CAN_MANAGE)
  register(@Param('customerId') customerId: string, @CurrentUser() u: JwtClaims, @Body() dto: RegisterDto) { return this.tokens.registerToken(customerId, u.sub, dto); }

  @Get(':tokenId')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  get(@Param('customerId') customerId: string, @Param('tokenId') tokenId: string) { return this.tokens.getToken(customerId, tokenId); }

  @Get(':tokenId/holders')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  holders(@Param('customerId') customerId: string, @Param('tokenId') tokenId: string) { return this.tokens.listHolders(customerId, tokenId); }

  @Post(':tokenId/holders')
  @HttpCode(HttpStatus.CREATED)
  @RequireTenantRole(...CAN_MANAGE)
  addHolder(@Param('customerId') customerId: string, @Param('tokenId') tokenId: string, @CurrentUser() u: JwtClaims, @Body() dto: HolderDto) {
    return this.tokens.addHolder(customerId, u.sub, tokenId, dto);
  }

  @Get(':tokenId/cap-table')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  capTable(@Param('customerId') customerId: string, @Param('tokenId') tokenId: string) { return this.tokens.capTable(customerId, tokenId); }

  @Get(':tokenId/ops')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  ops(@Param('customerId') customerId: string, @Param('tokenId') tokenId: string) { return this.tokens.listOps(customerId, tokenId); }

  @Post(':tokenId/ops')
  @HttpCode(HttpStatus.ACCEPTED)
  @RequireTenantRole(...CAN_INITIATE)
  op(@Param('customerId') customerId: string, @Param('tokenId') tokenId: string, @CurrentUser() u: JwtClaims, @Body() dto: OpDto) {
    const { op, ...params } = dto;
    return this.tokens.requestOp(customerId, { userId: u.sub, label: u.email }, tokenId, op, params);
  }
}
