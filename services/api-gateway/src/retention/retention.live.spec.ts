import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { RetentionService } from './retention.service';
import { CustomerService } from '../customers/customer.service';

// Travel Rule retention over real Postgres (migration 037).
//
//   eval "$(infrastructure/local/postgres-local.sh start)"
//   npx jest src/retention/retention.live.spec.ts

const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';

let reachable = false;
beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try { reachable = (await p.query(`SELECT to_regproc('purge_travel_rule') IS NOT NULL AS ok`)).rows[0].ok; } catch { reachable = false; } finally { await p.end().catch(() => undefined); }
});
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 037 is reachable');
  console.warn('skipping retention live test -- no database with migration 037');
  return true;
}

describe('Travel Rule retention (live Postgres)', () => {
  let admin: Pool; let svc: RetentionService; let customers: CustomerService;
  let org = ''; let other = '';
  const ids: Record<string, string> = {};

  async function record(name: string, customerId: string, ageDays: number, status: string) {
    const r = await admin.query(
      `INSERT INTO travel_rule_records (customer_id, request_id, chain_id, asset, amount, asset_decimals, originator_address,
         beneficiary_address, value_zar, valuation, threshold_zar, ivms101, beneficiary_unhosted, transmission_status, created_at)
       VALUES ($1, $2, 1, 'ETH', 1, 18, '0xaaa', '0xbbb', 6000, 'test', 5000, '{}', $3, $4, now() - make_interval(days => $5))
       RETURNING record_id`,
      [customerId, `rt-${name}-${randomUUID()}`, status === 'not_applicable_unhosted', status, ageDays],
    );
    ids[name] = r.rows[0].record_id;
  }
  const exists = async (name: string) => (await admin.query(`SELECT 1 FROM travel_rule_records WHERE record_id = $1`, [ids[name]])).rowCount === 1;

  beforeAll(async () => {
    if (!reachable) return;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN;
    admin = new Pool({ connectionString: ADMIN_DSN });
    svc = new RetentionService(); customers = new CustomerService();
    org = (await customers.createCustomer({ email: `ret-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    other = (await customers.createCustomer({ email: `ret-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    await record('old', org, 6 * 365 + 2, 'transmitted');
    await record('oldUnhosted', org, 6 * 365 + 2, 'not_applicable_unhosted');
    await record('oldFailed', org, 6 * 365 + 2, 'failed');        // still needs attention: kept
    await record('young', org, 4 * 365, 'transmitted');           // under five years: kept
    await record('otherOld', other, 6 * 365 + 2, 'transmitted');  // another organisation
  });
  afterAll(async () => {
    if (!reachable) return;
    await svc.onModuleDestroy(); await admin.end();
    await ((customers as unknown as { pool?: Pool }).pool?.end().catch(() => undefined));
  });

  it('refuses a direct delete, even of an old record, outside a purge', async () => {
    if (skipped()) return;
    await expect(admin.query(`DELETE FROM travel_rule_records WHERE record_id = $1`, [ids.old])).rejects.toMatchObject({ code: 'OFB04' });
  });

  it('refuses a delete of a young record even inside a purge session', async () => {
    if (skipped()) return;
    const c = await admin.connect();
    try {
      await c.query('BEGIN'); await c.query(`SELECT set_config('ofb.retention_purge', 'on', true)`);
      await expect(c.query(`DELETE FROM travel_rule_records WHERE record_id = $1`, [ids.young])).rejects.toMatchObject({ code: 'OFB04' });
    } finally { await c.query('ROLLBACK'); c.release(); }
  });

  it('refuses a retention period shorter than five years', async () => {
    if (skipped()) return;
    await expect(svc.purgeTravelRule({ retainDays: 1500, reason: 'too short', dryRun: true })).rejects.toThrow(/at least 1826/);
    const c = await admin.connect();
    try {
      await expect(c.query(`SELECT * FROM purge_travel_rule(interval '4 years', 'direct call', NULL, false)`)).rejects.toMatchObject({ code: 'OFB08' });
    } finally { c.release(); }
  });

  it('counts without deleting by default, and logs the dry run', async () => {
    if (skipped()) return;
    const r = await svc.purgeTravelRule({ customerId: org, reason: 'quarterly retention check' });
    expect(r.dryRun).toBe(true);
    expect(r.results.find((x) => x.table === 'travel_rule_records')!.rows).toBe(2);
    expect(await exists('old')).toBe(true);
    expect((await svc.history(5)).find((h) => h.customerId === org && h.dryRun)).toMatchObject({ rows: 2, reason: 'quarterly retention check' });
  });

  it('deletes only finished records past retention for the organisation asked, and logs it', async () => {
    if (skipped()) return;
    const r = await svc.purgeTravelRule({ customerId: org, dryRun: false, reason: 'FIC Act five-year retention expired' });
    expect(r.results.find((x) => x.table === 'travel_rule_records')!.rows).toBe(2);
    expect(await exists('old')).toBe(false);
    expect(await exists('oldUnhosted')).toBe(false);
    expect(await exists('oldFailed')).toBe(true);
    expect(await exists('young')).toBe(true);
    expect(await exists('otherOld')).toBe(true);
    const log = (await svc.history(10)).find((h) => h.customerId === org && !h.dryRun && h.table === 'travel_rule_records');
    expect(log).toMatchObject({ rows: 2, runBy: 'app_admin' });
  });

  it('keeps the retention log itself immutable', async () => {
    if (skipped()) return;
    await expect(admin.query(`DELETE FROM retention_purges`)).rejects.toMatchObject({ code: 'OFB08' });
    await expect(admin.query(`UPDATE retention_purges SET rows_deleted = 0`)).rejects.toMatchObject({ code: 'OFB08' });
  });
});
