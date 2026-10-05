import { Module } from '@nestjs/common';
import { BillingService } from './billing.service';
import { BillingController } from './billing.controller';
import { BillingClient } from './billing-client.service';
import { OrgBillingController } from './org-billing.controller';
import { CustomersModule } from '../customers/customers.module';
import { ApprovalsModule } from '../approvals/approvals.module';

// Usage metering + the admin usage endpoint. Exports BillingService so the sign
// flow can record usage.
@Module({
  imports: [CustomersModule, ApprovalsModule],
  controllers: [BillingController, OrgBillingController],
  providers: [BillingService, BillingClient],
  exports: [BillingService],
})
export class BillingModule {}
