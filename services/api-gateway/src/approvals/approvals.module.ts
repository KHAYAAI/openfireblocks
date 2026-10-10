import { Module } from '@nestjs/common';
import { IdentityModule } from '../identity/identity.module';
import { CustomersModule } from '../customers/customers.module';
import { SettlementsModule } from '../settlements/settlements.module';
import { ApprovalsController } from './approvals.controller';
import { OrganisationBootstrapController } from './bootstrap.controller';
import { MeController } from './me.controller';
import { ConsoleController } from './console.controller';
import { ApprovalsService } from './approvals.service';
import { TenantRoleGuard } from './tenant-role.guard';
import { NativeApprovalHooks } from './native-approval-hooks';

// Segregation of duties: people, roles per organisation, approval policy
// and the approval queue. See migration 023 for the rules themselves.
@Module({
  imports: [IdentityModule, CustomersModule, SettlementsModule],
  controllers: [ApprovalsController, OrganisationBootstrapController, MeController, ConsoleController],
  providers: [ApprovalsService, TenantRoleGuard, NativeApprovalHooks],
  exports: [ApprovalsService, TenantRoleGuard, NativeApprovalHooks],
})
export class ApprovalsModule {}
