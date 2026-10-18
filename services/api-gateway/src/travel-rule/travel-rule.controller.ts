import { Body, Controller, Get, Param, Post, Query, UseGuards } from '@nestjs/common';
import { IsString, MaxLength } from 'class-validator';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';
import { TravelRuleService } from './travel-rule.service';

class TransmittedDto {
  @IsString()
  @MaxLength(500)
  reference: string;
}

// The Travel Rule records a tenant has kept, and a way to say one was sent
// outside the platform.
@Controller('travel-rule')
@UseGuards(ApiKeyGuard)
export class TravelRuleController {
  constructor(private readonly travelRule: TravelRuleService) {}

  @Get('records')
  list(@CurrentCustomer() customer: Customer, @Query('status') status?: string) {
    return this.travelRule.list(customer.customer_id, status);
  }

  @Get('records/:recordId')
  get(@CurrentCustomer() customer: Customer, @Param('recordId') recordId: string) {
    return this.travelRule.get(customer.customer_id, recordId);
  }

  @Post('records/:recordId/transmitted')
  transmitted(@CurrentCustomer() customer: Customer, @Param('recordId') recordId: string, @Body() dto: TransmittedDto) {
    return this.travelRule.markTransmitted(customer.customer_id, recordId, dto.reference);
  }
}
