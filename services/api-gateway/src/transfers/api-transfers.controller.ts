import { Body, Controller, HttpCode, HttpStatus, NotFoundException, Param, Post, Res, UseGuards } from '@nestjs/common';
import type { Response } from 'express';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';
import { KeysService } from '../keys/keys.service';
import { kindOfBlockchain, parseTransfer } from './parse-transfer';
import { TransfersService } from './transfers.service';

// The same transfer route for a machine. There is no person to exclude from
// approving, so any approver in the organisation may. A transfer that needs
// approval answers 202 with the approval id, and its outcome is read from the
// approval.
@Controller('keys')
@UseGuards(ApiKeyGuard)
export class ApiTransfersController {
  constructor(
    private readonly keys: KeysService,
    private readonly transfers: TransfersService,
  ) {}

  @Post(':keyId/transfers')
  @HttpCode(HttpStatus.OK)
  async transfer(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
    @Body() body: Record<string, unknown>,
    @Res({ passthrough: true }) res: Response,
  ) {
    const key = await this.keys.getKey(keyId, customer.customer_id);
    if (!key) throw new NotFoundException(`no key ${keyId}`);
    const kind = kindOfBlockchain(key.blockchain);
    const dto = await parseTransfer(kind, body);
    const out = await this.transfers.submit(customer, keyId, kind, dto, { userId: null, label: 'api-key' });
    if (out.status === 'pending_approval') res.status(HttpStatus.ACCEPTED);
    return out;
  }
}
