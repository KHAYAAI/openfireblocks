import { ServiceUnavailableException } from '@nestjs/common';
import { AgentController } from './agents.controller';

// The platform-prepared path an AI agent uses: no nonce, no fees, no broadcasting of its own.
// The budget, policy and signing are covered elsewhere (agents.live.spec.ts, the keys specs);
// these check that the platform reads the chain, signs once with what it read, and broadcasts.

const AGENT = {
  agentId: 'a1', customerId: 'c1', name: 'bot', keyId: 'k1', allowedTokens: ['USDC'], allowedRecipients: null,
  perTransferLimitZar: 5000, dailyLimitZar: 10000, status: 'active', expiresAt: '2099-01-01', createdBy: 'u', createdAt: '', revokedAt: null,
};
const USDC = { symbol: 'USDC', decimals: 6, pegCurrency: 'USD', chainId: 11155111, contractAddress: '0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238' };
const SUPPLIER = '0x70997970C51812dc3A010C7d01b50e0d17dc79C8';

function setup(opts: { configured?: boolean; eip1559?: boolean; broadcastFails?: boolean; prior?: Record<string, unknown> } = {}) {
  const calls: Record<string, any[]> = { sendToken: [], broadcast: [], settle: [], reserve: [] };
  const provider = {
    getTransactionCount: jest.fn(async () => 7),
    getFeeData: jest.fn(async () => (opts.eip1559 === false
      ? { gasPrice: 1_000_000_000n, maxFeePerGas: null, maxPriorityFeePerGas: null }
      : { gasPrice: null, maxFeePerGas: 30_000_000_000n, maxPriorityFeePerGas: 1_000_000_000n })),
    broadcastTransaction: jest.fn(async (raw: string) => {
      calls.broadcast.push(raw);
      if (opts.broadcastFails) throw new Error('nonce too low');
      return { hash: '0xbroadcasthash' };
    }),
  };
  const rpc = { configured: jest.fn(() => opts.configured !== false), provider: jest.fn(() => provider) };
  const agents = {
    spend: jest.fn(async (_a: unknown, id: string) => (opts as any).prior?.[id] ?? null),
    record: jest.fn(async () => ({ remaining24hZar: 7500 })),
    reserve: jest.fn(async (_a: unknown, t: unknown) => { calls.reserve.push(t); return { spendId: 's1', valueZar: 18.5, remaining24hZar: 9981.5 }; }),
    settle: jest.fn(async (_a: unknown, _id: string, o: unknown) => { calls.settle.push(o); }),
  };
  const keys = {
    getKey: jest.fn(async () => ({ address: '0x00000000000000000000000000000000000000aa' })),
    sendToken: jest.fn(async (_c: unknown, _k: string, req: any) => {
      calls.sendToken.push(req);
      return { raw_transaction: '0xraw', transaction_hash: '0xlocalhash', parties: [1, 2] };
    }),
  };
  const customers = { getByCustomerId: jest.fn(async () => ({ customer_id: 'c1' })) };
  const tokens = { requireTransactable: jest.fn(async () => USDC) };
  const c = new AgentController(agents as any, keys as any, customers as any, tokens as any, rpc as any);
  const pay = (extra: Record<string, unknown> = {}) =>
    c.transfer({ agent: AGENT, headers: {} } as any, { token: 'USDC', recipient: SUPPLIER, amount: '1.00', chainId: 11155111, purpose: 'invoice', ...extra } as any);
  return { c, calls, provider, pay };
}

describe('agent payments the platform prepares and broadcasts', () => {
  it('reads the nonce and EIP-1559 fees, signs once with them, and broadcasts', async () => {
    const { calls, provider, pay } = setup();
    const out: any = await pay();
    expect(provider.getTransactionCount).toHaveBeenCalledWith('0x00000000000000000000000000000000000000aa', 'pending');
    expect(calls.sendToken).toHaveLength(1);
    expect(calls.sendToken[0]).toMatchObject({ nonce: 7, maxFeePerGas: '30000000000', maxPriorityFeePerGas: '1000000000', token: 'USDC' });
    expect(calls.broadcast).toEqual(['0xraw']);
    expect(out).toMatchObject({ broadcast: true, transaction_hash: '0xbroadcasthash', agent: { remaining24hZar: 9981.5 } });
    expect(calls.settle).toEqual([{ txHash: '0xbroadcasthash' }]);
  });

  it('uses a legacy gas price on a chain without EIP-1559', async () => {
    const { calls, pay } = setup({ eip1559: false });
    await pay();
    expect(calls.sendToken[0]).toMatchObject({ nonce: 7, gasPrice: '1000000000' });
    expect(calls.sendToken[0].maxFeePerGas).toBeUndefined();
  });

  it('refuses before reserving any budget when the platform cannot reach the chain', async () => {
    const { calls, pay } = setup({ configured: false });
    await expect(pay()).rejects.toBeInstanceOf(ServiceUnavailableException);
    expect(calls.reserve).toHaveLength(0);
    expect(calls.sendToken).toHaveLength(0);
  });

  it('gives the budget back when the network rejects the broadcast', async () => {
    const { calls, pay } = setup({ broadcastFails: true });
    await expect(pay()).rejects.toThrow(/nonce too low/);
    expect(calls.settle).toEqual([{ failed: 'nonce too low' }]);
  });

  it('keeps the original contract when the caller supplies the nonce: signed, not broadcast', async () => {
    const { calls, provider, pay } = setup();
    const out: any = await pay({ nonce: 3, gasPrice: '1' });
    expect(provider.getTransactionCount).not.toHaveBeenCalled();
    expect(calls.broadcast).toHaveLength(0);
    expect(out.broadcast).toBe(false);
    expect(calls.sendToken[0]).toMatchObject({ nonce: 3, gasPrice: '1' });
  });

  it('a retry of a payment already made returns it, and signs and pays nothing', async () => {
    const { calls, pay } = setup({ prior: { 'inv-1': { status: 'spent', txHash: '0xfirst', token: 'USDC', recipient: SUPPLIER, amountBaseUnits: '1000000', valueZar: 18.5 } } });
    const out: any = await pay({ idempotencyKey: 'inv-1' });
    expect(out).toMatchObject({ replayed: true, transaction_hash: '0xfirst', agent: { remaining24hZar: 7500 } });
    expect(calls.reserve).toHaveLength(0);
    expect(calls.sendToken).toHaveLength(0);
    expect(calls.broadcast).toHaveLength(0);
  });

  it('a retry while the first attempt is still being signed is refused, not doubled', async () => {
    const { calls, pay } = setup({ prior: { 'inv-2': { status: 'reserved' } } });
    await expect(pay({ idempotencyKey: 'inv-2' })).rejects.toThrow(/still being signed/);
    expect(calls.reserve).toHaveLength(0);
  });

  it('a refused attempt can be tried again under the same key', async () => {
    const { calls, pay } = setup({ prior: { 'inv-3': { status: 'refused' } } });
    await pay({ idempotencyKey: 'inv-3' });
    expect(calls.reserve).toHaveLength(1);
    expect(calls.broadcast).toHaveLength(1);
  });
});
