import { Body, Controller, HttpCode, HttpStatus, Param, Post, UseGuards } from '@nestjs/common';
import { CustomerService } from '../customers/customer.service';
import { JwtAuthGuard } from '../identity/jwt-auth.guard';
import { RequireTenantRole, TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CAN_INITIATE, CAN_MANAGE } from '../approvals/roles';
import { CreateKeyRequest } from '../keys/dto/create-key.dto';
import { BitcoinTransactionDto } from '../keys/dto/bitcoin-transaction.dto';
import { SolanaTransactionDto } from '../keys/dto/solana-transaction.dto';
import { CosmosTransactionDto } from '../keys/dto/cosmos-transaction.dto';
import { KeysService } from '../keys/keys.service';
import { validateCreateKey } from '../keys/create-key.validation';

// Creating keys and sending from them, for a named person in the console.
//
// The same KeysService the API-key routes call -- one implementation of
// policy, Travel Rule, ceremonies and relay -- behind the role model:
// creating a key is an admin action; starting a transfer is for anyone who
// may initiate one. EVM transfers do not go through here: they start as a
// settlement (POST /organisations/:id/settlements), which is the path that
// routes a high-value transfer to named approvers.
//
// Bitcoin, Solana and Cosmos spends are evaluated by policy and refused
// outright if policy says a transfer needs approval: those chains have no
// approval step yet, and refusing is the safe answer to "no way to ask".
@Controller('organisations/:customerId/keys')
@UseGuards(JwtAuthGuard, TenantRoleGuard)
export class OrgKeysController {
  constructor(
    private readonly keys: KeysService,
    private readonly customers: CustomerService,
  ) {}

  @Post()
  @HttpCode(HttpStatus.CREATED)
  @RequireTenantRole(...CAN_MANAGE)
  async create(@Param('customerId') customerId: string, @Body() req: CreateKeyRequest) {
    validateCreateKey(req);
    return this.keys.createKey(await this.customers.getByCustomerId(customerId), req);
  }

  @Post(':keyId/bitcoin-transactions')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_INITIATE)
  async sendBitcoin(@Param('customerId') customerId: string, @Param('keyId') keyId: string, @Body() req: BitcoinTransactionDto) {
    return this.keys.sendBitcoin(await this.customers.getByCustomerId(customerId), keyId, req);
  }

  @Post(':keyId/solana-transactions')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_INITIATE)
  async sendSolana(@Param('customerId') customerId: string, @Param('keyId') keyId: string, @Body() req: SolanaTransactionDto) {
    return this.keys.sendSolana(await this.customers.getByCustomerId(customerId), keyId, req);
  }

  @Post(':keyId/cosmos-transactions')
  @HttpCode(HttpStatus.OK)
  @RequireTenantRole(...CAN_INITIATE)
  async sendCosmos(@Param('customerId') customerId: string, @Param('keyId') keyId: string, @Body() req: CosmosTransactionDto) {
    return this.keys.sendCosmos(await this.customers.getByCustomerId(customerId), keyId, req);
  }
}
