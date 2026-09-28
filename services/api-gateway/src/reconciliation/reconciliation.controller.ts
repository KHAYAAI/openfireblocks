import { Body, Controller, Get, Param, Post, UseGuards } from '@nestjs/common';
import { IsArray, IsInt, IsOptional, IsString, Min } from 'class-validator';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';
import { ReconciliationService } from './reconciliation.service';
import { StatementRow } from './reconcile';

class ChainRunDto {
  @IsInt() chainId: number;
  @IsOptional() @IsInt() @Min(1) sinceHours?: number;
  @IsOptional() @IsInt() @Min(1) missingAfterMinutes?: number;
}

class StatementDto {
  @IsString() periodStart: string;
  @IsString() periodEnd: string;
  @IsArray() rows: StatementRow[];
}

@Controller('reconciliation')
@UseGuards(ApiKeyGuard)
export class ReconciliationController {
  constructor(private readonly recon: ReconciliationService) {}

  @Post('chain')
  chain(@CurrentCustomer() c: Customer, @Body() dto: ChainRunDto) {
    return this.recon.runChain({ customerId: c.customer_id, requestedBy: 'api-key', ...dto });
  }

  @Post('statements')
  statement(@CurrentCustomer() c: Customer, @Body() dto: StatementDto) {
    return this.recon.runStatement({ customerId: c.customer_id, requestedBy: 'api-key', ...dto });
  }

  @Get('runs')
  list(@CurrentCustomer() c: Customer) {
    return this.recon.list(c.customer_id);
  }

  @Get('runs/:runId')
  get(@CurrentCustomer() c: Customer, @Param('runId') runId: string) {
    return this.recon.get(c.customer_id, runId);
  }
}
