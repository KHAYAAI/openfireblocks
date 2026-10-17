import { BadRequestException, ServiceUnavailableException } from '@nestjs/common';
import { createHash } from 'crypto';
import { KeysService } from './keys.service';
import { PostgresService } from '../database/postgres.service';
import { PolicyService } from '../policies/policy.service';
import { KeysTemporalService } from './keys-temporal.service';
import { Customer } from '../customers/customer.service';

// Spending Solana and Cosmos through the public API.
//
// What is worth testing here is the orchestration, not the chains: the
// signer owns transaction layout, rent, fees and verification (tested in
// services/mpc-signer). The gateway's job is to get the order right --
// policy before any chain read, a ceremony only after the signer has
// refused what would fail, the signature handed back to the signer to be
// verified -- and to keep other chains' keys out of the EVM routes.

const customer: Customer = {
  customer_id: 'cust-1', name: 'demo', email: 'demo@x.io', status: 'active', tier: 'pro', policies: {},
  raw_digest_signing_enabled: false, arbitrary_contract_calls_enabled: false,
};
const ceremony = { ceremony_id: 'cer-1', total_parties: 3 };

const solKey = { key_id: 'sol-1', customer_id: 'cust-1', blockchain: 'solana', status: 'active', threshold: 2, total_parties: 3,
  address: 'SoLAddr', public_key: 'ab'.repeat(32) };
const cosKey = { ...solKey, key_id: 'cos-1', blockchain: 'cosmos', address: 'cosmos1x', public_key: '04' + 'cd'.repeat(64) };
const ethKey = { ...solKey, key_id: 'eth-1', blockchain: 'ethereum', address: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8' };

const SOL_DEST = '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM';
const COS_DEST = 'cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu';
const SOL_MESSAGE = '01000103' + 'aa'.repeat(400); // longer than a 32-byte digest on purpose
const COS_DIGEST = '1f'.repeat(32);
const ED_SIG = 'ee'.repeat(64);
const SECP_SIG = 'aa'.repeat(32) + 'bb'.repeat(32) + '01';

interface Calls { [path: string]: Array<Record<string, unknown>> }

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
      case '/cosmos/addresses': return reply(200, { address: 'cosmos1x', prefix: 'cosmos' });
      case '/solana/prepare':
        if ((opts.prepareStatus ?? 200) !== 200) return reply(opts.prepareStatus!, { error: opts.prepareError ?? 'insufficient balance' });
        return reply(200, { message_hex: SOL_MESSAGE, from: 'SoLAddr', to: SOL_DEST, amount: '1000000000', fee: '5000',
          balance: '5000000000', blockhash: 'bh', last_valid_block_height: 99 });
      case '/solana/finalize': return reply(200, { signature: 'SolSig', raw_tx_base64: 'cmF3', broadcast: body.broadcast === true });
      case '/cosmos/prepare':
        if ((opts.prepareStatus ?? 200) !== 200) return reply(opts.prepareStatus!, { error: opts.prepareError ?? 'insufficient uatom' });
        return reply(200, { plan: { body_bytes_hex: '0a', auth_info_bytes_hex: '12', chain_id: 'cosmoshub-4', account_number: 7, digest_hex: COS_DIGEST },
          from: 'cosmos1x', to: COS_DEST, amount: '1000000', denom: 'uatom', fee: '5000', fee_denom: 'uatom', gas_limit: 200000, balance: '9000000' });
      case '/cosmos/finalize': return reply(200, { txhash: 'AB'.repeat(32), raw_tx_base64: 'cmF3', broadcast: body.broadcast === true });
      case '/solana/balance': return reply(200, { address: 'SoLAddr', lamports: '42', asset: 'SOL', decimals: 9 });
      case '/cosmos/balance': return reply(200, { address: 'cosmos1x', denom: 'uatom', amount: '77' });
      case '/solana/status': return reply(200, { found: true, confirmation_status: 'finalized' });
      case '/cosmos/status': return reply(200, { found: true, height: '900', code: 0 });
    }
    throw new Error(`unexpected request to ${href}`);
  }) as unknown as typeof fetch;
  return calls;
}

