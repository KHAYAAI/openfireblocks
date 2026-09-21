import { Module } from '@nestjs/common';
import { DatabaseModule } from '../database/database.module';
import { CustomersModule } from '../customers/customers.module';
import { KeysModule } from '../keys/keys.module';
import { DashboardController } from './dashboard.controller';
import { DashboardService } from './dashboard.service';

// A web console, served by the API gateway itself.
//
// No separate service, no build step, no bundle. For a platform sold
// self-hosted that is the point: every additional artefact a customer has
// to build, serve, patch and get through their own security review is a
// reason for their platform team to say no. This ships inside a container
// they are already running.
//
// KeysModule is imported rather than reimplemented so the dashboard reads
// balances and addresses through exactly the code the API uses. A second
// implementation would be a second thing that can disagree about what a
// customer holds.
@Module({
  imports: [DatabaseModule, CustomersModule, KeysModule],
  controllers: [DashboardController],
  providers: [DashboardService],
})
export class DashboardModule {}
