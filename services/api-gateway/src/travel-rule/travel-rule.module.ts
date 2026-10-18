import { Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { ApprovalsModule } from '../approvals/approvals.module';
import { TravelRuleController } from './travel-rule.controller';
import { OrgTravelRuleController } from './org-travel-rule.controller';
import { TravelRuleService } from './travel-rule.service';
import { TrisaController } from './trisa/trisa.controller';
import { TrisaService } from './trisa/trisa.service';

@Module({
  imports: [CustomersModule, ApprovalsModule],
  controllers: [TravelRuleController, OrgTravelRuleController, TrisaController],
  providers: [TravelRuleService, TrisaService],
  exports: [TravelRuleService, TrisaService],
})
export class TravelRuleModule {}
