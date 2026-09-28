import { Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { TravelRuleController } from './travel-rule.controller';
import { TravelRuleService } from './travel-rule.service';

@Module({
  imports: [CustomersModule],
  controllers: [TravelRuleController],
  providers: [TravelRuleService],
  exports: [TravelRuleService],
})
export class TravelRuleModule {}
