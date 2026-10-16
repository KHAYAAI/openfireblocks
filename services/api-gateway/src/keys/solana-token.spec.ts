import { BadRequestException, NotFoundException } from '@nestjs/common';
import { KeysService } from './keys.service';
import { PostgresService } from '../database/postgres.service';
import { PolicyService } from '../policies/policy.service';
import { KeysTemporalService } from './keys-temporal.service';
import { Customer } from '../customers/customer.service';
import { TokenRegistryService, isSolanaMint, normaliseTokenAddress } from '../tokens/token-registry.service';

// SPL tokens through POST /keys/:keyId/solana-transactions with `token` set.
//
// As for native SOL, the chain work (token accounts, rent, decimals against
// the chain, the message bytes) is the signer's and is tested there. What the
// gateway owns is that the token is resolved from the registry before
// anything else, that policy and the Travel Rule see the TOKEN and its
// amount rather than a SOL figure, and that the registry's decimals and mint
// are what the signer is told to use.

const customer: Customer = {
  customer_id: 'cust-1', name: 'demo', email: 'demo@x.io', status: 'active', tier: 'pro', policies: {},
  raw_digest_signing_enabled: false, arbitrary_contract_calls_enabled: false,
};
const solKey = { key_id: 'sol-1', customer_id: 'cust-1', blockchain: 'solana', status: 'active', threshold: 2, total_parties: 3,
  address: 'SoLAddr', public_key: 'ab'.repeat(32) };
const DEST = '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM';
const USDC_MINT = 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v';
const MESSAGE = '01000508' + 'aa'.repeat(300);
const ED_SIG = 'ee'.repeat(64);

const usdc = {
  tokenId: 't-usdc', chainId: 501, contractAddress: USDC_MINT, symbol: 'USDC', name: 'USD Coin', decimals: 6,
  pegCurrency: 'USD', issuer: 'Circle', status: 'verified',
};

type Calls = Record<string, Array<Record<string, unknown>>>;

function mockSigner(opts: { prepareStatus?: number; prepareError?: string } = {}): Calls {
  const calls: Calls = {};
  global.fetch = jest.fn(async (url: string | URL | Request, init?: RequestInit) => {
    const href = String(url);
    const path = new URL(href).pathname;
    if (path === '/health') return { ok: true } as Response;
    const body = init?.body ? JSON.parse(String(init.body)) : Object.fromEntries(new URL(href).searchParams);
    (calls[path] ??= []).push(body);
    const reply = (status: number, payload: unknown) =>
      ({ ok: status === 200, status, text: async () => JSON.stringify(payload) }) as Response;
    switch (path) {
      case '/solana/addresses': return reply(200, { address: 'SoLAddr' });
      case '/solana/token/prepare':
        if ((opts.prepareStatus ?? 200) !== 200) return reply(opts.prepareStatus!, { error: opts.prepareError });
        return reply(200, {
          message_hex: MESSAGE, from: 'SoLAddr', to: DEST, mint: USDC_MINT,
          source_token_account: 'SrcATA', destination_token_account: 'DstATA', creates_token_account: true,
          amount: body.amount, decimals: 6, fee: '5000', rent: '2039280', sol_balance: '10000000', token_balance: '10000000',
          blockhash: 'bh', last_valid_block_height: 99,
        });
      case '/solana/finalize': return reply(200, { signature: 'SplSig', raw_tx_base64: 'cmF3', broadcast: body.broadcast === true });
      case '/solana/balance': return reply(200, { address: 'SoLAddr', lamports: '42', asset: 'SOL', decimals: 9 });
      case '/solana/token/balance': return reply(200, { owner: 'SoLAddr', mint: body.mint, token_account: 'SrcATA', exists: true, amount: '2500000', frozen: false });
    }
    throw new Error(`unexpected request to ${href}`);
  }) as unknown as typeof fetch;
  return calls;
}

