import { ConflictException, ServiceUnavailableException } from '@nestjs/common';
import { KeysService } from './keys.service';
import { PostgresService } from '../database/postgres.service';
import { PolicyService } from '../policies/policy.service';
import { KeysTemporalService } from './keys-temporal.service';
import { Customer } from '../customers/customer.service';

// The record that a signature was asked for, and the idempotency key that
// finally means something.
//
// Nothing wrote signing_requests. Several things read it: GET
// /keys/:id/details showed an empty signing history, compliance counted
// structuring signals in a permanently empty table, and settlement could
// not resolve the request a settlement was for. Underneath all of that,
// "what was signed, by whom, when" is the record a custody platform exists
// to keep, and it was not being kept.
//
// And every signing route accepted an idempotencyKey while ignoring it, so
// a client retrying a timed-out call ran a second threshold ceremony --
// for Bitcoin, a second broadcast.

const customer: Customer = {
  customer_id: 'cust-1',
  name: 'demo',
  email: 'demo@x.io',
  status: 'active',
  tier: 'pro',
  policies: {},
  raw_digest_signing_enabled: true,
};

const activeKey = {
  key_id: 'key-1',
  customer_id: 'cust-1',
  blockchain: 'ethereum',
  status: 'active',
  threshold: 2,
  total_parties: 3,
  address: '0x1111111111111111111111111111111111111111',
  public_key: '02' + 'ab'.repeat(32),
};

const ceremony = { ceremony_id: 'cer-1', total_parties: 3 };
const DIGEST = 'a'.repeat(64);
const SIGNATURE = 'ff'.repeat(65);

const signRequest = {
  message: DIGEST,
  to: '0x2222222222222222222222222222222222222222',
  value: '1000',
  chainId: 1,
};

function build(
  overrides: { existing?: unknown; sign?: jest.Mock; createSigningRequest?: jest.Mock } = {},
) {
  const postgres = {
    getKey: jest.fn().mockResolvedValue(activeKey),
    getCompletedCeremonyForKey: jest.fn().mockResolvedValue(ceremony),
    findSigningRequestByIdempotencyKey: jest
      .fn()
      .mockResolvedValue(overrides.existing ?? null),
    createSigningRequest: overrides.createSigningRequest ?? jest.fn().mockResolvedValue(undefined),
    completeSigningRequest: jest.fn().mockResolvedValue(undefined),
    failSigningRequest: jest.fn().mockResolvedValue(undefined),
  } as unknown as PostgresService;
  const temporal = {
    signWithThreshold:
      overrides.sign ??
      jest.fn().mockResolvedValue({ status: 'completed', signature: SIGNATURE }),
  } as unknown as KeysTemporalService;
  const policy = {
    evaluate: jest.fn().mockResolvedValue({
      approved: true,
      denials: [],
      requiresApproval: false,
      reason: 'ok',
    }),
  } as unknown as PolicyService;
  return { service: new KeysService(postgres, temporal, policy), postgres, temporal, policy };
}

beforeEach(() => {
  global.fetch = jest.fn(async () => ({ ok: true }) as Response) as unknown as typeof fetch;
});
afterEach(() => jest.restoreAllMocks());

describe('the signing audit trail', () => {
  it('records the request before the ceremony runs, not after it succeeds', async () => {
    const order: string[] = [];
    const postgresCreate = jest.fn(async () => {
      order.push('recorded');
    });
    const sign = jest.fn(async () => {
      order.push('signed');
      return { status: 'completed', signature: SIGNATURE };
    });
    const { service } = build({ sign, createSigningRequest: postgresCreate });

    await service.signWithKey(customer, 'key-1', { ...signRequest, idempotencyKey: 'req-1' });

    // A ceremony that starts and never returns is exactly the event an
    // audit trail has to contain, so the row cannot wait for success.
    expect(order).toEqual(['recorded', 'signed']);
  });

  it('records what was signed, with which key, for which tenant', async () => {
    const { service, postgres } = build();

    await service.signWithKey(customer, 'key-1', { ...signRequest, idempotencyKey: 'req-1' });

    // The row's request_id is a UUID of its own and the caller's
    // idempotency key is any string -- for a Bitcoin spend it is
    // "<key>:input-2". Forcing the second into the first made every
    // Bitcoin spend fail with "invalid input syntax for type uuid".
    expect(postgres.createSigningRequest).toHaveBeenCalledWith(
      expect.objectContaining({
        requestId: expect.stringMatching(
          /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
        ),
        customerId: 'cust-1',
        keyId: 'key-1',
        blockchain: 'ethereum',
        transactionHash: DIGEST,
        idempotencyKey: 'req-1',
      }),
    );
  });

  // Which parties signed is the question an auditor asks first: a
  // threshold signature only means something if you can say which shares
  // combined to produce it. The committee is chosen from whichever parties
  // are reachable, so it genuinely varies per request.
  it('records the committee that produced the signature, and the latency', async () => {
    const { service, postgres } = build();

    await service.signWithKey(customer, 'key-1', { ...signRequest, idempotencyKey: 'req-1' });

    // The same row that was opened, not a second one.
    const opened = (postgres.createSigningRequest as jest.Mock).mock.calls[0][0].requestId;
    expect(postgres.completeSigningRequest).toHaveBeenCalledWith(
      opened,
      'cust-1',
      expect.objectContaining({
        parties: [1, 2],
        latencyMs: expect.any(Number),
      }),
    );
  });

  it('records a failed ceremony as failed rather than leaving it in progress', async () => {
    const sign = jest.fn().mockResolvedValue({ status: 'failed', error: 'party 2 unreachable' });
    const { service, postgres } = build({ sign });

    await expect(
      service.signWithKey(customer, 'key-1', { ...signRequest, idempotencyKey: 'req-1' }),
    ).rejects.toBeInstanceOf(ServiceUnavailableException);

    const opened = (postgres.createSigningRequest as jest.Mock).mock.calls[0][0].requestId;
    expect(postgres.failSigningRequest).toHaveBeenCalledWith(
      opened,
      'cust-1',
      expect.stringContaining('party 2 unreachable'),
      expect.any(Number),
    );
  });

  it('records a workflow that threw as failed too', async () => {
    const sign = jest.fn().mockRejectedValue(new Error('Temporal connection refused'));
    const { service, postgres } = build({ sign });

    await expect(
      service.signWithKey(customer, 'key-1', { ...signRequest, idempotencyKey: 'req-1' }),
    ).rejects.toBeInstanceOf(ServiceUnavailableException);

    expect(postgres.failSigningRequest).toHaveBeenCalled();
  });

  // The signature exists and the caller is entitled to it. Losing the
  // audit row is a real problem, but it is not the caller's, and turning a
  // successful signature into an error would be worse than logging it.
  it('still returns the signature if the audit row cannot be written', async () => {
    const { service, postgres } = build();
    (postgres.completeSigningRequest as jest.Mock).mockRejectedValue(new Error('disk full'));

    const result = await service.signWithKey(customer, 'key-1', {
      ...signRequest,
      idempotencyKey: 'req-1',
    });

    expect(result.signature).toBe(SIGNATURE);
  });
});

