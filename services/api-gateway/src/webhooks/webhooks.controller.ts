import {
  BadRequestException,
  Body,
  Controller,
  Delete,
  Get,
  Post,
  Query,
  ServiceUnavailableException,
  UseGuards,
} from '@nestjs/common';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { CurrentCustomer } from '../auth/current-customer.decorator';
import { Customer } from '../customers/customer.service';

// Where a customer says how they want to be told.
//
// The webhooks service holds the endpoints and does the delivering, but it
// has no ingress and no notion of an API key -- it trusts an X-Customer-ID
// header, which is only safe because nothing outside the cluster can set
// it. So the customer-facing surface lives here, behind the same API key
// guard as everything else, and the tenant identity is attached from the
// authenticated customer rather than taken from the request.
//
// That is the whole reason this controller is a proxy rather than a
// redirect: the boundary between "the caller claims to be tenant X" and
// "the platform knows this is tenant X" is exactly here.

@Controller('webhooks')
@UseGuards(ApiKeyGuard)
export class WebhooksController {
  private readonly baseUrl = process.env.WEBHOOKS_URL ?? '';

  private async call(
    method: string,
    path: string,
    customerId: string,
    body?: unknown,
  ): Promise<{ status: number; parsed: unknown }> {
    if (!this.baseUrl) {
      throw new ServiceUnavailableException(
        'this deployment has no webhooks service, so endpoints cannot be registered',
      );
    }

    let response: Response;
    try {
      response = await fetch(`${this.baseUrl}${path}`, {
        method,
        headers: {
          'Content-Type': 'application/json',
          // Attached here, from the authenticated customer. A caller
          // cannot set it: the guard resolves the API key to a tenant and
          // this is the only place the header is written.
          'X-Customer-ID': customerId,
        },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(10_000),
      });
    } catch (err) {
      throw new ServiceUnavailableException(
        `the webhooks service is unreachable: ${(err as Error).message}`,
      );
    }

    const text = await response.text();
    let parsed: unknown = null;
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch {
        // The service reports validation failures as plain text.
        parsed = { error: text.trim() };
      }
    }
    return { status: response.status, parsed };
  }

  private unwrap({ status, parsed }: { status: number; parsed: unknown }): unknown {
    if (status >= 200 && status < 300) {
      return parsed;
    }
    const message =
      (parsed as { error?: string } | null)?.error ?? `the webhooks service returned ${status}`;
    // A rejected registration is the customer's mistake to fix -- an
    // unknown event name, a plaintext URL -- so it comes back as a 400
    // carrying the reason rather than as an opaque failure.
    if (status >= 400 && status < 500) {
      throw new BadRequestException(message);
    }
    throw new ServiceUnavailableException(message);
  }

  @Post()
  async register(
    @CurrentCustomer() customer: Customer,
    @Body() body: { url?: string; events?: string[]; secret?: string },
  ) {
    return this.unwrap(await this.call('POST', '/v1/webhooks', customer.customer_id, body));
  }

  @Get()
  async list(@CurrentCustomer() customer: Customer) {
    return this.unwrap(await this.call('GET', '/v1/webhooks', customer.customer_id));
  }

  @Delete()
  async remove(
    @CurrentCustomer() customer: Customer,
    @Query('webhook_id') webhookId: string,
  ) {
    return this.unwrap(
      await this.call(
        'DELETE',
        `/v1/webhooks?webhook_id=${encodeURIComponent(webhookId ?? '')}`,
        customer.customer_id,
      ),
    );
  }

  // What a customer's deliveries actually did. The question asked after
  // "why did my endpoint not fire", and unanswerable until now.
  @Get('deliveries')
  async deliveries(
    @CurrentCustomer() customer: Customer,
    @Query('webhook_id') webhookId: string,
  ) {
    return this.unwrap(
      await this.call(
        'GET',
        `/v1/deliveries?webhook_id=${encodeURIComponent(webhookId ?? '')}`,
        customer.customer_id,
      ),
    );
  }
}
