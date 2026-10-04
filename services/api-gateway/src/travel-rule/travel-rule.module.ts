import { Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { ApprovalsModule } from '../approvals/approvals.module';
import { TravelRuleController } from './travel-rule.controller';
import { OrgTravelRuleController } from './org-travel-rule.controller';
import { TravelRuleService } from './travel-rule.service';

@Module({
  imports: [CustomersModule, ApprovalsModule],
  controllers: [TravelRuleController, OrgTravelRuleController],
  providers: [TravelRuleService],
  exports: [TravelRuleService],
})
export class TravelRuleModule {}
