import { Module } from '@nestjs/common';
import { WebhooksController } from './webhooks.controller';
import { WebhookEmitter } from './webhooks.service';
import { CustomersModule } from '../customers/customers.module';

// Two halves of the same thing: WebhooksController is how a customer says
// where to be told, WebhookEmitter is how the platform tells them.
//
// CustomersModule is imported because ApiKeyGuard resolves an API key to a
// tenant, and attaching that tenant -- rather than trusting a header --
// is the entire security boundary this controller exists to hold.
@Module({
  imports: [CustomersModule],
  controllers: [WebhooksController],
  providers: [WebhookEmitter],
  exports: [WebhookEmitter],
})
export class WebhooksModule {}
