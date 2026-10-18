import { Module } from '@nestjs/common';
import { KeysModule } from '../keys/keys.module';
import { CustomersModule } from '../customers/customers.module';
import { TokensModule } from '../tokens/tokens.module';
import { ApprovalsModule } from '../approvals/approvals.module';
import { IdentityModule } from '../identity/identity.module';
import { AgentsService } from './agents.service';
import { AgentKeyGuard } from './agent-key.guard';
import { AgentController, AgentsAdminController } from './agents.controller';

@Module({
  imports: [KeysModule, CustomersModule, TokensModule, ApprovalsModule, IdentityModule],
  controllers: [AgentsAdminController, AgentController],
  providers: [AgentsService, AgentKeyGuard],
})
export class AgentsModule {}
