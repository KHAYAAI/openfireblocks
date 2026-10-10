import { BadRequestException, ServiceUnavailableException } from '@nestjs/common';
import { Transaction, Wallet, getAddress } from 'ethers';
import { KeysService } from './keys.service';
import { PostgresService } from '../database/postgres.service';
import { PolicyService } from '../policies/policy.service';
import { KeysTemporalService } from './keys-temporal.service';
import { TokenRegistryService, Token } from '../tokens/token-registry.service';
import { EvmRpcService } from '../tokens/evm-rpc.service';
import { Customer } from '../customers/customer.service';
import { decodeErc20Call, encodeTransfer } from './erc20';

// Moving a stablecoin, and the controls seeing what moved.
//
// The defect these tests are written against: an ERC-20 transfer puts its
// recipient and amount inside the calldata, so the transaction's own `to`
// is the token contract and its `value` is zero. The gateway handed those
// two fields to the policy engine. The result was not a loose limit -- it
// was that a fifty million rand transfer and a one rand transfer produced
// byte-identical policy input, and the counterparty whitelist compared the
// same contract address every time.
//
// So the assertions here are mostly about what the policy engine is *told*,
// because that is where the bug lived.

const USDC_CONTRACT = '0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48';
const ZARP_CONTRACT = '0x1234567890123456789012345678901234567890';
const RECIPIENT = '0x70997970C51812dc3A010C7d01b50e0d17dc79C8';

const wallet = new Wallet(
  '0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d',
);

const customer: Customer = {
  customer_id: '11111111-1111-4111-8111-111111111111',
  name: 'demo',
  email: 'demo@x.io',
  status: 'active',
  tier: 'enterprise',
  policies: {},
  raw_digest_signing_enabled: false,
  arbitrary_contract_calls_enabled: false,
};

const usdc: Token = {
  tokenId: 'tok-usdc',
  chainId: 1,
  contractAddress: USDC_CONTRACT,
  symbol: 'USDC',
  name: 'USD Coin',
  decimals: 6,
  pegCurrency: 'USD',
  issuer: 'Circle',
  status: 'verified',
  verifiedSymbol: 'USDC',
  verifiedDecimals: 6,
  verifiedAt: new Date(),
  verificationError: null,
  notes: null,
};

const zarp: Token = {
  ...usdc,
  tokenId: 'tok-zarp',
  contractAddress: ZARP_CONTRACT,
  symbol: 'ZARP',
  name: 'ZARP Stablecoin',
  decimals: 18,
  pegCurrency: 'ZAR',
  issuer: 'ZARP Stablecoin (Pty) Ltd',
  verifiedSymbol: 'ZARP',
  verifiedDecimals: 18,
};

const activeKey = {
  key_id: 'key-1',
  status: 'active',
  threshold: 2,
  total_parties: 3,
  blockchain: 'ethereum',
  address: wallet.address,
  public_key: 'ff'.repeat(33),
};

// The signing paths probe each party's liveness endpoint before choosing
// a committee. Every party is healthy here; party selection is tested
// elsewhere and is not what these tests are about.
beforeEach(() => {
  global.fetch = jest.fn(async () => ({ ok: true }) as Response) as unknown as typeof fetch;
});
afterEach(() => jest.restoreAllMocks());

function signingCeremony() {
  return jest.fn().mockImplementation(async ({ message }: { message: string }) => {
    const sig = wallet.signingKey.sign('0x' + message);
    return {
      status: 'completed',
      signature: sig.r.slice(2) + sig.s.slice(2) + (sig.yParity === 0 ? '00' : '01'),
    };
  });
}

