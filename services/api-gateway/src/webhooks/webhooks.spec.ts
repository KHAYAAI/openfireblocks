import { WebhookEmitter } from './webhooks.service';

// Announcing without ever affecting the thing being announced.
//
// The delivery machinery in services/webhooks has been complete for a long
// time and nothing ever emitted an event to it, so a customer could not
// learn that their key was ready or their transaction was signed. They
// could only poll.
//
// The property that matters most here is the negative one: a customer's
// signature must not fail, or slow down, because their notification
// endpoint is down. The money has already moved by the time this runs.

describe('WebhookEmitter', () => {
  afterEach(() => {
    jest.restoreAllMocks();
    delete process.env.WEBHOOKS_URL;
  });

  it('posts the event to the webhooks service with the tenant and payload', async () => {
    process.env.WEBHOOKS_URL = 'http://webhooks:8086';
    const fetchMock = jest.fn(async () => ({ ok: true, status: 202 }) as Response);
    global.fetch = fetchMock as unknown as typeof fetch;

    await new WebhookEmitter().emit('cust-1', 'signature.created', { key_id: 'key-1' });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('http://webhooks:8086/v1/events');
    expect(JSON.parse(String(init.body))).toEqual({
      event_type: 'signature.created',
      customer_id: 'cust-1',
      data: { key_id: 'key-1' },
    });
  });

  // The one that matters. A notification endpoint that is down, slow, or
  // returning 500 must not turn a completed signature into a failure.
  it('never throws when the webhooks service is unreachable', async () => {
    process.env.WEBHOOKS_URL = 'http://webhooks:8086';
    global.fetch = jest
      .fn()
      .mockRejectedValue(new Error('connect ECONNREFUSED')) as unknown as typeof fetch;

    await expect(
      new WebhookEmitter().emit('cust-1', 'signature.created', {}),
    ).resolves.toBeUndefined();
  });

  it('never throws when the webhooks service rejects the event', async () => {
    process.env.WEBHOOKS_URL = 'http://webhooks:8086';
    global.fetch = jest.fn(
      async () => ({ ok: false, status: 500 }) as Response,
    ) as unknown as typeof fetch;

    await expect(
      new WebhookEmitter().emit('cust-1', 'key.created', {}),
    ).resolves.toBeUndefined();
  });

  // A deployment without the webhooks service still signs transactions. It
  // just cannot announce them, and logging that on every signature would
  // be noise rather than information.
  it('does nothing, quietly, when no webhooks service is configured', async () => {
    const fetchMock = jest.fn();
    global.fetch = fetchMock as unknown as typeof fetch;

    await new WebhookEmitter().emit('cust-1', 'signature.created', {});

    expect(fetchMock).not.toHaveBeenCalled();
  });
});
