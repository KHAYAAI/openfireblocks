import { BadGatewayException, Injectable, ServiceUnavailableException } from '@nestjs/common';

export interface CardOnFile {
  configured: boolean;
  has_card: boolean;
  card?: { brand: string; last4: string; exp_month: number; exp_year: number };
}

// The gateway's side of services/billing, over HTTP with the shared bearer
// token. It exists so the console can show the card on file and send the
// customer to Stripe's hosted page to save one; nothing here sees a card
// number. Fails closed: with no BILLING_URL or token it says billing is not
// configured rather than guessing.
@Injectable()
export class BillingClient {
  private base(): string {
    const url = (process.env.BILLING_URL ?? '').trim().replace(/\/+$/, '');
    if (!url || !process.env.BILLING_API_TOKEN) {
      throw new ServiceUnavailableException('billing is not configured on this deployment (BILLING_URL, BILLING_API_TOKEN)');
    }
    return url;
  }

  private async call<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
    const base = this.base();
    let res: Response;
    try {
      res = await fetch(`${base}${path}`, {
        method,
        headers: { authorization: `Bearer ${process.env.BILLING_API_TOKEN}`, 'content-type': 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(15_000),
      });
    } catch (e) {
      throw new BadGatewayException(`the billing service is unreachable: ${(e as Error).message}`);
    }
    const text = await res.text();
    if (!res.ok) {
      // The billing service's own message is written for operators and says
      // what is wrong ("no payment processor is configured"); pass it on.
      const msg = text.trim().slice(0, 300) || res.statusText;
      if (res.status === 503) throw new ServiceUnavailableException(msg);
      throw new BadGatewayException(`billing said ${res.status}: ${msg}`);
    }
    return (text ? JSON.parse(text) : {}) as T;
  }

  cardOnFile(customerId: string): Promise<CardOnFile> {
    return this.call('GET', `/v1/billing/card?customer_id=${encodeURIComponent(customerId)}`);
  }

  cardSession(args: { customerId: string; email: string; name: string; successUrl: string; cancelUrl: string }): Promise<{ session_id: string; url: string }> {
    return this.call('POST', '/v1/billing/card-session', {
      customer_id: args.customerId, email: args.email, name: args.name, success_url: args.successUrl, cancel_url: args.cancelUrl,
    });
  }
}
