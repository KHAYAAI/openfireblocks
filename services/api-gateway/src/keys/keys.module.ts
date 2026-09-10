import { Module } from '@nestjs/common';
import { KeysController } from './keys.controller';
import { KeysService } from './keys.service';
import { KeysTemporalService } from './keys-temporal.service';
import { CustomersModule } from '../customers/customers.module';
import { PolicyModule } from '../policies/policy.module';
import { WebhookEmitter } from '../webhooks/webhooks.service';

// Threshold key lifecycle: creation (kicks off a real DKG ceremony via
// KeysTemporalService -> ProvisionKeyWorkflow), listing, share-distribution
// status, and threshold signing with a provisioned key.
//
// SignModule is a different thing despite the name: POST /sign there
// routes to mpc-signer, the single-key non-threshold service. Signing with
// a key that POST /keys created lives here, because it needs this module's
// ceremony lookup and Temporal client. PolicyModule is imported so that
// path is gated by the same fail-closed policy evaluation.
@Module({
  imports: [CustomersModule, PolicyModule],
  controllers: [KeysController],
  providers: [KeysService, KeysTemporalService, WebhookEmitter],
})
export class KeysModule {}