function build(opts: { approved?: boolean; token?: unknown } = {}) {
  const postgres = {
    getKey: jest.fn().mockResolvedValue(solKey),
    getCompletedCeremonyForKey: jest.fn().mockResolvedValue({ ceremony_id: 'cer-1', total_parties: 3 }),
    findSigningRequestByIdempotencyKey: jest.fn().mockResolvedValue(null),
    createSigningRequest: jest.fn().mockResolvedValue(undefined),
    completeSigningRequest: jest.fn().mockResolvedValue(undefined),
    failSigningRequest: jest.fn().mockResolvedValue(undefined),
    recordTransfer: jest.fn().mockResolvedValue(undefined),
  } as unknown as PostgresService;
  const temporal = {
    signWithThreshold: jest.fn().mockResolvedValue({ status: 'completed', signature: ED_SIG }),
  } as unknown as KeysTemporalService;
  const policy = {
    evaluate: jest.fn().mockResolvedValue({ approved: opts.approved !== false, denials: opts.approved === false ? ['amount_limit'] : [], requiresApproval: false, reason: 'x' }),
  } as unknown as PolicyService;
  const tokens = {
    requireTransactable: opts.token instanceof Error
      ? jest.fn().mockRejectedValue(opts.token)
      : jest.fn().mockResolvedValue(opts.token ?? usdc),
    list: jest.fn().mockResolvedValue([usdc, { ...usdc, tokenId: 't-x', symbol: 'XUNV', status: 'unverified' }]),
  } as unknown as TokenRegistryService;
  return { service: new KeysService(postgres, temporal, policy, undefined, tokens), postgres, temporal, policy, tokens };
}

beforeEach(() => { process.env.MPC_SIGNER_URL = 'http://signer:8080'; });
afterEach(() => { jest.restoreAllMocks(); delete process.env.MPC_SIGNER_URL; });