function build(opts: { key?: unknown; approved?: boolean; sign?: jest.Mock } = {}) {
  const postgres = {
    getKey: jest.fn().mockResolvedValue(opts.key),
    getCompletedCeremonyForKey: jest.fn().mockResolvedValue(ceremony),
    findSigningRequestByIdempotencyKey: jest.fn().mockResolvedValue(null),
    createSigningRequest: jest.fn().mockResolvedValue(undefined),
    completeSigningRequest: jest.fn().mockResolvedValue(undefined),
    failSigningRequest: jest.fn().mockResolvedValue(undefined),
    recordTransfer: jest.fn().mockResolvedValue(undefined),
  } as unknown as PostgresService;
  const temporal = {
    signWithThreshold: opts.sign ?? jest.fn().mockResolvedValue({ status: 'completed', signature: ED_SIG }),
  } as unknown as KeysTemporalService;
  const policy = {
    evaluate: jest.fn().mockResolvedValue({ approved: opts.approved !== false, denials: opts.approved === false ? ['amount_limit'] : [], requiresApproval: false, reason: 'x' }),
  } as unknown as PolicyService;
  return { service: new KeysService(postgres, temporal, policy), postgres, temporal, policy };
}

beforeEach(() => { process.env.MPC_SIGNER_URL = 'http://signer:8080'; });
afterEach(() => { jest.restoreAllMocks(); delete process.env.MPC_SIGNER_URL; });