function build(
  opts: { tokens?: Token[]; approved?: boolean; noRegistry?: boolean } = {},
) {
  const registered = opts.tokens ?? [usdc, zarp];
  const recordTransfer = jest.fn().mockResolvedValue(undefined);

  const postgres = {
    getKey: jest.fn().mockResolvedValue(activeKey),
    getCompletedCeremonyForKey: jest
      .fn()
      .mockResolvedValue({ ceremony_id: 'cer-1', threshold: 2, total_parties: 3 }),
    findSigningRequestByIdempotencyKey: jest.fn().mockResolvedValue(null),
    createSigningRequest: jest.fn().mockResolvedValue(undefined),
    completeSigningRequest: jest.fn().mockResolvedValue(undefined),
    failSigningRequest: jest.fn().mockResolvedValue(undefined),
    recordTransfer,
  } as unknown as PostgresService;

  const temporal = {
    signWithThreshold: signingCeremony(),
  } as unknown as KeysTemporalService;

  const evaluate = jest.fn().mockResolvedValue({
    approved: opts.approved !== false,
    denials: opts.approved === false ? ['amount exceeds the global limit'] : [],
    requiresApproval: false,
    reason: 'x',
  });
  const policy = { evaluate } as unknown as PolicyService;

  const registry = {
    byContract: jest.fn(async (chainId: number, address: string) =>
      registered.find(
        (t) =>
          t.chainId === chainId &&
          t.contractAddress?.toLowerCase() === address.toLowerCase(),
      ) ?? null,
    ),
    bySymbol: jest.fn(async (chainId: number, symbol: string) =>
      registered.find(
        (t) => t.chainId === chainId && t.symbol.toUpperCase() === symbol.toUpperCase(),
      ) ?? null,
    ),
    list: jest.fn(async () => registered),
    requireTransactable: jest.fn(async (chainId: number, symbolOrAddress: string) => {
      const found = symbolOrAddress.startsWith('0x')
        ? registered.find(
            (t) => t.contractAddress?.toLowerCase() === symbolOrAddress.toLowerCase(),
          )
        : registered.find((t) => t.symbol.toUpperCase() === symbolOrAddress.toUpperCase());
      if (!found) {
        throw new BadRequestException(`no token ${symbolOrAddress} on chain ${chainId}`);
      }
      if (found.status !== 'verified') {
        throw new BadRequestException(`${found.symbol} is ${found.status}`);
      }
      return found;
    }),
  } as unknown as TokenRegistryService;

  const rpc = {
    configured: jest.fn(() => true),
    call: jest.fn(),
    provider: jest.fn(),
  } as unknown as EvmRpcService;

  const service = new KeysService(
    postgres,
    temporal,
    policy,
    undefined,
    opts.noRegistry ? undefined : registry,
    rpc,
  );
  return { service, policy, evaluate, postgres, recordTransfer, registry, rpc };
}

const transferReq = {
  token: 'USDC',
  recipient: RECIPIENT,
  amount: '100.50',
  chainId: 1,
  nonce: 7,
  gasPrice: '20000000000',
};

