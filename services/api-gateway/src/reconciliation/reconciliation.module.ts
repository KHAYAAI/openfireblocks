import { Module } from '@nestjs/common';
import { CustomersModule } from '../customers/customers.module';
import { TokensModule } from '../tokens/tokens.module';
import { ReconciliationController } from './reconciliation.controller';
import { ReconciliationService } from './reconciliation.service';

// The platform's ledger against the chain, and against a customer's books.
@Module({
  imports: [CustomersModule, TokensModule],
  controllers: [ReconciliationController],
  providers: [ReconciliationService],
})
export class ReconciliationModule {}
