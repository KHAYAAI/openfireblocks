import { ReconciliationService } from './reconciliation.service';
import { EvmRpcService } from '../tokens/evm-rpc.service';

// runNative against a stubbed database and a stubbed signer, so the control
// flow -- what is asked of the node, what is flagged, what is skipped -- is
// tested without a chain.

function pool(ledger: unknown[], keys: unknown[] = []) {
  const saved: unknown[][] = [];
  const client = {
    query: jest.fn(async (sql: string, args?: unknown[]) => {
      if (/FROM signing.transactions/.test(sql)) return { rows: ledger };
      if (/FROM key_pairs/.test(sql)) return { rows: keys };
      if (/INSERT INTO reconciliation_runs/.test(sql)) { saved.push(args!); return { rows: [{ run_id: 'run-1', started_at: new Date() }] }; }
      return { rows: [] };
    }),
    release: jest.fn(),
  };
  return { pool: { connect: async () => client } as never, saved };
}

function service(p: never, signer: Record<string, jest.Mock>) {
  const s = new ReconciliationService(p, new EvmRpcService());
  (s as unknown as { chainSigner: unknown }).chainSigner = signer;
  return s;
}

const row = (hash: string, to = 'DST', amount = '1000', minsAgo = 5) => ({
  request_id: hash, tx_hash: hash, effective_to: to, effective_amount: amount,
  created_at: new Date(Date.now() - minsAgo * 60000).toISOString().replace('Z', ''),
});

describe('ReconciliationService.runNative', () => {
  it('flags a Solana transaction that confirmed but moved something else, and a dropped one', async () => {
    const { pool: p } = pool([row('good'), row('wrong'), row('gone', 'DST', '1000', 120)]);
    const solanaStatus = jest.fn(async (sig: string) => {
      if (sig === 'good') return { found: true, confirmation_status: 'finalized', transfer: { from: 'S', to: 'DST', lamports: '1000' } };
      if (sig === 'wrong') return { found: true, confirmation_status: 'finalized', transfer: { from: 'S', to: 'ATTACKER', lamports: '1000' } };
      return { found: false };
    });
    const run = await service(p, { solanaStatus }).runNative({ customerId: 'c', blockchain: 'solana', requestedBy: 'ada' });

    expect(run.summary).toMatchObject({ blockchain: 'solana', chainId: 501, examined: 3, critical: 1, needsAttention: true });
    expect(run.breaks.map((b) => b.classification).sort()).toEqual(['mismatch', 'missing']);
    expect((run.summary as { sequenceCheck: string }).sequenceCheck).toMatch(/not applicable/);
  });

  it('flags Cosmos transactions the platform never signed when the chain sequence is ahead of the ledger', async () => {
    const { pool: p } = pool([row('a'), row('b')], [{ public_key: '04ab' }]);
    const signer = {
      cosmosStatus: jest.fn(async () => ({ found: true, code: 0, send: { from: 'F', to: 'DST', denom: 'uatom', amount: '1000' } })),
      cosmosAddress: jest.fn(async () => ({ address: 'cosmos1key' })),
      cosmosAccount: jest.fn(async () => ({ address: 'cosmos1key', exists: true, sequence: 5 })),
    };
    const run = await service(p, signer).runNative({ customerId: 'c', blockchain: 'cosmos', requestedBy: 'ada' });
    const unsigned = run.breaks.find((b) => b.classification === 'unsigned_outbound');
    expect(unsigned).toMatchObject({ severity: 'critical', address: 'cosmos1key' });
    expect(unsigned!.detail).toMatch(/sent 5 .* signed 2/);
  });

  it('does not raise a sequence alarm when the chain agrees with the ledger', async () => {
    const { pool: p } = pool([row('a')], [{ public_key: '04ab' }]);
    const signer = {
      cosmosStatus: jest.fn(async () => ({ found: true, code: 0, send: { from: 'F', to: 'DST', denom: 'uatom', amount: '1000' } })),
      cosmosAddress: jest.fn(async () => ({ address: 'cosmos1key' })),
      cosmosAccount: jest.fn(async () => ({ address: 'cosmos1key', exists: true, sequence: 1 })),
    };
    const run = await service(p, signer).runNative({ customerId: 'c', blockchain: 'cosmos', requestedBy: 'ada' });
    expect(run.breaks).toHaveLength(0);
    expect(run.summary).toMatchObject({ needsAttention: false });
  });

  it('says it skipped the sequence check rather than guessing when there are several Cosmos keys', async () => {
    const { pool: p } = pool([row('a')], [{ public_key: '04ab' }, { public_key: '04cd' }]);
    const signer = { cosmosStatus: jest.fn(async () => ({ found: true, code: 0 })), cosmosAddress: jest.fn(), cosmosAccount: jest.fn() };
    const run = await service(p, signer).runNative({ customerId: 'c', blockchain: 'cosmos', requestedBy: 'ada' });
    expect((run.summary as { sequenceCheck: string }).sequenceCheck).toMatch(/skipped: 2 Cosmos keys/);
    expect(signer.cosmosAccount).not.toHaveBeenCalled();
  });

  it('fails the run rather than calling a transaction missing when the node cannot be reached', async () => {
    const { pool: p } = pool([row('a')]);
    const solanaStatus = jest.fn(async () => { throw new Error('the chain signer is unreachable'); });
    await expect(service(p, { solanaStatus }).runNative({ customerId: 'c', blockchain: 'solana', requestedBy: 'ada' }))
      .rejects.toThrow(/could not read solana transaction/);
  });
});