describe('sending a stablecoin', () => {
  it('produces a transaction to the token contract carrying the transfer in its calldata', async () => {
    const { service } = build();

    const out = await service.sendToken(customer, 'key-1', transferReq);

    const parsed = Transaction.from(out.raw_transaction);
    expect(parsed.to).toBe(getAddress(USDC_CONTRACT));
    // Zero native value: attaching ether to an ERC-20 transfer sends it to
    // the token contract, where most contracts reject it and some keep it.
    expect(parsed.value).toBe(0n);
    expect(parsed.from).toBe(getAddress(wallet.address));

    const call = decodeErc20Call(parsed.data)!;
    expect(call.method).toBe('transfer');
    expect(call.recipient).toBe(getAddress(RECIPIENT));
    expect(call.amount).toBe('100500000'); // 100.50 at six decimals
  });

  // The headline assertion. Policy must be told the real recipient and the
  // real amount, not the contract and zero.
  it('tells policy the real recipient and amount, not the contract and zero', async () => {
    const { service, evaluate } = build();

    await service.sendToken(customer, 'key-1', transferReq);

    const input = evaluate.mock.calls[0][0];
    expect(input.to).toBe(RECIPIENT);
    expect(input.to).not.toBe(USDC_CONTRACT);
    expect(input.asset).toBe('USDC');
    expect(input.assetAmount).toBe('100500000');
    expect(input.assetDecimals).toBe(6);
    expect(input.pegCurrency).toBe('USD');
  });

  it('uses the registry decimals, so the same decimal amount differs by token', async () => {
    const { service, evaluate } = build();

    await service.sendToken(customer, 'key-1', { ...transferReq, token: 'ZARP', amount: '1' });

    const input = evaluate.mock.calls[0][0];
    expect(input.assetAmount).toBe('1000000000000000000'); // 18 decimals
    expect(input.pegCurrency).toBe('ZAR');
  });

  it('does not sign when policy denies', async () => {
    const { service, postgres } = build({ approved: false });

    await expect(service.sendToken(customer, 'key-1', transferReq)).rejects.toThrow();
    expect(postgres.createSigningRequest).not.toHaveBeenCalled();
  });

  // Records what moved, so the regulatory aggregate has something to sum.
  // signing.transactions previously carried nothing at all for the
  // threshold path.
  it('records the effective recipient and amount against the transaction', async () => {
    const { service, recordTransfer } = build();

    await service.sendToken(customer, 'key-1', transferReq);

    const row = recordTransfer.mock.calls[0][0];
    expect(row.assetSymbol).toBe('USDC');
    expect(row.assetDecimals).toBe(6);
    expect(row.assetPeg).toBe('USD');
    expect(row.effectiveTo).toBe(RECIPIENT);
    expect(row.effectiveAmount).toBe('100500000');
    // The envelope is still recorded truthfully alongside it.
    expect(row.to).toBe(USDC_CONTRACT);
    expect(row.value).toBe('0');
  });

  // A retry with the same idempotency key must land on the same row, or
  // one transfer appears twice in a number a filing is based on.
  it('derives a stable row id from a non-uuid idempotency key', async () => {
    const first = build();
    await first.service.sendToken(customer, 'key-1', {
      ...transferReq,
      idempotencyKey: 'invoice-4471',
    });
    const second = build();
    await second.service.sendToken(customer, 'key-1', {
      ...transferReq,
      idempotencyKey: 'invoice-4471',
    });

    const a = first.recordTransfer.mock.calls[0][0].rowId;
    const b = second.recordTransfer.mock.calls[0][0].rowId;
    expect(a).toBe(b);
    expect(a).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  });

  it('returns both the decimal amount and the base units that are in the bytes', async () => {
    const { service } = build();

    const out = await service.sendToken(customer, 'key-1', transferReq);

    expect(out.amount).toBe('100.50');
    expect(out.amount_base_units).toBe('100500000');
    expect(out.token.symbol).toBe('USDC');
    expect(out.token.decimals).toBe(6);
  });
});

describe('tokens the platform will not move', () => {
  it('refuses a token that is not registered', async () => {
    const { service } = build();

    await expect(
      service.sendToken(customer, 'key-1', { ...transferReq, token: 'SCAMCOIN' }),
    ).rejects.toThrow(/SCAMCOIN/);
  });

  // The gate that makes a seeded or hand-entered contract address safe.
  it('refuses a registered token that has never been verified on-chain', async () => {
    const unverified: Token = { ...usdc, status: 'unverified' };
    const { service, postgres } = build({ tokens: [unverified] });

    await expect(service.sendToken(customer, 'key-1', transferReq)).rejects.toThrow(
      /unverified/,
    );
    expect(postgres.createSigningRequest).not.toHaveBeenCalled();
  });

  it('refuses a suspended token', async () => {
    const { service } = build({ tokens: [{ ...usdc, status: 'suspended' }] });

    await expect(service.sendToken(customer, 'key-1', transferReq)).rejects.toThrow(
      /suspended/,
    );
  });

  it('refuses a token whose address was never confirmed with the issuer', async () => {
    const awaiting: Token = { ...zarp, status: 'awaiting_address', contractAddress: null };
    const { service } = build({ tokens: [awaiting] });

    await expect(
      service.sendToken(customer, 'key-1', { ...transferReq, token: 'ZARP' }),
    ).rejects.toThrow(/awaiting_address|address/);
  });

  it('refuses more precision than the token has, rather than rounding it away', async () => {
    const { service } = build();

    await expect(
      service.sendToken(customer, 'key-1', { ...transferReq, amount: '1.0000005' }),
    ).rejects.toThrow(/decimal places/);
  });

  it('refuses to send a token from a bitcoin key', async () => {
    const { service, postgres } = build();
    (postgres.getKey as jest.Mock).mockResolvedValue({
      ...activeKey,
      blockchain: 'bitcoin',
    });

    await expect(service.sendToken(customer, 'key-1', transferReq)).rejects.toThrow(
      /bitcoin key/,
    );
  });

  it('refuses when no registry is configured rather than signing ungoverned', async () => {
    const { service } = build({ noRegistry: true });

    await expect(service.sendToken(customer, 'key-1', transferReq)).rejects.toBeInstanceOf(
      ServiceUnavailableException,
    );
  });
});

