import { Module } from '@nestjs/common';
import { HttpModule } from '@nestjs/axios';
import { SignController } from './sign.controller';
import { SignService } from './sign.service';
import { EthereumService } from '../blockchain/ethereum.service';
import { PrepareService } from '../blockchain/prepare.service';
import { CustomersModule } from '../customers/customers.module';
import { PolicyModule } from '../policies/policy.module';
import { RiskModule } from '../risk/risk.module';
import { BillingModule } from '../billing/billing.module';

// Bundles the tenant-facing signing API with its MPC-signer HTTP client, the
// Ethereum broadcast service, tenant auth (CustomersModule) and policy checks.
// Database + metrics services come from the global modules.
@Module({
  imports: [
    HttpModule.register({ timeout: 10000 }),
    CustomersModule,
    PolicyModule,
    RiskModule,
    BillingModule,
  ],
  controllers: [SignController],
  providers: [SignService, EthereumService, PrepareService],
})
export class SignModule {}
