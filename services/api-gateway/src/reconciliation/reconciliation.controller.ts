import { Body, Controller, Get, Param, Post, UseGuards } from '@nestjs/common';
import { IsArray, IsIn, IsInt, IsOptional, IsString, Min, ValidateIf } from 'class-validator';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';
import { ReconciliationService } from './reconciliation.service';
import { StatementRow } from './reconcile';

class ChainRunDto {
  // An EVM chain id, or -- for a Solana or Cosmos key -- the blockchain name.
  // Exactly one of them.
  @ValidateIf((o) => o.blockchain === undefined) @IsInt() chainId?: number;
  @ValidateIf((o) => o.chainId === undefined) @IsIn(['solana', 'cosmos']) blockchain?: 'solana' | 'cosmos';
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
    const { blockchain, chainId, ...rest } = dto;
    return blockchain
      ? this.recon.runNative({ customerId: c.customer_id, requestedBy: 'api-key', blockchain, ...rest })
      : this.recon.runChain({ customerId: c.customer_id, requestedBy: 'api-key', chainId: chainId!, ...rest });
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
