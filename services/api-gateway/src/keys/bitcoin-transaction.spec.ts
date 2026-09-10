import { BadRequestException, ServiceUnavailableException } from '@nestjs/common';
import { KeysService } from './keys.service';
import { PostgresService } from '../database/postgres.service';
import { PolicyService } from '../policies/policy.service';
import { KeysTemporalService } from './keys-temporal.service';
import { Customer } from '../customers/customer.service';
import { splitSignature } from './bitcoin-signer-client';

// Spending Bitcoin through the public API.
//
// The reason this route exists is the thing worth testing: before it, the
// only way to move Bitcoin was POST :keyId/sign, where the customer computes
// their own sighash and the platform signs a digest it cannot inspect. That
// route is gated per tenant precisely because policy cannot see inside a
// digest. So these tests care most about which fields reach the policy
// engine, and that nothing is signed when policy says no.

const customer: Customer = {
  customer_id: 'cust-1',
  name: 'demo',
  email: 'demo@x.io',
  status: 'active',
  tier: 'pro',
  policies: {},
  raw_digest_signing_enabled: false, // deliberately: this route must not need it
};

const bitcoinKey = {
  key_id: 'key-1',
  customer_id: 'cust-1',
  blockchain: 'bitcoin',
  status: 'active',
  threshold: 2,
  total_parties: 3,
  address: 'bcrt1qexample',
  public_key: '02' + 'ab'.repeat(32),
};

const ceremony = { ceremony_id: 'cer-1', total_parties: 3 };

const DEST = 'bcrt1qw508d6qejxtdg4y5r3zarvary0c5xw7kygt080';

// A stub mpc-signer plus healthy parties, on one fetch mock. Records what
// the gateway sent so the tests can assert on it.
interface SignerCalls {
  prepare: Array<Record<string, unknown>>;
  finalize: Array<Record<string, unknown>>;
}

function mockSigner(
  opts: {
    inputs?: number;
    prepareStatus?: number;
    prepareBody?: Record<string, unknown>;
    finalizeStatus?: number;
    unreachable?: boolean;
  } = {},
): SignerCalls {
  const calls: SignerCalls = { prepare: [], finalize: [] };
  const inputs = opts.inputs ?? 1;

  global.fetch = jest.fn(async (url: string | URL | Request, init?: RequestInit) => {
    const href = String(url);

    if (href.includes('/health')) {
      return { ok: true } as Response;
    }
    if (opts.unreachable) {
      throw new Error('connect ECONNREFUSED');
    }

    const body = init?.body ? JSON.parse(String(init.body)) : {};

    if (href.endsWith('/bitcoin/prepare')) {
      calls.prepare.push(body);
      const status = opts.prepareStatus ?? 200;
      const payload =
        opts.prepareBody ??
        (status === 200
          ? {
              selection: {
                selected: Array.from({ length: inputs }, (_, i) => ({
                  txid: String(i).repeat(64),
                  vout: 0,
                  amount: 100_000,
                  confirmations: 6,
                })),
                total_in: 100_000 * inputs,
                amount: 50_000,
                fee: 1_410,
                change: 100_000 * inputs - 51_410,
                virtual_size: 141,
                fee_rate: 10,
                change_dropped_to_fee: false,
              },
              plan: {
                unsigned_tx_hex: '0200',
                sighashes: Array.from({ length: inputs }, (_, i) => String(i + 1).repeat(64)),
                prev_scripts: Array.from({ length: inputs }, () => '0014' + '11'.repeat(20)),
                witness: Array.from({ length: inputs }, () => true),
                amounts: Array.from({ length: inputs }, () => 100_000),
              },
              balance: 100_000 * inputs,
              utxo_count: inputs,
              addresses: ['bcrt1qsegwit', 'mLegacy'],
            }
          : { error: 'insufficient funds: 1000 sats available, need 51410', balance: 1000 });
      return {
        ok: status === 200,
        status,
        text: async () => JSON.stringify(payload),
      } as Response;
    }

    if (href.endsWith('/bitcoin/finalize')) {
      calls.finalize.push(body);
      const status = opts.finalizeStatus ?? 200;
      const payload =
        status === 200
          ? { raw_tx_hex: '0200beef', txid: 'f'.repeat(64), broadcast: true, broadcast_txid: 'f'.repeat(64) }
          : { error: 'txn-mempool-conflict', raw_tx_hex: '0200beef', txid: 'f'.repeat(64) };
      return {
        ok: status === 200,
        status,
        text: async () => JSON.stringify(payload),
      } as Response;
    }

    throw new Error(`unexpected request to ${href}`);
  }) as unknown as typeof fetch;

  return calls;
}

