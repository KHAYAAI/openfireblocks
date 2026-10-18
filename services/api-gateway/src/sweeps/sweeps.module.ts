import { Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { IdentityModule } from '../identity/identity.module';
import { KeysModule } from '../keys/keys.module';
import { ApprovalsModule } from '../approvals/approvals.module';
import { TransfersModule } from '../transfers/transfers.module';
import { SweepsController } from './sweeps.controller';
import { SweepsService } from './sweeps.service';

// Sits above transfers: a sweep is submitted as a transfer.
@Module({
  imports: [CustomersModule, IdentityModule, KeysModule, ApprovalsModule, TransfersModule],
  controllers: [SweepsController],
  providers: [SweepsService],
  exports: [SweepsService],
})
export class SweepsModule {}
