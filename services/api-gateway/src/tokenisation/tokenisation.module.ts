import { Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { IdentityModule } from '../identity/identity.module';
import { KeysModule } from '../keys/keys.module';
import { ApprovalsModule } from '../approvals/approvals.module';
import { TransfersModule } from '../transfers/transfers.module';
import { TokensModule } from '../tokens/tokens.module';
import { TokenisationController } from './tokenisation.controller';
import { TokenisationService } from './tokenisation.service';

@Module({
  imports: [CustomersModule, IdentityModule, KeysModule, ApprovalsModule, TransfersModule, TokensModule],
  controllers: [TokenisationController],
  providers: [TokenisationService],
  exports: [TokenisationService],
})
export class TokenisationModule {}
