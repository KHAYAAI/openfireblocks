import { Module } from '@nestjs/common';
import { KeysController } from './keys.controller';
import { KeysService } from './keys.service';
import { KeysTemporalService } from './keys-temporal.service';
import { CustomersModule } from '../customers/customers.module';

// Threshold key lifecycle: creation (kicks off a real DKG ceremony via
// KeysTemporalService -> ProvisionKeyWorkflow), listing, and
// share-distribution status. Signing itself lives in SignModule.
@Module({
  imports: [CustomersModule],
  controllers: [KeysController],
  providers: [KeysService, KeysTemporalService],
})
export class KeysModule {}
