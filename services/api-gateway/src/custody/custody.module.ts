import { Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { IdentityModule } from '../identity/identity.module';
import { KeysModule } from '../keys/keys.module';
import { ApprovalsModule } from '../approvals/approvals.module';
import { TransfersModule } from '../transfers/transfers.module';
import { CustodyController } from './custody.controller';
import { CustodyService } from './custody.service';

@Module({
  imports: [CustomersModule, IdentityModule, KeysModule, ApprovalsModule, TransfersModule],
  controllers: [CustodyController],
  providers: [CustodyService],
  exports: [CustodyService],
})
export class CustodyModule {}
