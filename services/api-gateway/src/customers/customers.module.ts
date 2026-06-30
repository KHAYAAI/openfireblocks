import { Module } from '@nestjs/common';
import { CustomerService } from './customer.service';
import { CustomerController } from './customer.controller';
import { ApiKeyGuard } from '../auth/api-key.guard';

// Tenant management. Exports CustomerService + ApiKeyGuard so other modules
// (e.g. SignModule) can authenticate requests.
@Module({
  controllers: [CustomerController],
  providers: [CustomerService, ApiKeyGuard],
  exports: [CustomerService, ApiKeyGuard],
})
export class CustomersModule {}