describe('KeysService.sendSolana', () => {
  it('signs the exact message the signer prepared and hands the signature back to be verified and relayed', async () => {
    const calls = mockSigner();
    const { service, temporal } = build({ key: solKey });

    const res = await service.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1000000000' });

    expect((temporal.signWithThreshold as jest.Mock).mock.calls[0][0].message).toBe(SOL_MESSAGE);
    expect(calls['/solana/finalize'][0]).toEqual({ message_hex: SOL_MESSAGE, signature_hex: ED_SIG, broadcast: true });
    expect(res).toMatchObject({ signature: 'SolSig', broadcast: true, asset: 'SOL', from: 'SoLAddr', fee: '5000' });
  });

  it('presents the amount to policy in 18-decimal units, so one SOL is one whole coin', async () => {
    mockSigner();
    const { service, policy } = build({ key: solKey });
    await service.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1000000000' });
    expect(policy.evaluate).toHaveBeenCalledWith(expect.objectContaining({ to: SOL_DEST, value: '1000000000000000000', chainId: 501 }));
  });

  it('asks the signer nothing and signs nothing when policy refuses', async () => {
    const calls = mockSigner();
    const { service, temporal } = build({ key: solKey, approved: false });
    await expect(service.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1' })).rejects.toThrow();
    expect(calls['/solana/prepare']).toBeUndefined();
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  it('does not run a ceremony when the signer refuses the spend', async () => {
    mockSigner({ prepareStatus: 400, prepareError: 'insufficient balance: need 1000005000 lamports' });
    const { service, temporal } = build({ key: solKey });
    await expect(service.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1000000000' })).rejects.toBeInstanceOf(BadRequestException);
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  it('refuses a ceremony result that is not a 64-byte Ed25519 signature, before relaying anything', async () => {
    const calls = mockSigner();
    const { service } = build({ key: solKey, sign: jest.fn().mockResolvedValue({ status: 'completed', signature: SECP_SIG }) });
    await expect(service.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1000000000' })).rejects.toBeInstanceOf(ServiceUnavailableException);
    expect(calls['/solana/finalize']).toBeUndefined();
  });

  it('records a long message by its SHA-256 but still signs the whole message', async () => {
    mockSigner();
    const { service, postgres } = build({ key: solKey });
    await service.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1000000000' });
    const row = (postgres.createSigningRequest as jest.Mock).mock.calls[0][0];
    expect(row.transactionHash).toBe(createHash('sha256').update(Buffer.from(SOL_MESSAGE, 'hex')).digest('hex'));
    expect(row.transactionHash).toHaveLength(64);
    expect(row.transactionData.equals(Buffer.from(SOL_MESSAGE, 'hex'))).toBe(true);
  });

  it('records the transfer for the compliance aggregate', async () => {
    mockSigner();
    const { service, postgres } = build({ key: solKey });
    await service.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1000000000' });
    expect(postgres.recordTransfer).toHaveBeenCalledWith(expect.objectContaining({
      chain: 'solana', assetSymbol: 'SOL', assetDecimals: 9, effectiveTo: SOL_DEST, effectiveAmount: '1000000000', txHash: 'SolSig',
    }));
  });

  it('refuses another chain\'s key', async () => {
    mockSigner();
    const { service } = build({ key: ethKey });
    await expect(service.sendSolana(customer, 'eth-1', { destination: SOL_DEST, amount: '1' })).rejects.toThrow(/ethereum key/);
  });
});

describe('KeysService.sendCosmos', () => {
  const cosSign = () => jest.fn().mockResolvedValue({ status: 'completed', signature: SECP_SIG });

  it('signs the digest the signer prepared and returns the r and s it needs', async () => {
    const calls = mockSigner();
    const { service, temporal } = build({ key: cosKey, sign: cosSign() });

    const res = await service.sendCosmos(customer, 'cos-1', { destination: COS_DEST, amount: '1000000' });

    expect((temporal.signWithThreshold as jest.Mock).mock.calls[0][0].message).toBe(COS_DIGEST);
    expect(calls['/cosmos/finalize'][0]).toMatchObject({ r: 'aa'.repeat(32), s: 'bb'.repeat(32), pubkey_hex: cosKey.public_key, broadcast: true });
    expect(res).toMatchObject({ txhash: 'AB'.repeat(32), denom: 'uatom', fee: '5000', chain_id: 'cosmoshub-4' });
  });

  it('presents one ATOM to policy as one whole coin', async () => {
    mockSigner();
    const { service, policy } = build({ key: cosKey, sign: cosSign() });
    await service.sendCosmos(customer, 'cos-1', { destination: COS_DEST, amount: '1000000' });
    expect(policy.evaluate).toHaveBeenCalledWith(expect.objectContaining({ value: '1000000000000000000', chainId: 118 }));
  });

  it('does not run a ceremony when the signer refuses the spend', async () => {
    mockSigner({ prepareStatus: 400 });
    const { service, temporal } = build({ key: cosKey, sign: cosSign() });
    await expect(service.sendCosmos(customer, 'cos-1', { destination: COS_DEST, amount: '1000000' })).rejects.toBeInstanceOf(BadRequestException);
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  it('refuses a Solana key', async () => {
    mockSigner();
    const { service } = build({ key: solKey });
    await expect(service.sendCosmos(customer, 'sol-1', { destination: COS_DEST, amount: '1' })).rejects.toThrow(/solana key/);
  });
});

// The policy service says "approved: true, requiresApproval: true" for a
// high-value transfer. enforcePolicy used to read only `approved`, so every
// route signed such a transfer at once.
describe('transfers that policy says need approval', () => {
  const needsApproval = { approved: true, denials: [], requiresApproval: true, approvalReasons: ['high-value transaction (> 10 ETH) requires approval'], reason: 'approved, manual approval required' };

  it.each([
    ['solana', solKey, (s: KeysService) => s.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1000000000' })],
    ['cosmos', cosKey, (s: KeysService) => s.sendCosmos(customer, 'cos-1', { destination: COS_DEST, amount: '1000000' })],
  ])('are not signed on a %s key', async (_n, key, run) => {
    const calls = mockSigner();
    const { service, policy, temporal } = build({ key });
    (policy.evaluate as jest.Mock).mockResolvedValue(needsApproval);
    await expect(run(service)).rejects.toMatchObject({ reasons: ['high-value transaction (> 10 ETH) requires approval'] });
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
    expect(calls['/solana/prepare']).toBeUndefined();
    expect(calls['/cosmos/prepare']).toBeUndefined();
  });

  it('are not signed on an EVM key either', async () => {
    mockSigner();
    const { service, policy, temporal } = build({ key: ethKey });
    (policy.evaluate as jest.Mock).mockResolvedValue(needsApproval);
    await expect(service.signTransaction(customer, 'eth-1', {
      to: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', value: '42500000000000000000', gasLimit: 21000, nonce: 0, chainId: 1, gasPrice: '1',
    } as never)).rejects.toThrow(/needs approval/);
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  it('are signed when they are being executed after their approval reached quorum', async () => {
    mockSigner();
    const { service, policy, temporal } = build({ key: solKey });
    (policy.evaluate as jest.Mock).mockResolvedValue(needsApproval);
    const res = await service.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1000000000' }, { approvalGranted: true });
    expect(res.broadcast).toBe(true);
    expect(temporal.signWithThreshold).toHaveBeenCalled();
  });

  it('a policy denial is never lifted by an approval', async () => {
    mockSigner();
    const { service } = build({ key: solKey, approved: false });
    await expect(service.sendSolana(customer, 'sol-1', { destination: SOL_DEST, amount: '1' }, { approvalGranted: true })).rejects.toThrow();
  });
});

describe('keeping other chains\' keys out of the EVM routes', () => {
  it.each([['solana', solKey], ['cosmos', cosKey]])('POST :keyId/transactions refuses a %s key', async (_n, key) => {
    mockSigner();
    const { service, temporal } = build({ key });
    await expect(service.signTransaction(customer, key.key_id, {
      to: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', value: '1', gasLimit: 21000, nonce: 0, chainId: 1, gasPrice: '1',
    } as never)).rejects.toThrow(/route builds and signs an EVM transaction/);
    expect(temporal.signWithThreshold).not.toHaveBeenCalled();
  });

  it('POST :keyId/token-transfers refuses a cosmos key', async () => {
    mockSigner();
    const { postgres, temporal, policy } = build({ key: cosKey });
    // A registry must exist or the route stops earlier for a different reason.
    const tokens = { requireTransactable: jest.fn() } as never;
    const service = new KeysService(postgres, temporal, policy, undefined, tokens);
    await expect(service.sendToken(customer, 'cos-1', { token: 'USDC', recipient: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', amount: '1', chainId: 1 } as never))
      .rejects.toThrow(/cosmos key/);
    expect((tokens as { requireTransactable: jest.Mock }).requireTransactable).not.toHaveBeenCalled();
  });
});

describe('balances and addresses for non-EVM keys', () => {
  it('reads a Solana balance through the signer without an EVM chain id', async () => {
    mockSigner();
    const { service } = build({ key: solKey });
    expect(await service.getBalancesForKey(customer, 'sol-1')).toMatchObject({ blockchain: 'solana', balances: [{ asset: 'SOL', amount: '42', decimals: 9 }] });
  });
  it('reads a Cosmos balance, defaulting the denomination', async () => {
    mockSigner();
    const { service } = build({ key: cosKey });
    expect(await service.getBalancesForKey(customer, 'cos-1')).toMatchObject({ balances: [{ asset: 'uatom', amount: '77' }] });
  });
  it('still insists on a chain id for an EVM key', async () => {
    mockSigner();
    const { service } = build({ key: ethKey });
    await expect(service.getBalancesForKey(customer, 'eth-1')).rejects.toBeInstanceOf(BadRequestException);
  });
  it('derives the deposit address through the signer', async () => {
    mockSigner();
    const { service } = build({ key: solKey });
    expect(await service.getDepositAddresses(customer, 'sol-1')).toMatchObject({ addresses: { preferred: 'SoLAddr' } });
  });
});
