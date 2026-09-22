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
import { mtlsHttpOptions } from '../common/mtls';

// Bundles the tenant-facing signing API with its MPC-signer HTTP client, the
// Ethereum broadcast service, tenant auth (CustomersModule) and policy checks.
// Database + metrics services come from the global modules.
//
// The HTTP client presents a client certificate when
// MTLS_CERT_FILE/MTLS_KEY_FILE/MTLS_CA_FILE are set: this is the link that
// carries transactions to the signer, so it is the highest-value one in the
// gateway to authenticate and encrypt.
@Module({
  imports: [
    HttpModule.register({ timeout: 10000, ...mtlsHttpOptions() }),
    CustomersModule,
    PolicyModule,
    RiskModule,
    BillingModule,
  ],
  controllers: [SignController],
  providers: [SignService, EthereumService, PrepareService],
})
export class SignModule {}
