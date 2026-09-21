import { Module } from '@nestjs/common';
import { DatabaseModule } from '../database/database.module';
import { CustomersModule } from '../customers/customers.module';
import { TokenRegistryService } from './token-registry.service';
import { EvmRpcService } from './evm-rpc.service';
import { AdminTokensController, TokensController } from './tokens.controller';

// The registry of contracts the platform will move money through, and the
// per-chain RPC access that verifies them and reads balances.
//
// Exported because KeysModule needs both: the registry to resolve a symbol
// to a contract before building a transfer, and the RPC to read what a key
// holds.
@Module({
  imports: [DatabaseModule, CustomersModule],
  controllers: [AdminTokensController, TokensController],
  providers: [TokenRegistryService, EvmRpcService],
  exports: [TokenRegistryService, EvmRpcService],
})
export class TokensModule {}
