import { ForbiddenException, NotFoundException, UnprocessableEntityException } from '@nestjs/common';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { SweepsService } from './sweeps.service';

// Sweep rules and runs over real Postgres, with the transfer path, the key
// store and the chain faked at their edges: what is under test is the rule
// storage, the database's refusals, the arithmetic wiring, and that a run goes
// through the transfer path (so freeze, whitelist and approvals apply).
//
//   eval "$(infrastructure/local/postgres-local.sh start)"   # migrations through 031
//   npx jest src/sweeps

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const SOL_KEY_ADDR = '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM';
const SOL_TREASURY = 'Vote111111111111111111111111111111111111111';
const EVM_TREASURY = '0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045';

let reachable = false;
beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try { reachable = (await p.query(`SELECT to_regclass('sweep_rules') IS NOT NULL AS ok`)).rows[0].ok; } catch { reachable = false; } finally { await p.end().catch(() => undefined); }
});
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 031 is reachable');
  console.warn('skipping sweeps live test -- no database with migration 031');
  return true;
}

describe('deposit sweeps (live Postgres)', () => {
  let admin: Pool; let tenant: Pool; let svc: SweepsService;
  let org = ''; let other = ''; let user = '';
  let solKey = ''; let evmKey = '';
  let balance = 0n; let balanceError: Error | null = null;
  const submitted: any[] = []; let submitBehaviour: () => any = () => ({ status: 'completed', result: { transaction_hash: 'abc' } });
  const alerts: string[] = []; let whitelistRefuses = false;

  const keys: any = {
    getKey: async (id: string) => (id === solKey
      ? { key_id: id, status: 'active', blockchain: 'solana', address: SOL_KEY_ADDR }
      : id === evmKey ? { key_id: id, status: 'active', blockchain: 'ethereum', address: '0x1111111111111111111111111111111111111111' } : null),
    getBalancesForKey: async (_c: unknown, id: string) => {
      if (balanceError) throw balanceError;
      return id === solKey ? { balances: [{ asset: 'SOL', amount: balance.toString(), decimals: 9 }] } : { native: { balance_wei: balance.toString() } };
    },
  };
  const transfers: any = { submit: async (...a: any[]) => { submitted.push(a); return submitBehaviour(); } };
  const controls: any = { assertDestinationAllowed: async () => { if (whitelistRefuses) throw new ForbiddenException('not on the whitelist'); } };
  const alertsSvc: any = { notify: async (a: { title: string }) => { alerts.push(a.title); } };
  const customers: any = { getByCustomerId: async (id: string) => ({ customer_id: id, name: 'Test Org' }) };

  beforeAll(async () => {
    if (!reachable) return;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN;
    admin = new Pool({ connectionString: ADMIN_DSN }); tenant = new Pool({ connectionString: TENANT_DSN });
    const mk = async () => (await admin.query(`INSERT INTO customers (name, api_key_hash) VALUES ('sweeps-test', decode(md5(clock_timestamp()::text || random()::text), 'hex')) RETURNING customer_id`)).rows[0].customer_id;
    org = await mk(); other = await mk();
    user = (await admin.query(`INSERT INTO users (email, password_hash, full_name) VALUES ($1, 'x', 'admin') RETURNING id`, [`sw-${randomUUID()}@example.test`])).rows[0].id;
    solKey = randomUUID(); evmKey = randomUUID();
    svc = new SweepsService(tenant, customers, keys, transfers, controls, alertsSvc);
  });
  afterAll(async () => { if (!reachable) return; await admin.end(); await tenant.end(); });
  beforeEach(() => { submitted.length = 0; alerts.length = 0; balanceError = null; whitelistRefuses = false; submitBehaviour = () => ({ status: 'completed', result: { transaction_hash: 'abc' } }); });

  const solRule = (over: Record<string, unknown> = {}) => ({ name: 'treasury', keyId: solKey, destination: SOL_TREASURY, minAmount: '1000000', reserve: '5000', ...over });

  it('creates a rule and refuses the things that would make one useless or dangerous', async () => {
    if (skipped()) return;
    const r = await svc.create(org, user, solRule());
    expect(r).toMatchObject({ blockchain: 'solana', minAmount: '1000000', reserve: '5000', enabled: true });
    await expect(svc.create(org, user, solRule({ keyId: randomUUID() }))).rejects.toThrow(NotFoundException);
    await expect(svc.create(org, user, solRule({ destination: SOL_KEY_ADDR }))).rejects.toThrow(/own address/);
    await expect(svc.create(org, user, solRule({ destination: 'not-an-address' }))).rejects.toThrow();
    await expect(svc.create(org, user, solRule({ minAmount: '0' }))).rejects.toThrow(/positive/);
    await expect(svc.create(org, user, solRule({ reserve: '-1' }))).rejects.toThrow(/reserve/);
    await expect(svc.create(org, user, { name: 'e', keyId: evmKey, destination: EVM_TREASURY, minAmount: '1' })).rejects.toThrow(/chainId/);
    whitelistRefuses = true;
    await expect(svc.create(org, user, solRule())).rejects.toThrow(ForbiddenException);
  });

  it('keeps a rule fixed in the database: destination, amounts and key cannot be edited, only switched off or deleted', async () => {
    if (skipped()) return;
    const r = await svc.create(org, user, solRule({ name: 'fixed' }));
    for (const col of ["destination = 'Another1111111111111111111111111111111111'", 'min_amount = 1', 'reserve = 0', `key_id = '${randomUUID()}'`]) {
      await expect(admin.query(`UPDATE sweep_rules SET ${col} WHERE rule_id = $1`, [r.ruleId])).rejects.toMatchObject({ code: 'OFB06' });
    }
    await expect(admin.query(`DELETE FROM sweep_rules WHERE rule_id = $1`, [r.ruleId])).rejects.toMatchObject({ code: 'OFB06' });
    expect((await svc.setEnabled(org, r.ruleId, false)).enabled).toBe(false);
    await svc.remove(org, r.ruleId);
    await expect(admin.query(`UPDATE sweep_rules SET enabled = true, deleted_at = NULL WHERE rule_id = $1`, [r.ruleId])).rejects.toMatchObject({ code: 'OFB06' });
    expect((await svc.list(org)).find((x) => x.ruleId === r.ruleId)).toBeUndefined();
  });

  it('is invisible to another organisation', async () => {
    if (skipped()) return;
    const r = await svc.create(org, user, solRule({ name: 'private' }));
    expect(await svc.list(other)).toEqual([]);
    await expect(svc.run(other, r.ruleId, 'manual', 'x', null)).rejects.toThrow(NotFoundException);
    await expect(svc.setEnabled(other, r.ruleId, false)).rejects.toThrow(NotFoundException);
    expect(await svc.runs(other)).toEqual([]);
  });

  it('skips below the threshold and records why, without submitting anything', async () => {
    if (skipped()) return;
    const r = await svc.create(org, user, solRule({ name: 'skip' }));
    balance = 900_000n;
    const run = await svc.run(org, r.ruleId, 'manual', 'oscar', user);
    expect(run).toMatchObject({ status: 'skipped', balance: '900000', amount: null });
    expect(run.reason).toMatch(/below the threshold/);
    expect(submitted).toHaveLength(0);
  });

  it('sweeps balance minus reserve through the transfer path as a named initiator', async () => {
    if (skipped()) return;
    const r = await svc.create(org, user, solRule({ name: 'go' }));
    balance = 2_500_000n;
    const run = await svc.run(org, r.ruleId, 'manual', 'oscar@example.test', user);
    expect(run).toMatchObject({ status: 'completed', balance: '2500000', amount: '2495000' });
    expect(submitted).toHaveLength(1);
    const [, keyId, kind, dto, initiator] = submitted[0];
    expect(keyId).toBe(solKey); expect(kind).toBe('solana');
    expect(dto).toMatchObject({ destination: SOL_TREASURY, amount: '2495000' });
    expect(initiator).toEqual({ userId: user, label: 'oscar@example.test' });
  });

  it('carries the chain id for an EVM key and the Travel Rule details when the rule has them', async () => {
    if (skipped()) return;
    const r = await svc.create(org, user, { name: 'evm', keyId: evmKey, chainId: 137, destination: EVM_TREASURY, minAmount: '1000', reserve: '100', travelRule: { originator: { name: 'Test Org' } } });
    balance = 5_000n;
    const run = await svc.run(org, r.ruleId, 'manual', 'x', user);
    expect(run).toMatchObject({ status: 'completed', amount: '4900' });
    const dto = submitted[0][3];
    expect(dto).toMatchObject({ chainId: 137, destination: EVM_TREASURY, amount: '4900' });
    expect(dto.travelRule).toEqual({ originator: { name: 'Test Org' } });
  });

  it('records an approval wait when policy holds the sweep, and the approval id to follow', async () => {
    if (skipped()) return;
    const r = await svc.create(org, user, solRule({ name: 'held' }));
    balance = 90_000_000_000n;
    const approvalId = randomUUID();
    submitBehaviour = () => ({ status: 'pending_approval', approvalId, expiresAt: 'x', requiredApprovals: 2, reasons: ['large'] });
    const run = await svc.run(org, r.ruleId, 'manual', 'x', user);
    expect(run).toMatchObject({ status: 'pending_approval', approvalId, amount: '89999995000' });
  });

  it('records a freeze, whitelist or policy block as refused (a decision, not a fault) and a real failure as failed with an alert', async () => {
    if (skipped()) return;
    const r = await svc.create(org, user, solRule({ name: 'refusals' }));
    balance = 3_000_000n;
    submitBehaviour = () => { throw new ForbiddenException('the organisation is frozen'); };
    expect(await svc.run(org, r.ruleId, 'manual', 'x', user)).toMatchObject({ status: 'refused', error: expect.stringContaining('frozen') });
    submitBehaviour = () => { throw new UnprocessableEntityException('blocked by policy'); };
    expect((await svc.run(org, r.ruleId, 'manual', 'x', user)).status).toBe('refused');
    expect(alerts).not.toContain('A sweep failed');
    submitBehaviour = () => { throw new Error('signer unreachable'); };
    expect(await svc.run(org, r.ruleId, 'manual', 'x', user)).toMatchObject({ status: 'failed', error: 'signer unreachable' });
    expect(alerts).toContain('A sweep failed');
  });

  it('records a failed balance read instead of throwing, and will not run a switched-off rule', async () => {
    if (skipped()) return;
    const r = await svc.create(org, user, solRule({ name: 'rpc-down' }));
    balanceError = new Error('node unreachable');
    const run = await svc.run(org, r.ruleId, 'manual', 'x', user);
    expect(run).toMatchObject({ status: 'failed' });
    expect(run.error).toMatch(/could not read the balance: node unreachable/);
    expect(submitted).toHaveLength(0);
    await svc.setEnabled(org, r.ruleId, false);
    await expect(svc.run(org, r.ruleId, 'manual', 'x', user)).rejects.toThrow(UnprocessableEntityException);
  });

  it('lists the runs of the organisation, newest first', async () => {
    if (skipped()) return;
    const runs = await svc.runs(org);
    expect(runs.length).toBeGreaterThan(5);
    const times = runs.map((x) => Date.parse(x.createdAt));
    expect([...times].sort((a, b) => b - a)).toEqual(times);
  });

  it('the scheduler runs a due rule once even when two replicas tick at the same moment', async () => {
    if (skipped()) return;
    await admin.query(`UPDATE sweep_rules SET enabled = false WHERE deleted_at IS NULL`);
    // The scheduler is cross-tenant: every other rule in this shared test
    // database is switched off so only this test's rule is due.
    const r = await svc.create(org, user, solRule({ name: 'scheduled', intervalSeconds: 60 }));
    balance = 4_000_000n;
    const ran = await Promise.all([svc.tick(), svc.tick()]);
    expect(ran.reduce((a, b) => a + b, 0)).toBe(1);
    expect(submitted).toHaveLength(1);
    expect((await svc.runs(org, r.ruleId))[0]).toMatchObject({ trigger: 'schedule', status: 'completed' });
    // Not due again within its interval.
    expect(await svc.tick()).toBe(0);
  });
});