// The other route. A customer can still hand-encode calldata through
// POST /keys/:keyId/transactions, and when they do it must be decoded
// before policy rather than passed around it.
describe('calldata supplied through the general transaction route', () => {
  const rawReq = {
    to: USDC_CONTRACT,
    value: '0',
    data: encodeTransfer(RECIPIENT, '250000000'), // 250 USDC
    gasLimit: 100000,
    nonce: 3,
    chainId: 1,
    gasPrice: '20000000000',
  };

  it('decodes a hand-encoded transfer and governs it on the decoded values', async () => {
    const { service, evaluate } = build();

    await service.signTransaction(customer, 'key-1', rawReq);

    const input = evaluate.mock.calls[0][0];
    expect(input.to).toBe(getAddress(RECIPIENT));
    expect(input.asset).toBe('USDC');
    expect(input.assetAmount).toBe('250000000');
    expect(input.pegCurrency).toBe('USD');
  });

  it('leaves a native transfer governed exactly as before', async () => {
    const { service, evaluate } = build();

    await service.signTransaction(customer, 'key-1', {
      to: RECIPIENT,
      value: '1000000000000000000',
      gasLimit: 21000,
      nonce: 1,
      chainId: 1,
      gasPrice: '20000000000',
    });

    const input = evaluate.mock.calls[0][0];
    expect(input.to).toBe(RECIPIENT);
    expect(input.value).toBe('1000000000000000000');
    expect(input.asset).toBe('NATIVE');
  });

  // Fail closed. Calldata the platform cannot account for means no control
  // has read what the transaction does.
  it('refuses calldata it cannot decode', async () => {
    const { service, postgres } = build();

    await expect(
      service.signTransaction(customer, 'key-1', {
        ...rawReq,
        data: '0xdeadbeef' + '00'.repeat(32),
      }),
    ).rejects.toBeInstanceOf(BadRequestException);
    expect(postgres.createSigningRequest).not.toHaveBeenCalled();
  });

  // A transfer of a token nobody registered decodes fine -- but without
  // the decimals, the amount is a number with no unit, and a limit cannot
  // be applied to that.
  it('refuses a transfer of a token that is not in the registry', async () => {
    const { service } = build();

    await expect(
      service.signTransaction(customer, 'key-1', {
        ...rawReq,
        to: '0x9999999999999999999999999999999999999999',
      }),
    ).rejects.toThrow(/no token is registered/);
  });

  it('tells the caller which route to use instead', async () => {
    const { service } = build();

    await expect(
      service.signTransaction(customer, 'key-1', {
        ...rawReq,
        data: '0xdeadbeef' + '00'.repeat(32),
      }),
    ).rejects.toThrow(/token-transfers/);
  });

  // The escape hatch, and its cost. A tenant who has explicitly accepted
  // that policy cannot read these calls gets them signed -- with the
  // envelope's own to and value going to policy, which is the old blind
  // behaviour, now reachable only by a decision somebody made on the
  // record.
  it('allows undecodable calldata only for a tenant that enabled it', async () => {
    const { service, evaluate } = build();
    const permitted: Customer = { ...customer, arbitrary_contract_calls_enabled: true };

    await service.signTransaction(permitted, 'key-1', {
      ...rawReq,
      data: '0xdeadbeef' + '00'.repeat(32),
    });

    const input = evaluate.mock.calls[0][0];
    expect(input.asset).toBe('UNKNOWN');
    // No peg, so the policy engine escalates it rather than holding it to
    // a limit it has no units for.
    expect(input.pegCurrency).toBeUndefined();
  });

  it('records nothing it could not decode rather than guessing', async () => {
    const { service, recordTransfer } = build();
    const permitted: Customer = { ...customer, arbitrary_contract_calls_enabled: true };

    await service.signTransaction(permitted, 'key-1', {
      ...rawReq,
      data: '0xdeadbeef' + '00'.repeat(32),
    });

    const row = recordTransfer.mock.calls[0][0];
    // Null, not the contract address. Recording the contract as the
    // recipient would assert that a contract call was a payment to the
    // contract.
    expect(row.effectiveTo).toBeNull();
    expect(row.effectiveAmount).toBeNull();
  });
});

