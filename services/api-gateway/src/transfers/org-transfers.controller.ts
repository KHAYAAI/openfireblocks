import { Body, Controller, HttpCode, HttpStatus, NotFoundException, Param, Post, Res, UseGuards } from '@nestjs/common';
import type { Response } from 'express';
import { CustomerService } from '../customers/customer.service';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { CurrentUser } from '../identity/current-user.decorator';
import { JwtClaims } from '../identity/auth.service';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_INITIATE, CAN_MANAGE } from '../approvals/roles';
import { CreateKeyRequest } from '../keys/dto/create-key.dto';
import { KeysService } from '../keys/keys.service';
import { validateCreateKey } from '../keys/create-key.validation';
import { kindOfBlockchain, parseTransfer } from './parse-transfer';
import { TransfersService } from './transfers.service';

// Creating keys and sending from them, for a named person in the console.
//
// A transfer is one route whatever the chain, because the key already says
// which chain it is. It either completes (200) or, when policy says people
// must sign off, is parked and an approval is opened for the organisation's
// approvers (202). The person who asked can never approve it: the approval
// records who initiated it and the database refuses that person's decision.
@Controller('organisations/:customerId/keys')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class OrgTransfersController {
  constructor(
    private readonly keys: KeysService,
    private readonly transfers: TransfersService,
    private readonly customers: CustomerService,
  ) {}

  @Post()
  @HttpCode(HttpStatus.CREATED)
  @RequireTenantRole(...CAN_MANAGE)
  async create(@Param('customerId') customerId: string, @Body() req: CreateKeyRequest) {
    validateCreateKey(req);
    return this.keys.createKey(await this.customers.getByCustomerId(customerId), req);
  }

  @Post(':keyId/transfers')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_INITIATE)
  async transfer(
    @Param('customerId') customerId: string,
    @Param('keyId') keyId: string,
    @CurrentUser() claims: JwtClaims,
    @Body() body: Record<string, unknown>,
    @Res({ passthrough: true }) res: Response,
  ) {
    const key = await this.keys.getKey(keyId, customerId);
    if (!key) throw new NotFoundException(`no key ${keyId}`);
    const kind = kindOfBlockchain(key.blockchain);
    const dto = await parseTransfer(kind, body);
    const out = await this.transfers.submit(await this.customers.getByCustomerId(customerId), keyId, kind, dto, {
      userId: claims.sub,
      label: claims.email,
    });
    if (out.status === 'pending_approval') res.status(HttpStatus.ACCEPTED);
    return out;
  }
}
