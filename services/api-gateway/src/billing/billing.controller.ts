import { Controller, Get, Param, UseGuards } from '@nestjs/common';
import { BillingService } from './billing.service';
import { AdminGuard } from '../auth/admin.guard';

// Admin endpoint to read a tenant's metered usage (for billing/reconciliation).
@Controller('admin/customers')
@UseGuards(AdminGuard)
export class BillingController {
  constructor(private readonly billing: BillingService) {}

  @Get(':customerId/usage')
  usage(@Param('customerId') customerId: string) {
    return this.billing.getUsage(customerId);
  }
}
