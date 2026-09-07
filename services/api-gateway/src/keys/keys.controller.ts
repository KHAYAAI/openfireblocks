import {
  Controller,
  Get,
  Post,
  Body,
  Param,
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