function build(
  overrides: { key?: unknown; approved?: boolean; sign?: jest.Mock } = {},
) {
  const postgres = {
    getKey: jest.fn().mockResolvedValue(overrides.key === undefined ? bitcoinKey : overrides.key),
    getCompletedCeremonyForKey: jest.fn().mockResolvedValue(ceremony),
    findSigningRequestByIdempotencyKey: jest.fn().mockResolvedValue(null),
    createSigningRequest: jest.fn().mockResolvedValue(undefined),
    completeSigningRequest: jest.fn().mockResolvedValue(undefined),
    failSigningRequest: jest.fn().mockResolvedValue(undefined),
  } as unknown as PostgresService;
  const temporal = {
    signWithThreshold:
      overrides.sign ??
      jest.fn().mockResolvedValue({
        status: 'completed',
        signature: 'aa'.repeat(32) + 'bb'.repeat(32) + '01',
      }),
  } as unknown as KeysTemporalService;
  const policy = {
    evaluate: jest.fn().mockResolvedValue({
      approved: overrides.approved !== false,
      denials: overrides.approved === false ? ['amount_limit'] : [],
      requiresApproval: false,
      reason: 'x',
    }),
  } as unknown as PolicyService;
  return { service: new KeysService(postgres, temporal, policy), postgres, temporal, policy };
}

beforeEach(() => {
  process.env.BITCOIN_NETWORK = 'regtest';
  process.env.MPC_SIGNER_URL = 'http://signer:8080';
});
afterEach(() => {
  jest.restoreAllMocks();
  delete process.env.BITCOIN_NETWORK;
  delete process.env.MPC_SIGNER_URL;
});