describe('reading what a key holds', () => {
  it('reports a balance per registered token, in the token\'s own units', async () => {
    const { service, rpc } = build();
    (rpc.call as jest.Mock).mockImplementation(async (_chain, contract: string) =>
      contract.toLowerCase() === USDC_CONTRACT
        ? '0x' + (2_000_000_000_000n).toString(16).padStart(64, '0') // 2,000,000 USDC
        : '0x' + (5n * 10n ** 18n).toString(16).padStart(64, '0'), // 5 ZARP
    );
    (rpc.provider as jest.Mock).mockReturnValue({
      getBalance: jest.fn().mockResolvedValue(10n ** 18n),
    });

    const out = await service.getBalances(customer, 'key-1', 1);

    const bySymbol = Object.fromEntries(out.tokens.map((t) => [t.symbol, t]));
    expect(bySymbol.USDC.balance).toBe('2000000');
    expect(bySymbol.USDC.balance_base_units).toBe('2000000000000');
    expect(bySymbol.ZARP.balance).toBe('5');
    expect(out.native.balance).toBe('1');
  });

  // One unresponsive contract must not blank out every other balance. A
  // customer looking at an incomplete list with a named error can act on
  // it; one looking at an error page cannot tell whether their money is
  // gone.
  it('reports one token\'s failure without losing the others', async () => {
    const { service, rpc } = build();
    (rpc.call as jest.Mock).mockImplementation(async (_chain, contract: string) => {
      if (contract.toLowerCase() === USDC_CONTRACT) {
        throw new Error('node unreachable');
      }
      return '0x' + (5n * 10n ** 18n).toString(16).padStart(64, '0');
    });
    (rpc.provider as jest.Mock).mockReturnValue({
      getBalance: jest.fn().mockResolvedValue(0n),
    });

    const out = await service.getBalances(customer, 'key-1', 1);

    const bySymbol = Object.fromEntries(out.tokens.map((t) => [t.symbol, t]));
    expect(bySymbol.USDC.balance).toBeNull();
    expect(bySymbol.USDC.error).toMatch(/node unreachable/);
    expect(bySymbol.ZARP.balance).toBe('5');
  });

  // An empty reply means the call reverted or nothing is deployed there.
  // Reporting that as zero is a lie with the same shape as the truth.
  it('does not report an empty reply as a zero balance', async () => {
    const { service, rpc } = build();
    (rpc.call as jest.Mock).mockResolvedValue('0x');
    (rpc.provider as jest.Mock).mockReturnValue({
      getBalance: jest.fn().mockResolvedValue(0n),
    });

    const out = await service.getBalances(customer, 'key-1', 1);

    expect(out.tokens.every((t) => t.balance === null)).toBe(true);
    expect(out.tokens.every((t) => typeof t.error === 'string')).toBe(true);
  });
});
