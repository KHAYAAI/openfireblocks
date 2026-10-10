import { BadGatewayException, ServiceUnavailableException } from '@nestjs/common';
import { BillingClient } from './billing-client.service';
import { OrgBillingController } from './org-billing.controller';

const realFetch = global.fetch;
afterEach(() => {
  global.fetch = realFetch;
  delete process.env.BILLING_URL;
  delete process.env.BILLING_API_TOKEN;
  delete process.env.CONSOLE_PUBLIC_URL;
});

function stubFetch(status: number, body: unknown) {
  const calls: Array<{ url: string; init: RequestInit }> = [];
  global.fetch = (async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    return new Response(typeof body === 'string' ? body : JSON.stringify(body), { status });
  }) as never;
  return calls;
}

describe('BillingClient', () => {
  it('refuses to guess when billing is not configured', async () => {
    await expect(new BillingClient().cardOnFile('c1')).rejects.toBeInstanceOf(ServiceUnavailableException);
    process.env.BILLING_URL = 'http://billing:8085';
    await expect(new BillingClient().cardOnFile('c1')).rejects.toBeInstanceOf(ServiceUnavailableException); // no token
  });

  it('calls billing with the bearer token', async () => {
    process.env.BILLING_URL = 'http://billing:8085/';
    process.env.BILLING_API_TOKEN = 't0ken';
    const calls = stubFetch(200, { configured: true, has_card: true, card: { brand: 'visa', last4: '4242', exp_month: 1, exp_year: 2030 } });
    const out = await new BillingClient().cardOnFile('a b');
    expect(out.card?.last4).toBe('4242');
    expect(calls[0].url).toBe('http://billing:8085/v1/billing/card?customer_id=a%20b');
    expect((calls[0].init.headers as Record<string, string>).authorization).toBe('Bearer t0ken');
  });

  it('passes the billing service\'s reason through, and calls an upstream fault a bad gateway', async () => {
    process.env.BILLING_URL = 'http://billing:8085';
    process.env.BILLING_API_TOKEN = 't';
    stubFetch(503, 'no payment processor is configured');
    await expect(new BillingClient().cardOnFile('c')).rejects.toThrow('no payment processor is configured');
    stubFetch(500, 'boom');
    await expect(new BillingClient().cardOnFile('c')).rejects.toBeInstanceOf(BadGatewayException);
  });
});

describe('OrgBillingController', () => {
  const customers = { getByCustomerId: async () => ({ name: 'Acme', email: 'ops@acme.example' }) } as never;

  it('returns Stripe\'s page and sends the customer back to the configured console, not to a caller-chosen address', async () => {
    process.env.BILLING_URL = 'http://billing:8085';
    process.env.BILLING_API_TOKEN = 't';
    process.env.CONSOLE_PUBLIC_URL = 'https://console.acme.example/';
    const calls = stubFetch(200, { session_id: 'cs_1', url: 'https://checkout.stripe.com/c/pay/cs_1' });
    const out = await new OrgBillingController(new BillingClient(), customers).cardSession('cust-1');
    expect(out).toEqual({ url: 'https://checkout.stripe.com/c/pay/cs_1' });
    const sent = JSON.parse(calls[0].init.body as string);
    expect(sent).toMatchObject({ customer_id: 'cust-1', email: 'ops@acme.example', name: 'Acme' });
    expect(sent.success_url).toBe('https://console.acme.example/console#/billing?card=saved');
    expect(sent.cancel_url).toBe('https://console.acme.example/console#/billing?card=cancelled');
  });

  it('is off until CONSOLE_PUBLIC_URL is an https address', async () => {
    process.env.BILLING_URL = 'http://billing:8085';
    process.env.BILLING_API_TOKEN = 't';
    const c = new OrgBillingController(new BillingClient(), customers);
    await expect(c.cardSession('cust-1')).rejects.toBeInstanceOf(ServiceUnavailableException);
    process.env.CONSOLE_PUBLIC_URL = 'http://console.acme.example';
    await expect(c.cardSession('cust-1')).rejects.toBeInstanceOf(ServiceUnavailableException);
  });
});
