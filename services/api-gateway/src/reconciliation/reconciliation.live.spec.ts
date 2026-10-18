import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { JsonRpcProvider, Wallet, Interface, NonceManager, parseEther } from 'ethers';
import { ReconciliationService } from './reconciliation.service';
import { EvmRpcService } from '../tokens/evm-rpc.service';

// Reconciliation against a real EVM chain and a real Postgres.
//
// Needs a dev chain with id 1337 in which DEPLOYER below is funded
// (Ganache or Anvil), at RECON_EVM_RPC:
//
//   npx ganache@7.9.2 --chain.chainId 1337 --server.port 18545 \
//     --wallet.accounts "0x59c6...690d,1000000000000000000000" \
//     --wallet.accounts "0x5de4...365a,1000000000000000000000"
//   RECON_EVM_RPC=http://127.0.0.1:18545 REQUIRE_LIVE_DB=1 npx jest src/reconciliation
//
// The contracts are assembled by hand so the test needs no compiler:
//   REVERTER  -- runtime 60006000fd: every call reverts
//   TOKEN     -- on any call, emits Transfer(caller, arg0, arg1) and returns
//                true: a real ERC-20 Transfer event, from real calldata

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const RPC = process.env.RECON_EVM_RPC ?? '';
const CHAIN = 1337;

const DEPLOYER = '0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a';
const TO = '0x70997970C51812dc3A010C7d01b50e0d17dc79C8';

const REVERTER_INIT = '0x600580600b6000396000f3' + '60006000fd';
const TRANSFER_T0 = 'ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef';
const TOKEN_RUNTIME =
  '602435' + '600052' + '600435' + '33' + '7f' + TRANSFER_T0 + '60206000a3' + '6001600052' + '60206000f3';
const tokenInit = () => {
  const len = (TOKEN_RUNTIME.length / 2).toString(16).padStart(2, '0');
  // PUSH1 len DUP1 PUSH1 0x0b PUSH1 0 CODECOPY PUSH1 0 RETURN, then runtime
  return '0x60' + len + '80600b6000396000f3' + TOKEN_RUNTIME;
};

let ready = false;
function skipped(): boolean {
  if (ready) return false;
  if (process.env.REQUIRE_LIVE_DB && process.env.RECON_EVM_RPC) {
    throw new Error('RECON_EVM_RPC and REQUIRE_LIVE_DB are set but the chain or database is not reachable');
  }
  console.warn('skipping reconciliation live tests -- set RECON_EVM_RPC to a dev chain and start Postgres');
  return true;
}

