import { Controller, Get, NotFoundException, Param, Query, UseGuards } from '@nestjs/common';
import { CustomerService } from '../customers/customer.service';
import { PostgresService } from '../database/postgres.service';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_READ_APPROVALS } from '../approvals/roles';
import { DashboardService } from './dashboard.service';

// The dashboard's data (balances, keys, transaction history, compliance,
// webhooks), reachable by a named, role-checked person instead of the
// tenant-wide API key the original /dashboard route uses.
//
// This is additive, not a replacement: /dashboard still works for a
// deployment that only ever issued an API key and never set up people.
// It exists so the unified console (one sign-in, one role model) can show
// the same data without a second, parallel way of deciding who may see
// what. DashboardService itself is untouched -- it only ever needed a
// Customer, not an opinion about how one was authenticated.
@Controller('organisations/:customerId')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class OrgDashboardController {
  constructor(
    private readonly dashboard: DashboardService,
    private readonly postgres: PostgresService,
    private readonly customers: CustomerService,
  ) {}

  @Get('overview')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  async overview(@Param('customerId') customerId: string) {
    return this.dashboard.overview(await this.customers.getByCustomerId(customerId));
  }

  @Get('keys')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  async keys(@Param('customerId') customerId: string) {
    return this.postgres.listKeys(customerId);
  }

  @Get('keys/:keyId')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  async keyDetail(
    @Param('customerId') customerId: string,
    @Param('keyId') keyId: string,
    @Query('chainId') chainId?: string,
  ) {
    const detail = await this.dashboard.keyDetail(
      await this.customers.getByCustomerId(customerId),
      keyId,
      chainId ? Number(chainId) : undefined,
    );
    if (!detail) throw new NotFoundException('no such key');
    return detail;
  }

  @Get('transactions')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  async transactions(@Param('customerId') customerId: string) {
    return this.postgres.listTransactions(customerId, 200);
  }

  @Get('compliance')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  async compliance(
    @Param('customerId') customerId: string,
    @Query('day') day?: string,
    @Query('chain') chain?: string,
  ) {
    return this.dashboard.compliance(await this.customers.getByCustomerId(customerId), day, chain);
  }

  @Get('webhooks')
  @RequireTenantRole(...CAN_READ_APPROVALS)
  async webhooks(@Param('customerId') customerId: string) {
    return this.dashboard.webhooks(await this.customers.getByCustomerId(customerId));
  }
}
