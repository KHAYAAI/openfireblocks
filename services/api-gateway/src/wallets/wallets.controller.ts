import {
  Body, Controller, Get, HttpCode, HttpStatus, Param, Post, Query, Res, UseGuards,
} from '@nestjs/common';
import type { Response } from 'express';
import {
  ArrayMaxSize, ArrayMinSize, IsArray, IsInt, IsOptional, IsString, Length, Matches, Max, Min,
} from 'class-validator';
import { Type } from 'class-transformer';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer, CustomerService } from '../customers/customer.service';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_MANAGE, CAN_READ_APPROVALS } from '../approvals/roles';
import { AuditService } from '../database/audit.service';
import { WalletsService } from './wallets.service';

export class CreateWalletDto {
  // The customer's own identifier for its user. Not a name, email or ID number:
  // the platform keeps no personal information about end users.
  @Matches(/^[A-Za-z0-9._:-]{1,128}$/, { message: 'externalRef must be 1-128 characters of A-Z a-z 0-9 . _ : -' })
  externalRef: string;

  @IsOptional() @IsString() @Length(1, 120)
  label?: string;

  @IsArray() @ArrayMinSize(1) @ArrayMaxSize(5) @IsString({ each: true })
  blockchains: string[];

  @IsOptional() @IsInt() @Min(2) @Max(15)
  threshold?: number;

  @IsOptional() @IsInt() @Min(2) @Max(15)
  totalParties?: number;
}

export class WalletStatusDto {
  @IsString() @Length(3, 500)
  reason: string;
}

export class ListWalletsQuery {
  @IsOptional() @Type(() => Number) @IsInt() @Min(1) @Max(200)
  limit?: number;

  @IsOptional() @IsString()
  before?: string;
}

// The machine API a customer's own platform calls for each of its users.
//
// Freezing is here, so a customer's fraud system can stop a user's wallet at
// once. Unfreezing is not: it needs a person, an organisation admin in the
// console, so a stolen API key cannot undo a freeze.
@Controller('wallets')
@UseGuards(ApiKeyGuard)
export class WalletsController {
  constructor(private readonly wallets: WalletsService, private readonly audit: AuditService) {}

  @Post()
  @HttpCode(HttpStatus.CREATED)
  create(@CurrentCustomer() customer: Customer, @Body() dto: CreateWalletDto) {
    return this.wallets.create(customer, dto);
  }

  @Get()
  list(@CurrentCustomer() customer: Customer, @Query() q: ListWalletsQuery) {
    return this.wallets.list(customer.customer_id, q);
  }

  @Get('by-ref/:externalRef')
  byRef(@CurrentCustomer() customer: Customer, @Param('externalRef') ref: string) {
    return this.wallets.byRef(customer.customer_id, ref);
  }

  @Get(':walletId')
  get(@CurrentCustomer() customer: Customer, @Param('walletId') walletId: string) {
    return this.wallets.get(customer.customer_id, walletId);
  }

  @Get(':walletId/addresses')
  addresses(@CurrentCustomer() customer: Customer, @Param('walletId') walletId: string) {
    return this.wallets.addresses(customer, walletId);
  }

  @Get(':walletId/balances')
  balances(@CurrentCustomer() customer: Customer, @Param('walletId') walletId: string, @Query('chainId') chainId?: string) {
    return this.wallets.balances(customer, walletId, chainId === undefined ? undefined : Number(chainId));
  }

  @Post(':walletId/transfers')
  @HttpCode(HttpStatus.OK)
  async transfer(
    @CurrentCustomer() customer: Customer,
    @Param('walletId') walletId: string,
    @Body() body: Record<string, unknown>,
    @Res({ passthrough: true }) res: Response,
  ) {
    const out = await this.wallets.transfer(customer, walletId, body, { userId: null, label: 'api-key' });
    if (out.status === 'pending_approval') res.status(HttpStatus.ACCEPTED);
    return out;
  }

  @Post(':walletId/freeze')
  @HttpCode(HttpStatus.OK)
  async freeze(@CurrentCustomer() customer: Customer, @Param('walletId') walletId: string, @Body() dto: WalletStatusDto) {
    const wallet = await this.wallets.setStatus(customer.customer_id, walletId, 'frozen', dto.reason);
    await this.audit.logEvent({
      type: 'wallet.frozen', requestId: walletId, customerId: customer.customer_id,
      message: `API key froze user wallet ${walletId} (${wallet.external_ref}): ${dto.reason}`, status: 'frozen',
    });
    return wallet;
  }
}

// The console's view: everyone who may read approvals may read wallets;
// only an admin freezes or unfreezes.
@Controller('organisations/:customerId/wallets')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class OrgWalletsController {
  constructor(
    private readonly wallets: WalletsService,
    private readonly customers: CustomerService,
    private readonly audit: AuditService,
  ) {}

  @Get()
  @RequireTenantRole(...CAN_READ_APPROVALS)
  list(@Param('customerId') customerId: string, @Query() q: ListWalletsQuery) {
    return this.wallets.list(customerId, q);
  }

  @Get(':walletId')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  get(@Param('customerId') customerId: string, @Param('walletId') walletId: string) {
    return this.wallets.get(customerId, walletId);
  }

  @Get(':walletId/balances')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  async balances(@Param('customerId') customerId: string, @Param('walletId') walletId: string, @Query('chainId') chainId?: string) {
    return this.wallets.balances(await this.customers.getByCustomerId(customerId), walletId, chainId === undefined ? undefined : Number(chainId));
  }

  @Post(':walletId/freeze')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_MANAGE)
  freeze(@Param('customerId') customerId: string, @Param('walletId') walletId: string, @Body() dto: WalletStatusDto, @CurrentUser() claims: JwtClaims) {
    return this.change(customerId, walletId, 'frozen', dto.reason, claims);
  }

  @Post(':walletId/unfreeze')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_MANAGE)
  unfreeze(@Param('customerId') customerId: string, @Param('walletId') walletId: string, @Body() dto: WalletStatusDto, @CurrentUser() claims: JwtClaims) {
    return this.change(customerId, walletId, 'active', dto.reason, claims);
  }

  private async change(customerId: string, walletId: string, status: 'active' | 'frozen', reason: string, claims: JwtClaims) {
    const wallet = await this.wallets.setStatus(customerId, walletId, status, `${reason} (${claims.email})`);
    await this.audit.logEvent({
      type: status === 'frozen' ? 'wallet.frozen' : 'wallet.unfrozen', requestId: walletId, customerId,
      message: `${claims.email} ${status === 'frozen' ? 'froze' : 'unfroze'} user wallet ${walletId} (${wallet.external_ref}): ${reason}`,
      status,
    });
    return wallet;
  }
}
