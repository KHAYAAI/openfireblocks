import { Module } from '@nestjs/common';
import { HttpModule } from '@nestjs/axios';
import { SignController } from './sign.controller';
import { SignService } from './sign.service';
import { EthereumService } from '../blockchain/ethereum.service';

// Bundles the /sign endpoint with its HTTP client (to the MPC signer) and the
// Ethereum broadcast service. Database services come from the global DatabaseModule.
@Module({
  imports: [HttpModule.register({ timeout: 10000 })],
  controllers: [SignController],
  providers: [SignService, EthereumService],
})
export class SignModule {}