describe('KeysService.sendBitcoin', () => {
  it('sends the destination and amount the customer asked for to the policy engine', async () => {
    mockSigner();
    const { service, policy } = build();

    await service.sendBitcoin(customer, 'key-1', { destination: DEST, amount: '50000' });

    expect(policy.evaluate).toHaveBeenCalledWith(
      expect.objectContaining({ to: DEST, value: '50000', customerId: 'cust-1' }),
    );
  });

  // The whole argument for this route over the raw-digest one: the platform
  // builds the transaction, so a policy decision cannot be about a
  // different transaction than the one that gets signed.
  it('spends without the raw-digest capability the sign route requires', async () => {
    mockSigner();
    const { service } = build();

    const result = await service.sendBitcoin(customer, 'key-1', {
      destination: DEST,
      amount: '50000',
    });

    expect(customer.raw_digest_signing_enabled).toBe(false);
    expect(result.txid).toBe('f'.repeat(64));
    expect(result.broadcast).toBe(true);
  });

  it('does not touch the node or the parties when policy denies the spend', async () => {
    const calls = mockSigner();
    const { service, temporal } = build({ approved: false });

    await expect(
      service.sendBitcoin(customer, 'key-1', { destination: DEST, amount: '50000' }),
    ).rejects.toBeInstanceOf(Error);

    expect(calls.prepare).toHaveLength(0);
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  // Each input is a separate digest and a separate ceremony. Sharing an
  // idempotency key across them would make the second ceremony return the
  // first one's signature -- a valid signature over the wrong digest, which
  // produces a transaction that spends nothing and says nothing about why.
  it('runs one ceremony per input, each with its own request id', async () => {
    const calls = mockSigner({ inputs: 3 });
    const { service, temporal } = build();

    await service.sendBitcoin(customer, 'key-1', {
      destination: DEST,
      amount: '50000',
      idempotencyKey: 'req-7',
    });

    const sign = temporal.signWithThreshold as jest.Mock;
    expect(sign).toHaveBeenCalledTimes(3);
    const requestIds = sign.mock.calls.map((c) => c[0].requestId);
    expect(new Set(requestIds).size).toBe(3);
    expect(requestIds).toEqual(['req-7:input-0', 'req-7:input-1', 'req-7:input-2']);

    // Every digest the plan asked for was signed, and in order.
    expect(sign.mock.calls.map((c) => c[0].message)).toEqual([
      '1'.repeat(64),
      '2'.repeat(64),
      '3'.repeat(64),
    ]);
    expect(calls.finalize[0].signatures).toHaveLength(3);
  });

  it('passes the ceremony signature to the signer as r and s, without the recovery byte', async () => {
    const calls = mockSigner();
    const { service } = build();

    await service.sendBitcoin(customer, 'key-1', { destination: DEST, amount: '50000' });

    expect(calls.finalize[0].signatures).toEqual([{ r: 'aa'.repeat(32), s: 'bb'.repeat(32) }]);
  });

  // A customer without enough Bitcoin has made a mistake they can fix.
  // Reporting it as a 500 makes it look like the platform is broken.
  it('reports insufficient funds as a client error', async () => {
    mockSigner({ prepareStatus: 400 });
    const { service } = build();

    await expect(
      service.sendBitcoin(customer, 'key-1', { destination: DEST, amount: '50000' }),
    ).rejects.toBeInstanceOf(BadRequestException);
  });

  it('reports an unreachable signer as unavailable, not as a bad request', async () => {
    mockSigner({ unreachable: true });
    const { service } = build();

    await expect(
      service.sendBitcoin(customer, 'key-1', { destination: DEST, amount: '50000' }),
    ).rejects.toBeInstanceOf(ServiceUnavailableException);
  });

  it('refuses a key that is not a Bitcoin key', async () => {
    mockSigner();
    const { service } = build({ key: { ...bitcoinKey, blockchain: 'ethereum' } });

    await expect(
      service.sendBitcoin(customer, 'key-1', { destination: DEST, amount: '50000' }),
    ).rejects.toBeInstanceOf(BadRequestException);
  });

  it('reports the fee, the change and what was spent', async () => {
    mockSigner({ inputs: 2 });
    const { service } = build();

    const result = await service.sendBitcoin(customer, 'key-1', {
      destination: DEST,
      amount: '50000',
    });

    expect(result.fee).toBe('1410');
    expect(result.fee_rate).toBe(10);
    expect(result.inputs).toBe(2);
    expect(result.balance_before).toBe('200000');
    expect(result.change_dropped_to_fee).toBe(false);
  });

  // Amounts are strings for a reason: a satoshi count above 2^53 cannot
  // survive a round trip through a JavaScript number, and a silently
  // rounded amount is a transaction that does not balance.
  it('refuses an amount too large to be an exact number of satoshis', async () => {
    mockSigner();
    const { service } = build();

    await expect(
      service.sendBitcoin(customer, 'key-1', { destination: DEST, amount: '99999999999999999' }),
    ).rejects.toBeInstanceOf(BadRequestException);
  });
});

// A custody platform that cannot say where to deposit is not usable. For
// Bitcoin the answer is two addresses: the same key is payable at both
// encodings, both are spendable, and publishing only one would leave the
// platform unable to explain a deposit made to the other.
describe('KeysService.getDepositAddresses', () => {
  it('returns both address forms and names a preferred one', async () => {
    global.fetch = jest.fn(async (url: string | URL | Request) => {
      expect(String(url)).toContain('/bitcoin/addresses');
      expect(String(url)).toContain('network=regtest');
      return {
        ok: true,
        status: 200,
        text: async () =>
          JSON.stringify({
            network: 'regtest',
            segwit: 'bcrt1qsegwit',
            legacy: 'mLegacy',
            preferred: 'bcrt1qsegwit',
          }),
      } as Response;
    }) as unknown as typeof fetch;
    const { service } = build();

    const result = await service.getDepositAddresses(customer, 'key-1');

    expect(result.addresses).toEqual({
      preferred: 'bcrt1qsegwit',
      segwit: 'bcrt1qsegwit',
      legacy: 'mLegacy',
    });
  });

  // An Ethereum key already has its address recorded, so this must not
  // reach for the Bitcoin signer at all.
  it('answers for an Ethereum key without calling the Bitcoin signer', async () => {
    const fetchMock = jest.fn();
    global.fetch = fetchMock as unknown as typeof fetch;
    const { service } = build({
      key: { ...bitcoinKey, blockchain: 'ethereum', address: '0xabc' },
    });

    const result = await service.getDepositAddresses(customer, 'key-1');

    expect(result.addresses.preferred).toBe('0xabc');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  // A key still in its DKG ceremony has no public key, so no address can
  // exist yet. Saying that is better than returning nothing.
  it('refuses a key whose ceremony has not finished', async () => {
    const { service } = build({
      key: { ...bitcoinKey, status: 'pending_dkg', public_key: null },
    });

    await expect(service.getDepositAddresses(customer, 'key-1')).rejects.toThrow(
      /ceremony/,
    );
  });
});

describe('splitSignature', () => {
  it('drops the recovery byte Bitcoin has no use for', () => {
    const sig = 'aa'.repeat(32) + 'bb'.repeat(32) + '1c';
    expect(splitSignature(sig)).toEqual({ r: 'aa'.repeat(32), s: 'bb'.repeat(32) });
  });

  it('tolerates a 0x prefix', () => {
    expect(splitSignature('0x' + 'aa'.repeat(32) + 'bb'.repeat(32) + '00').r).toBe('aa'.repeat(32));
  });

  it('refuses a signature too short to contain r and s', () => {
    expect(() => splitSignature('abcd')).toThrow(/expected at least 64 bytes/);
  });
});