describe('reconciliation against a real chain (live)', () => {
  let admin: Pool;
  let tenant: Pool;
  let provider: JsonRpcProvider;
  let service: ReconciliationService;
  let customerId: string;
  const hashes: Record<string, string> = {};

  async function record(name: string, signedTx: string, meta: { contract?: string; to?: string; amount?: string; createdAt?: Date }) {
    const hash = (await import('ethers')).Transaction.from(signedTx).hash!;
    hashes[name] = hash;
    await admin.query(
      `INSERT INTO signing.transactions (request_id, customer_id, chain, to_address, amount, signed_tx, tx_hash, status,
         asset_symbol, asset_contract, asset_decimals, effective_to, effective_amount, created_at)
       VALUES ($1, $2, 'ethereum', $3, $4, $5, $6, 'signed', $7, $8, $9, $10, $11, $12)`,
      [
        randomUUID(), customerId, meta.to ?? TO, meta.amount ?? '0', signedTx, hash,
        meta.contract ? 'TKN' : null, meta.contract ?? null, meta.contract ? 18 : null,
        meta.to ?? TO, meta.amount ?? null, (meta.createdAt ?? new Date()).toISOString().replace('Z', ''),
      ],
    );
    return hash;
  }

  beforeAll(async () => {
    if (!RPC) return;
    admin = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
    try {
      provider = new JsonRpcProvider(RPC, CHAIN, { staticNetwork: true });
      await provider.getBlockNumber();
      ready = (await admin.query(`SELECT to_regclass('reconciliation_runs') IS NOT NULL AS ok`)).rows[0].ok;
    } catch {
      ready = false;
    }
    if (!ready) return;

    process.env[`EVM_RPC_${CHAIN}`] = RPC;
    tenant = new Pool({ connectionString: TENANT_DSN });
    service = new ReconciliationService(tenant, new EvmRpcService());
    customerId = randomUUID();
    await admin.query(
      `INSERT INTO customers (customer_id, name, email, api_key_hash, status, tier)
       VALUES ($1, 'Recon Co', $2, decode(md5(random()::text), 'hex'), 'active', 'enterprise')`,
      [customerId, `recon-${customerId}@example.test`],
    );

    // A fresh key every run, so a rerun against the same chain starts from
    // nonce 0 and the expected nonce gaps are exact.
    const deployer = new NonceManager(new Wallet(DEPLOYER, provider));
    const reverter = (await (await deployer.sendTransaction({ data: REVERTER_INIT })).wait())!.contractAddress!;
    const token = (await (await deployer.sendTransaction({ data: tokenInit() })).wait())!.contractAddress!;
    const key = Wallet.createRandom().connect(provider);
    await (await deployer.sendTransaction({ to: key.address, value: parseEther('10') })).wait();
    await admin.query(
      `INSERT INTO key_pairs (customer_id, name, blockchain, threshold, total_parties, status, address)
       VALUES ($1, 'treasury', 'ethereum', 1, 3, 'active', $2)`,
      [customerId, key.address],
    );
    const base = { chainId: CHAIN, gasPrice: 2_000_000_000, gasLimit: 100_000 };
    let nonce = await provider.getTransactionCount(key.address);
    const erc20 = new Interface(['function transfer(address,uint256)']);
    const sign = (tx: object) => key.signTransaction({ ...base, nonce: nonce++, ...tx });
    const send = async (raw: string) => (await provider.broadcastTransaction(raw)).wait().catch(() => undefined);

    // Signed by the platform and broadcast:
    let raw = await sign({ to: TO, value: 12345n });
    await record('native', raw, { amount: '12345' });
    await send(raw);

    raw = await sign({ to: token, data: erc20.encodeFunctionData('transfer', [TO, 1000n]) });
    await record('token', raw, { contract: token, amount: '1000' });
    await send(raw);

    // The ledger says 999 moved; the transaction moved 1000.
    raw = await sign({ to: token, data: erc20.encodeFunctionData('transfer', [TO, 1000n]) });
    await record('wrongAmount', raw, { contract: token, amount: '999' });
    await send(raw);

    raw = await sign({ to: reverter, data: '0x01' });
    await record('reverted', raw, { to: reverter });
    await send(raw);

    // Somebody else uses the key: a transaction the platform never signed.
    await (await key.sendTransaction({ ...base, to: TO, value: 1n, nonce: nonce++ })).wait();

    // Signed, never broadcast: one two hours ago, one just now.
    await record('neverSent', await sign({ to: TO, value: 1n }), { amount: '1', createdAt: new Date(Date.now() - 2 * 3600_000) });
    await record('justSigned', await sign({ to: TO, value: 1n }), { amount: '1' });
  }, 60000);

  afterAll(async () => {
    await tenant?.end();
    await admin?.end();
    provider?.destroy();
  });

  it('classifies every signed transfer, and finds the transaction nobody here signed', async () => {
    if (skipped()) return;
    const run = await service.runChain({ customerId, chainId: CHAIN, requestedBy: 'test' });
    expect(run.summary).toMatchObject({
      counts: { confirmed: 2, mismatch: 1, failed: 1, missing: 1, recent: 1, unsigned_outbound: 1 },
      needsAttention: true,
      critical: 2,
    });
    const by = (h: string) => run.breaks.find((b: any) => b.txHash === h);
    expect(by(hashes.wrongAmount)).toMatchObject({ classification: 'mismatch', severity: 'critical' });
    expect((by(hashes.wrongAmount) as any).detail).toMatch(/ledger says 999.*chain shows 1000/);
    expect(by(hashes.reverted)).toMatchObject({ classification: 'failed' });
    expect(by(hashes.neverSent)).toMatchObject({ classification: 'missing' });
    expect(by(hashes.justSigned)).toMatchObject({ classification: 'recent' });
    expect(by(hashes.native)).toBeUndefined(); // confirmed is not a break
    const unsigned = run.breaks.find((b: any) => b.classification === 'unsigned_outbound') as any;
    expect(unsigned.detail).toMatch(/nonce 4 on chain, and this platform never signed/);
  });

  it('compares a customer statement with what was signed', async () => {
    if (skipped()) return;
    const run = await service.runStatement({
      customerId,
      requestedBy: 'test',
      periodStart: new Date(Date.now() - 3 * 3600_000).toISOString(),
      periodEnd: new Date(Date.now() + 60_000).toISOString(),
      rows: [
        { txHash: hashes.native, asset: 'ETH', amount: '12345' },
        { txHash: hashes.token, asset: 'TKN', amount: '1000' },
        { txHash: hashes.reverted, asset: 'ETH', amount: '5' },
        { txHash: '0x' + 'ab'.repeat(32), asset: 'ETH', amount: '1' },
      ],
    });
    expect(run.summary).toMatchObject({ matched: 2, needsAttention: true });
    const kinds = run.breaks.map((b: any) => b.classification);
    expect(kinds).toContain('amount_mismatch');
    expect(kinds).toContain('missing_in_platform');
    expect(kinds.filter((k: string) => k === 'missing_in_statement').length).toBe(3); // wrongAmount, neverSent, justSigned
  });

  it('a run is evidence: it cannot be changed or deleted', async () => {
    if (skipped()) return;
    const runs = await service.list(customerId);
    for (const stmt of [`UPDATE reconciliation_runs SET breaks = '[]' WHERE run_id = $1`, `DELETE FROM reconciliation_runs WHERE run_id = $1`]) {
      const e = await admin.query(stmt, [runs[0].runId]).catch((x) => x);
      expect(e.code).toBe('OFB04');
    }
  });
});
