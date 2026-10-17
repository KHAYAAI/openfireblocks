import { BadRequestException } from '@nestjs/common';
import { Transaction, Wallet } from 'ethers';
import { KeysService } from './keys.service';
import { GovernedContracts } from './governed-contracts';
import { PostgresService } from '../database/postgres.service';
import { PolicyService } from '../policies/policy.service';
import { KeysTemporalService } from './keys-temporal.service';
import { TokenRegistryService } from '../tokens/token-registry.service';
import { Customer } from '../customers/customer.service';
import { buildCall, recogniseCall } from '../tokenisation/token-calls';

// The signing path and calldata it cannot read. Unknown calldata is refused unless the
// organisation has turned on arbitrary contract calls -- a blanket escape hatch. A module
// that owns a contract can vouch for specific calls to it, so those do not need the hatch;
// and nothing it does not vouch for is let through by that.

const wallet = new Wallet('0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d');
const TOKEN = '0x1234567890123456789012345678901234567890';
const HOLDER = '0x70997970c51812dc3a010c7d01b50e0d17dc79c8';
const customer: Customer = {
  customer_id: '11111111-1111-4111-8111-111111111111', name: 'demo', email: 'demo@x.io', status: 'active', tier: 'enterprise',
  policies: {}, raw_digest_signing_enabled: false, arbitrary_contract_calls_enabled: false,
};
const activeKey = { key_id: 'key-1', status: 'active', threshold: 2, total_parties: 3, blockchain: 'ethereum', address: wallet.address, public_key: 'ff'.repeat(33) };

beforeEach(() => { global.fetch = jest.fn(async () => ({ ok: true }) as Response) as unknown as typeof fetch; });
afterEach(() => jest.restoreAllMocks());

function build(governed?: GovernedContracts) {
  const postgres = {
    getKey: jest.fn().mockResolvedValue(activeKey),
    getCompletedCeremonyForKey: jest.fn().mockResolvedValue({ ceremony_id: 'cer-1', threshold: 2, total_parties: 3 }),
    findSigningRequestByIdempotencyKey: jest.fn().mockResolvedValue(null),
    createSigningRequest: jest.fn().mockResolvedValue(undefined), completeSigningRequest: jest.fn().mockResolvedValue(undefined),
    failSigningRequest: jest.fn().mockResolvedValue(undefined), recordTransfer: jest.fn().mockResolvedValue(undefined),
  } as unknown as PostgresService;
  const temporal = { signWithThreshold: jest.fn().mockImplementation(async ({ message }: { message: string }) => {
    const sig = wallet.signingKey.sign('0x' + message);
    return { status: 'completed', signature: sig.r.slice(2) + sig.s.slice(2) + (sig.yParity === 0 ? '00' : '01') };
  }) } as unknown as KeysTemporalService;
  const evaluate = jest.fn().mockResolvedValue({ approved: true, denials: [], requiresApproval: false, reason: 'x' });
  const policy = { evaluate } as unknown as PolicyService;
  const tokens = { byContract: jest.fn(async () => null) } as unknown as TokenRegistryService;
  const service = new KeysService(postgres, temporal, policy, undefined, tokens, { configured: () => true } as any, undefined, undefined, governed);
  return { service, evaluate };
}

const mint = buildCall('mint', { to: HOLDER, amount: '1000' }).data;
const req = (data: string, over: Record<string, unknown> = {}) => ({ to: TOKEN, value: '0', data, gasLimit: 100000, nonce: 1, chainId: 1, gasPrice: '20000000000', ...over });

function recogniserFor(contract: string) {
  const g = new GovernedContracts();
  g.register({
    recognise: async (_c, _chain, to, value, data) => {
      if (to.toLowerCase() !== contract.toLowerCase() || value !== '0') return null;
      const call = recogniseCall(data);
      return call ? { description: `${call.op} (recognised)` } : null;
    },
  });
  return g;
}

describe('calldata the platform cannot read', () => {
  it('is refused by default', async () => {
    const { service, evaluate } = build();
    await expect(service.signTransaction(customer, 'key-1', req(mint) as any)).rejects.toThrow(BadRequestException);
    await expect(service.signTransaction(customer, 'key-1', req(mint) as any)).rejects.toThrow(/cannot account for/);
    expect(evaluate).not.toHaveBeenCalled();
  });

  it('is refused when the module that owns the contract does not vouch for it', async () => {
    const { service } = build(recogniserFor('0x' + '9'.repeat(40))); // some other contract
    await expect(service.signTransaction(customer, 'key-1', req(mint) as any)).rejects.toThrow(/cannot account for/);
  });

  it('is signed when vouched for, and policy is told it is a governed call to the contract, not a payment', async () => {
    const { service, evaluate } = build(recogniserFor(TOKEN));
    const out: any = await service.signTransaction(customer, 'key-1', req(mint) as any, { approvalGranted: true });
    const parsed = Transaction.from(out.raw_transaction);
    expect(parsed.data).toBe(mint);
    expect(parsed.value).toBe(0n);
    const input = evaluate.mock.calls[0][0];
    expect(input).toMatchObject({ to: TOKEN, value: '0', asset: 'GOVERNED', assetAmount: '0' });
  });

  it('is still refused if the vouched contract is sent anything the recogniser does not read: other selectors, trailing bytes, value', async () => {
    const { service } = build(recogniserFor(TOKEN));
    const transferData = '0xa9059cbb' + '0'.repeat(24) + HOLDER.slice(2) + '0'.repeat(63) + '1';
    await expect(service.signTransaction(customer, 'key-1', req('0xdeadbeef') as any)).rejects.toThrow(/cannot account for/);
    await expect(service.signTransaction(customer, 'key-1', req(mint + '00') as any)).rejects.toThrow(/cannot account for/);
    await expect(service.signTransaction(customer, 'key-1', req(mint, { value: '1' }) as any)).rejects.toThrow(/cannot account for/);
    // An ordinary token transfer still goes down the ERC-20 path, which needs a registered token.
    await expect(service.signTransaction(customer, 'key-1', req(transferData) as any)).rejects.toThrow(/cannot account for|no token is registered/);
  });
});
