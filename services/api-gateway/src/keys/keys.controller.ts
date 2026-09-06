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