describe('idempotency keys', () => {
  // A Bitcoin spend runs one ceremony per input and keys them
  // "<request>:input-N", which is not a UUID. The audit row's own id is,
  // so the two must not be the same value.
  it('accepts a non-UUID idempotency key', async () => {
    const { service, postgres } = build();

    await service.signWithKey(customer, 'key-1', {
      ...signRequest,
      idempotencyKey: 'abc-123:input-2',
    });

    expect(postgres.createSigningRequest).toHaveBeenCalledWith(
      expect.objectContaining({ idempotencyKey: 'abc-123:input-2' }),
    );
  });


  // The retry a client makes after a timeout should get the answer it
  // missed, not a second ceremony over the same money.
  it('replays a recorded signature instead of signing again', async () => {
    const { service, temporal } = build({
      existing: {
        request_id: 'req-1',
        status: 'completed',
        transaction_hash: DIGEST,
        signature: Buffer.from(SIGNATURE, 'hex'),
        signing_parties: [1, 3],
      },
    });

    const result = await service.signWithKey(customer, 'key-1', {
      ...signRequest,
      idempotencyKey: 'req-1',
    });

    expect(result.signature).toBe(SIGNATURE);
    expect(result.parties).toEqual([1, 3]);
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  // The dangerous case. Reusing a key for a different message must not
  // return the first message's signature -- that would hand the caller a
  // valid signature over something they did not ask to sign.
  it('refuses a key reused for a different message', async () => {
    const { service } = build({
      existing: {
        request_id: 'req-1',
        status: 'completed',
        transaction_hash: 'b'.repeat(64),
        signature: Buffer.from(SIGNATURE, 'hex'),
        signing_parties: [1, 2],
      },
    });

    await expect(
      service.signWithKey(customer, 'key-1', { ...signRequest, idempotencyKey: 'req-1' }),
    ).rejects.toBeInstanceOf(ConflictException);
  });

  it('refuses a retry while the first request is still running', async () => {
    const { service, temporal } = build({
      existing: { request_id: 'req-1', status: 'in_progress', transaction_hash: DIGEST },
    });

    await expect(
      service.signWithKey(customer, 'key-1', { ...signRequest, idempotencyKey: 'req-1' }),
    ).rejects.toBeInstanceOf(ConflictException);
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  // Two requests racing past the lookup: the unique constraint is what
  // actually settles it, so the loser has to be reported as a conflict
  // rather than a 500 carrying constraint text.
  it('turns the unique-constraint race into a conflict, not a server error', async () => {
    const duplicate = Object.assign(new Error('duplicate key value'), { code: '23505' });
    const { service } = build({
      createSigningRequest: jest.fn().mockRejectedValue(duplicate),
    });

    await expect(
      service.signWithKey(customer, 'key-1', { ...signRequest, idempotencyKey: 'req-1' }),
    ).rejects.toBeInstanceOf(ConflictException);
  });

  it('lets a failed request be retried', async () => {
    const { service, temporal } = build({
      existing: {
        request_id: 'req-1',
        status: 'failed',
        transaction_hash: DIGEST,
        error_message: 'party 2 unreachable',
      },
    });

    const result = await service.signWithKey(customer, 'key-1', {
      ...signRequest,
      idempotencyKey: 'req-1',
    });

    expect(result.signature).toBe(SIGNATURE);
    expect(temporal.signWithThreshold).toHaveBeenCalled();
  });
});
