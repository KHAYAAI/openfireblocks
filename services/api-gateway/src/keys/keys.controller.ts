import {
  Controller,
  Get,
  Post,
  Body,
  Param,
  Query,
  HttpCode,
  HttpStatus,
  NotFoundException,
  BadRequestException,
  UseGuards,
} from '@nestjs/common';
import { KeysService } from './keys.service';
import { CreateKeyRequest } from './dto/create-key.dto';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';
import { ThresholdSignRequestDto } from './dto/threshold-sign.dto';
import { SignTransactionDto } from './dto/sign-transaction.dto';
import { BitcoinTransactionDto } from './dto/bitcoin-transaction.dto';
import { TokenTransferDto } from './dto/token-transfer.dto';

@Controller('keys')
@UseGuards(ApiKeyGuard)
export class KeysController {
  constructor(private readonly keysService: KeysService) {}

  @Post()
  @HttpCode(HttpStatus.CREATED)
  async createKey(
    @CurrentCustomer() customer: Customer,
    @Body() req: CreateKeyRequest,
  ) {
    // Validate blockchain
    const validBlockchains = ['bitcoin', 'ethereum', 'solana', 'cosmos', 'polygon'];
    if (!validBlockchains.includes(req.blockchain)) {
      throw new BadRequestException(
        `Unsupported blockchain: ${req.blockchain}. Supported: ${validBlockchains.join(', ')}`,
      );
    }

    // Validate threshold
    if (req.threshold < 1 || req.total_parties < 1) {
      throw new BadRequestException('threshold and total_parties must be >= 1');
    }

    if (req.threshold > req.total_parties) {
      throw new BadRequestException(
        'threshold must be <= total_parties',
      );
    }

    // Validate threshold (at least k-of-n where k >= 2 for security)
    if (req.threshold < 2 && req.total_parties > 1) {
      throw new BadRequestException(
        'For multi-party keys, threshold must be >= 2',
      );
    }

    return this.keysService.createKey(customer, req);
  }

  // Sign with a key this customer provisioned. Distinct from POST /sign,
  // which routes to mpc-signer's separate single-key path -- this is the
  // only route that uses a threshold key produced by POST /keys.
  @Post(':keyId/sign')
  @HttpCode(HttpStatus.OK)
  async signWithKey(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
    @Body() req: ThresholdSignRequestDto,
  ) {
    return this.keysService.signWithKey(customer, keyId, req);
  }

  // Sign a transaction this service builds from the supplied fields.
  //
  // Prefer this over POST :keyId/sign for anything that is actually a
  // transaction: here the digest that gets signed is computed from the same
  // fields the policy engine evaluated, so policy governs what is really
  // being signed rather than what the caller says it is.
  @Post(':keyId/transactions')
  @HttpCode(HttpStatus.OK)
  async signTransaction(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
    @Body() req: SignTransactionDto,
  ) {
    return this.keysService.signTransaction(customer, keyId, req);
  }

  // Where to deposit so this key can spend it.
  @Get(':keyId/addresses')
  async getDepositAddresses(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
  ) {
    return this.keysService.getDepositAddresses(customer, keyId);
  }

  // Send a registered token -- a stablecoin, in practice -- from a
  // threshold key.
  //
  // Separate from :keyId/transactions for the same reason the Bitcoin
  // route is: there the caller supplies calldata and the platform decodes
  // it to find out what it does, which is sound but rests entirely on the
  // decoder being right about every encoding a caller might produce. Here
  // the caller supplies a token, a recipient and an amount, and the
  // platform encodes the call -- so there is nothing for the caller's
  // claim and the signed bytes to disagree about.
  @Post(':keyId/token-transfers')
  @HttpCode(HttpStatus.OK)
  async sendToken(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
    @Body() req: TokenTransferDto,
  ) {
    return this.keysService.sendToken(customer, keyId, req);
  }

  // What this key holds, in every registered token as well as the native
  // coin. Separate from :keyId/addresses because that answer is derived
  // from the public key and always available, while this one is a set of
  // live chain reads.
  @Get(':keyId/balances')
  async getBalances(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
    @Query('chainId') chainId: string,
  ) {
    const parsed = Number(chainId);
    if (!Number.isInteger(parsed) || parsed < 1) {
      throw new BadRequestException('chainId is required and must be a positive integer');
    }
    return this.keysService.getBalances(customer, keyId, parsed);
  }

  // Send Bitcoin from a threshold key.
  //
  // Separate from :keyId/transactions rather than a branch inside it
  // because the two chains need genuinely different inputs. Ethereum wants
  // a nonce, a gas limit and a fee, because an account model expects the
  // caller to know the account's state. Bitcoin has no such state to know:
  // which coins to spend and what fee to pay are the platform's job, and a
  // single endpoint taking the union of both would be mostly fields that
  // must not be set.
  @Post(':keyId/bitcoin-transactions')
  @HttpCode(HttpStatus.OK)
  async sendBitcoin(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
    @Body() req: BitcoinTransactionDto,
  ) {
    return this.keysService.sendBitcoin(customer, keyId, req);
  }

  @Get()
  async listKeys(@CurrentCustomer() customer: Customer) {
    return this.keysService.listKeys(customer.customer_id);
  }

  @Get(':keyId')
  async getKey(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
  ) {
    const key = await this.keysService.getKey(keyId, customer.customer_id);
    if (!key) {
      throw new NotFoundException(`Key ${keyId} not found`);
    }
    return key;
  }

  @Get(':keyId/details')
  async getKeyDetails(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
  ) {
    const details = await this.keysService.getKeyDetails(
      keyId,
      customer.customer_id,
    );
    if (!details) {
      throw new NotFoundException(`Key details for ${keyId} not found`);
    }
    return details;
  }

  @Get(':keyId/share-status')
  async getShareStatus(
    @CurrentCustomer() customer: Customer,
    @Param('keyId') keyId: string,
  ) {
    return this.keysService.getShareStatus(keyId, customer.customer_id);
  }
}
