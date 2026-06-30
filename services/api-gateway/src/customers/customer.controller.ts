import {
  Body,
  Controller,
  Get,
  Param,
  Post,
  Put,
  UseGuards,
} from '@nestjs/common';
import { IsEmail, IsIn, IsObject, IsOptional, IsString } from 'class-validator';
import { CustomerService } from './customer.service';
import { AdminGuard } from '../auth/admin.guard';

class CreateCustomerDto {
  @IsEmail()
  email: string;

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

class SetStatusDto {
  @IsIn(['active', 'suspended', 'deleted'])
  status: string;
}

// Admin-only tenant management endpoints (protected by AdminGuard / ADMIN_API_KEY).
@Controller('admin/customers')
@UseGuards(AdminGuard)
export class CustomerController {
  constructor(private readonly customers: CustomerService) {}

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

  @Put(':customerId/status')
  async setStatus(
    @Param('customerId') customerId: string,
    @Body() dto: SetStatusDto,
  ) {
    await this.customers.setStatus(customerId, dto.status);
    return { customerId, status: dto.status };
  }
}
