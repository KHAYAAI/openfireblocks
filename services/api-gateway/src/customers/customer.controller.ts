import {
  Body,
  Controller,
  Get,
  Param,
  Post,
  Put,
  UseGuards,
} from '@nestjs/common';
import {
  IsBoolean,
  IsEmail,
  IsIn,
  IsInt,
  IsObject,
  IsOptional,
  IsString,
  Max,
  Min,
} from 'class-validator';
import { CustomerService } from './customer.service';
import { AdminGuard } from '../auth/admin.guard';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from './customer.service';
import { AuditService } from '../database/audit.service';
import { randomUUID } from 'crypto';

export class RotateApiKeyDto {
  // Optional lifetime for the new key, in days.
  @IsOptional()
  @IsInt()
  @Min(1)
  @Max(730)
  ttlDays?: number;
}

// Self-service rotation for a tenant that suspects its key was captured
// (AUTH-01): authenticated with the current key, it issues a new one and the
// old one stops working immediately. Before this, the only remedy was
// suspending the whole tenant through an admin-only route that the public edge
// does not even expose.
@Controller('api-key')
@UseGuards(ApiKeyGuard)
export class ApiKeyController {
  constructor(
    private readonly customers: CustomerService,
    private readonly audit: AuditService,
  ) {}

  @Post('rotate')
  async rotate(@CurrentCustomer() customer: Customer, @Body() dto: RotateApiKeyDto) {
    const result = await this.customers.rotateApiKey(customer.customer_id, dto.ttlDays);
    await this.audit.logEvent({
      type: 'API_KEY_ROTATED',
      requestId: randomUUID(),
      customerId: customer.customer_id,
      message: `rotated by the tenant${result.expires_at ? `; expires ${result.expires_at}` : ''}`,
      status: 'ok',
    });
    return result;
  }
}

class CreateCustomerDto {
  @IsEmail()
  email: string;

  @IsOptional()
  @IsString()
  name?: string;

  @IsOptional()
  @IsString()
  customerId?: string;

  @IsOptional()
  @IsIn(['free', 'pro', 'enterprise'])
  tier?: string;
}

class UpdatePoliciesDto {
  @IsObject()
  policies: Record<string, unknown>;
}

// Granting this accepts that policy on POST /keys/:keyId/sign evaluates a
// caller's *claim* about an opaque digest rather than the digest itself.
class SetRawDigestSigningDto {
  @IsBoolean()
  enabled: boolean;
}

// Must match customers_status_check (migration 001) exactly -- there is
// no 'deleted' status in the real schema (this previously listed one that
// doesn't exist, which would have failed the CHECK constraint on every
// real attempt to use it).
class SetStatusDto {
  @IsIn(['active', 'inactive', 'suspended'])
  status: string;
}

// Admin-only tenant management endpoints (protected by AdminGuard / ADMIN_API_KEY).
@Controller('admin/customers')
@UseGuards(AdminGuard)
export class CustomerController {
  constructor(
    private readonly customers: CustomerService,
    private readonly audit: AuditService,
  ) {}

  @Post()
  create(@Body() dto: CreateCustomerDto) {
    return this.customers.createCustomer(dto);
  }

  @Get()
  list() {
    return this.customers.list();
  }

  @Get(':customerId')
  get(@Param('customerId') customerId: string) {
    return this.customers.getByCustomerId(customerId);
  }

  @Put(':customerId/policies')
  async updatePolicies(
    @Param('customerId') customerId: string,
    @Body() dto: UpdatePoliciesDto,
  ) {
    await this.customers.updatePolicies(customerId, dto.policies);
    return { customerId, policies: dto.policies };
  }

  // Grants or revokes the weaker opaque-digest signing route. See
  // KeysService.signWithKey and migration 017 for why it is off by default.
  @Put(':customerId/raw-digest-signing')
  async setRawDigestSigning(
    @Param('customerId') customerId: string,
    @Body() dto: SetRawDigestSigningDto,
  ) {
    await this.customers.setRawDigestSigning(customerId, dto.enabled);
    return { customerId, raw_digest_signing_enabled: dto.enabled };
  }

  // Admin recovery: issues a new key for a tenant that has lost control of its
  // own, invalidating the old one.
  @Post(':customerId/api-key/rotate')
  async rotateApiKey(@Param('customerId') customerId: string, @Body() dto: RotateApiKeyDto) {
    const result = await this.customers.rotateApiKey(customerId, dto.ttlDays);
    await this.audit.logEvent({
      type: 'API_KEY_ROTATED',
      requestId: randomUUID(),
      customerId,
      message: 'rotated by an administrator',
      status: 'ok',
    });
    return result;
  }

  @Put(':customerId/status')
  async setStatus(
    @Param('customerId') customerId: string,
    @Body() dto: SetStatusDto,
  ) {
    await this.customers.setStatus(customerId, dto.status);
    return { customerId, status: dto.status };
  }
}