describe('sending an SPL token', () => {
  it('resolves the token on Solana, tells the signer the registry mint and decimals, and signs what it prepared', async () => {
    const calls = mockSigner();
    const { service, temporal, tokens } = build();
    const res = await service.sendSolana(customer, 'sol-1', { destination: DEST, amount: '2500000', token: 'USDC' });

    expect((tokens.requireTransactable as jest.Mock)).toHaveBeenCalledWith(501, 'USDC');
    expect(calls['/solana/token/prepare'][0]).toEqual({
      pubkey_hex: solKey.public_key, destination: DEST, mint: USDC_MINT, amount: '2500000', decimals: 6,
    });
    expect(calls['/solana/prepare']).toBeUndefined();
    expect((temporal.signWithThreshold as jest.Mock).mock.calls[0][0].message).toBe(MESSAGE);
    expect(calls['/solana/finalize'][0]).toEqual({ message_hex: MESSAGE, signature_hex: ED_SIG, broadcast: true });
    expect(res).toMatchObject({
      asset: 'USDC', decimals: 6, mint: USDC_MINT, amount: '2500000', signature: 'SplSig',
      destination_token_account: 'DstATA', created_token_account: true, rent: '2039280',
    });
  });

  it('shows policy the token and its amount, not a SOL value', async () => {
    mockSigner();
    const { service, policy } = build();
    await service.sendSolana(customer, 'sol-1', { destination: DEST, amount: '2500000', token: 'USDC' });
    expect(policy.evaluate).toHaveBeenCalledWith(expect.objectContaining({
      to: DEST, chainId: 501, value: '0', asset: 'USDC', assetAmount: '2500000', assetDecimals: 6, pegCurrency: 'USD',
    }));
  });

  it('records what moved -- the token, the mint, the amount -- for the compliance aggregate', async () => {
    mockSigner();
    const { service, postgres } = build();
    await service.sendSolana(customer, 'sol-1', { destination: DEST, amount: '2500000', token: 'USDC' });
    expect(postgres.recordTransfer).toHaveBeenCalledWith(expect.objectContaining({
      chain: 'solana', assetSymbol: 'USDC', assetDecimals: 6, assetContract: USDC_MINT,
      effectiveTo: DEST, effectiveAmount: '2500000', txHash: 'SplSig',
    }));
  });

  it('asks the signer nothing for a token the registry refuses', async () => {
    const calls = mockSigner();
    const { service, temporal } = build({ token: new NotFoundException('no token FAKE is registered on chain 501') });
    await expect(service.sendSolana(customer, 'sol-1', { destination: DEST, amount: '1', token: 'FAKE' })).rejects.toBeInstanceOf(NotFoundException);
    expect(calls['/solana/token/prepare']).toBeUndefined();
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  it('runs no ceremony when policy refuses or the signer refuses', async () => {
    let calls = mockSigner();
    let b = build({ approved: false });
    await expect(b.service.sendSolana(customer, 'sol-1', { destination: DEST, amount: '1', token: 'USDC' })).rejects.toThrow();
    expect(calls['/solana/token/prepare']).toBeUndefined();
    expect(b.temporal.signWithThreshold).not.toHaveBeenCalled();

    calls = mockSigner({ prepareStatus: 400, prepareError: 'insufficient SOL for fees' });
    b = build();
    await expect(b.service.sendSolana(customer, 'sol-1', { destination: DEST, amount: '1', token: 'USDC' })).rejects.toBeInstanceOf(BadRequestException);
    expect(b.temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  it('lists verified token balances next to SOL, and only verified ones', async () => {
    const calls = mockSigner();
    const { service } = build();
    const res = await service.getBalancesForKey(customer, 'sol-1');
    expect((res as { balances: unknown[] }).balances).toEqual([
      { asset: 'SOL', amount: '42', decimals: 9 },
      { asset: 'USDC', amount: '2500000', decimals: 6, mint: USDC_MINT, token_account: 'SrcATA' },
    ]);
    expect(calls['/solana/token/balance']).toHaveLength(1);
  });
});

describe('Solana mint addresses in the registry', () => {
  it('accepts a base58 mint exactly as written, and refuses anything else', () => {
    expect(isSolanaMint(USDC_MINT)).toBe(true);
    expect(normaliseTokenAddress(501, USDC_MINT)).toBe(USDC_MINT); // not lowercased: case is part of the address
    expect(() => normaliseTokenAddress(501, '0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48')).toThrow(BadRequestException);
    expect(() => normaliseTokenAddress(501, 'EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1O')).toThrow(BadRequestException); // 'O' is not base58
    expect(normaliseTokenAddress(1, '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48')).toBe('0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48');
    expect(() => normaliseTokenAddress(1, USDC_MINT)).toThrow(BadRequestException);
  });
});

describe('verifying a Solana mint', () => {
  function registry(row: Record<string, unknown>, mintReply: { status: number; body: unknown }) {
    const updates: unknown[][] = [];
    const pool = {
      query: jest.fn(async (sql: string, params: unknown[]) => {
        if (/^\s*SELECT/.test(sql)) return { rows: [row] };
        updates.push(params);
        return { rows: [{ ...row, status: /status = 'verified'/.test(sql) ? 'verified' : row.status, verified_decimals: params[1] }] };
      }),
    };
    global.fetch = jest.fn(async () => ({
      ok: mintReply.status === 200, status: mintReply.status, text: async () => JSON.stringify(mintReply.body),
    })) as unknown as typeof fetch;
    return { service: new TokenRegistryService(pool as never, {} as never), updates };
  }
  const row = {
    token_id: 't-usdc', chain_id: 501, contract_address: USDC_MINT, symbol: 'USDC', name: 'USD Coin', decimals: 6,
    peg_currency: 'USD', issuer: 'Circle', status: 'unverified', verified_symbol: null, verified_decimals: null,
    verified_at: null, verification_error: null, notes: null,
  };

  it('marks it verified when the chain agrees on the decimals', async () => {
    const { service } = registry(row, { status: 200, body: { mint: USDC_MINT, program: 'Tokenkeg', decimals: 6, supply: '1' } });
    await expect(service.verify('t-usdc')).resolves.toMatchObject({ status: 'verified', verifiedDecimals: 6 });
    expect(String((global.fetch as jest.Mock).mock.calls[0][0])).toContain(`/solana/token/mint?mint=${USDC_MINT}`);
  });

  it('leaves it unusable when the decimals differ, or the signer says it is not a Token program mint', async () => {
    let r = registry({ ...row, decimals: 18 }, { status: 200, body: { mint: USDC_MINT, decimals: 6 } });
    await expect(r.service.verify('t-usdc')).rejects.toThrow(/reports 6 decimals, the registry says 18/);
    expect(r.updates.every((p) => p.length === 2 && String(p[1]).includes('decimals'))).toBe(true);

    r = registry(row, { status: 422, body: { error: 'mint X is a Token-2022 mint; only the original Token program is supported' } });
    await expect(r.service.verify('t-usdc')).rejects.toThrow(/Token-2022/);
  });
});
