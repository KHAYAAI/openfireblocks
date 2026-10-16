import { Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { IdentityModule } from '../identity/identity.module';
import { KeysModule } from '../keys/keys.module';
import { ApprovalsModule } from '../approvals/approvals.module';
import { TransfersModule } from '../transfers/transfers.module';
import { OrgWalletsController, WalletsController } from './wallets.controller';
import { WalletsService } from './wallets.service';

// Per-user wallets. Sits above transfers, like sweeps: a user's transfer is
// submitted as an ordinary transfer.
@Module({
  imports: [CustomersModule, IdentityModule, KeysModule, ApprovalsModule, TransfersModule],
  controllers: [WalletsController, OrgWalletsController],
  providers: [WalletsService],
  exports: [WalletsService],
})
export class WalletsModule {}
