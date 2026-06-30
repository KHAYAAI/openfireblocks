import { Module } from '@nestjs/common';
import { SettlementsController } from './settlements.controller';
import { TemporalService } from './temporal.service';
import { CustomersModule } from '../customers/customers.module';

// Durable settlement endpoints backed by the Temporal worker. CustomersModule
// provides the API-key guard.
@Module({
  imports: [CustomersModule],
  controllers: [SettlementsController],
  providers: [TemporalService],
})
export class SettlementsModule {}
