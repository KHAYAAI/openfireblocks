import { Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { KeysModule } from '../keys/keys.module';
import { ApprovalsModule } from '../approvals/approvals.module';
import { TokensModule } from '../tokens/tokens.module';
import { OrgTransfersController } from './org-transfers.controller';
import { ApiTransfersController } from './api-transfers.controller';
import { TransfersService } from './transfers.service';

// Starting a transfer from a threshold key, and acting on its approval.
// Needs the keys module (to sign) and the approvals module (to ask), which
// cannot import each other, so it sits above both.
@Module({
  imports: [CustomersModule, KeysModule, ApprovalsModule, TokensModule],
  controllers: [OrgTransfersController, ApiTransfersController],
  providers: [TransfersService],
})
export class TransfersModule {}
