import { Module } from '@nestjs/common';
import { KeysController } from './keys.controller';
import { KeysService } from './keys.service';
import { CustomersModule } from '../customers/customers.module';

// Threshold key lifecycle: creation (kicks off a DKG ceremony), listing, and
// share-distribution status. Signing itself lives in SignModule.
@Module({
  imports: [CustomersModule],
  controllers: [KeysController],
  providers: [KeysService],
})
export class KeysModule {}
