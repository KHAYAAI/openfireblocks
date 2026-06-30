import {
  Body,
  Controller,
  Get,
  HttpCode,
  HttpStatus,
  NotFoundException,
  Param,
  Post,
  Query,
  UseGuards,
} from '@nestjs/common';
import { SignService } from './sign.service';
import { SignRequestDto } from './dto/sign-request.dto';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';
import { PostgresService } from '../database/postgres.service';
import { AuditService } from '../database/audit.service';
import { PrepareService } from '../blockchain/prepare.service';

// Tenant-facing signing + read API. Every route requires a valid API key and is
// scoped to the authenticated customer.
@Controller()
@UseGuards(ApiKeyGuard)
export class SignController {
  constructor(
    private readonly signService: SignService,
    private readonly postgres: PostgresService,
    private readonly audit: AuditService,
    private readonly prepare: PrepareService,
  ) {}

  @Post('sign')
  @HttpCode(HttpStatus.OK)
  async sign(
    @CurrentCustomer() customer: Customer,
    @Body() req: SignRequestDto,
  ) {
    return this.signService.sign(customer, req);
  }

  // Suggests nonce/gas/fees (from live network data) for building a sign
  // request. Requires an RPC endpoint to be configured.
  @Get('prepare')
  async prepareTx(
    @Query('to') to: string,
    @Query('data') data?: string,
    @Query('value') value?: string,
  ) {
    return this.prepare.prepare({ to, data, value });
  }

  @Get('transactions')
  async listTransactions(@CurrentCustomer() customer: Customer) {
    return this.postgres.listTransactions(customer.customer_id);
  }

  @Get('transactions/:requestId')
  async getTransaction(
    @CurrentCustomer() customer: Customer,
    @Param('requestId') requestId: string,
  ) {
    const tx = await this.postgres.getTransaction(
      requestId,
      customer.customer_id,
    );
    if (!tx) throw new NotFoundException('transaction not found');
    return tx;
  }

  @Get('transactions/:requestId/audit')
  async getAuditTrail(
    @CurrentCustomer() customer: Customer,
    @Param('requestId') requestId: string,
  ) {
    return this.audit.getAuditTrail(requestId, customer.customer_id);
  }
}
