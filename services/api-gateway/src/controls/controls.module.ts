import { Global, Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { IdentityModule } from '../identity/identity.module';
import { ApprovalsModule } from '../approvals/approvals.module';
import { AlertsService } from './alerts.service';
import { ControlsController } from './controls.controller';
import { ControlsService } from './controls.service';

// Global so the signing paths and the approval flow can ask the same
// questions without each module importing this one.
@Global()
@Module({
  imports: [CustomersModule, IdentityModule, ApprovalsModule],
  controllers: [ControlsController],
  providers: [ControlsService, AlertsService],
  exports: [ControlsService, AlertsService],
})
export class ControlsModule {}
