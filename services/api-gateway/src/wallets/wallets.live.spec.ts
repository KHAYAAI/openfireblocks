import { BadRequestException, ConflictException, ForbiddenException, NotFoundException } from '@nestjs/common';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { WalletsService } from './wallets.service';
import { PostgresService } from '../database/postgres.service';
import { assertWalletNotFrozen } from '../keys/keys.service';

// Per-user wallets over real Postgres. Key creation is faked at the edge (it
// inserts the key row a real createKey would, without a key ceremony), and
// the transfer path is faked: under test are the wallet records, the
// database's refusals, tenant isolation, idempotent creation, and that a
// frozen wallet cannot send.
//
//   eval "$(infrastructure/local/postgres-local.sh start)"   # migrations through 039
//   npx jest src/wallets

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';

let reachable = false;
beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try { reachable = (await p.query(`SELECT to_regclass('user_wallets') IS NOT NULL AS ok`)).rows[0].ok; } catch { reachable = false; } finally { await p.end().catch(() => undefined); }
});
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 039 is reachable');
  console.warn('skipping wallets live test -- no database with migration 039');
  return true;
}

describe('per-user wallets (live Postgres)', () => {
  let admin: Pool; let tenant: Pool; let svc: WalletsService; let postgres: PostgresService;
  let org: any; let other: any;
  const created: Array<{ name: string; blockchain: string }> = [];
  const submitted: any[] = [];

  const keys: any = {
    createKey: async (customer: { customer_id: string }, req: { name: string; blockchain: string; threshold: number; total_parties: number }) => {
      created.push({ name: req.name, blockchain: req.blockchain });
      const keyId = randomUUID();
      await postgres.createKey({ key_id: keyId, customer_id: customer.customer_id, name: req.name, blockchain: req.blockchain,
        threshold: req.threshold, total_parties: req.total_parties, status: 'pending_dkg' });
      return { id: keyId };
    },
    getDepositAddresses: async () => ({ addresses: { preferred: 'addr' } }),
    getBalancesForKey: async () => ({ balances: [] }),
  };
  const transfers: any = { submit: async (...a: any[]) => { submitted.push(a); return { status: 'completed', result: {} }; } };

  beforeAll(async () => {
    if (!reachable) return;
    admin = new Pool({ connectionString: ADMIN_DSN }); tenant = new Pool({ connectionString: TENANT_DSN });
    const mk = async () => ({ customer_id: (await admin.query(`INSERT INTO customers (name, api_key_hash) VALUES ('wallets-test', decode(md5(clock_timestamp()::text || random()::text), 'hex')) RETURNING customer_id`)).rows[0].customer_id, name: 'wallets-test' });
    org = await mk(); other = await mk();
    postgres = new PostgresService(tenant);
    svc = new WalletsService(tenant, keys, transfers);
  });
  afterAll(async () => { if (!reachable) return; await admin.end(); await tenant.end(); });
  beforeEach(() => { created.length = 0; submitted.length = 0; });

  it('creates a wallet with one threshold key per chain, and asking again returns the same wallet', async () => {
    if (skipped()) return;
    const ref = `user-${randomUUID()}`;
    const w = await svc.create(org, { externalRef: ref, label: 'Customer 42', blockchains: ['solana', 'ethereum'] });
    expect(w).toMatchObject({ external_ref: ref, label: 'Customer 42', status: 'active' });
    expect(w.keys.map((k) => k.blockchain)).toEqual(['ethereum', 'solana']);
    expect(created.map((c) => c.name).sort()).toEqual([`wallet:${ref}:ethereum`, `wallet:${ref}:solana`]);

    created.length = 0;
    const again = await svc.create(org, { externalRef: ref, blockchains: ['solana'] });
    expect(again.wallet_id).toBe(w.wallet_id);
    expect(created).toHaveLength(0); // no second key

    const more = await svc.create(org, { externalRef: ref, blockchains: ['solana', 'bitcoin'] });
    expect(more.keys.map((k) => k.blockchain)).toEqual(['bitcoin', 'ethereum', 'solana']);
    expect(created.map((c) => c.blockchain)).toEqual(['bitcoin']);
    expect((await svc.byRef(org.customer_id, ref)).wallet_id).toBe(w.wallet_id);
  });

  it('refuses references that could carry personal information or break paths, and unknown chains', async () => {
    if (skipped()) return;
    for (const externalRef of ['jane@example.com', 'Jane Smith', '', 'x'.repeat(129), '../etc']) {
      await expect(svc.create(org, { externalRef, blockchains: ['solana'] })).rejects.toThrow(BadRequestException);
    }
    await expect(svc.create(org, { externalRef: 'ok-1', blockchains: ['dogecoin'] })).rejects.toThrow(/unsupported/);
    await expect(svc.create(org, { externalRef: 'ok-1', blockchains: [] })).rejects.toThrow(BadRequestException);
  });

  it('keeps each organisation to its own wallets', async () => {
    if (skipped()) return;
    const w = await svc.create(org, { externalRef: `iso-${randomUUID()}`, blockchains: ['solana'] });
    await expect(svc.get(other.customer_id, w.wallet_id)).rejects.toThrow(NotFoundException);
    expect((await svc.list(other.customer_id)).wallets.find((x) => x.wallet_id === w.wallet_id)).toBeUndefined();
    // The same reference in another organisation is a different wallet.
    const mine = await svc.create(other, { externalRef: w.external_ref, blockchains: ['solana'] });
    expect(mine.wallet_id).not.toBe(w.wallet_id);
  });

  it('lets the database refuse re-pointing a wallet, deleting it, or linking another organisation\'s key', async () => {
    if (skipped()) return;
    const w = await svc.create(org, { externalRef: `db-${randomUUID()}`, blockchains: ['solana'] });
    await expect(admin.query(`UPDATE user_wallets SET external_ref = 'someone-else' WHERE wallet_id = $1`, [w.wallet_id])).rejects.toMatchObject({ code: 'OFB10' });
    await expect(admin.query(`UPDATE user_wallets SET customer_id = $2 WHERE wallet_id = $1`, [w.wallet_id, other.customer_id])).rejects.toMatchObject({ code: 'OFB10' });
    await expect(admin.query(`DELETE FROM user_wallets WHERE wallet_id = $1`, [w.wallet_id])).rejects.toMatchObject({ code: 'OFB10' });
    await expect(admin.query(`DELETE FROM user_wallet_keys WHERE wallet_id = $1`, [w.wallet_id])).rejects.toMatchObject({ code: 'OFB10' });

    const theirs = randomUUID();
    await postgres.createKey({ key_id: theirs, customer_id: other.customer_id, name: `k-${theirs}`, blockchain: 'bitcoin', threshold: 2, total_parties: 3, status: 'pending_dkg' });
    await expect(admin.query(`INSERT INTO user_wallet_keys (wallet_id, customer_id, blockchain, key_id) VALUES ($1, $2, 'bitcoin', $3)`,
      [w.wallet_id, org.customer_id, theirs])).rejects.toMatchObject({ code: 'OFB10' });
  });

  it('a frozen wallet sends nothing: refused before the transfer path, and its keys will not sign', async () => {
    if (skipped()) return;
    const w = await svc.create(org, { externalRef: `frz-${randomUUID()}`, blockchains: ['solana'] });
    const keyId = w.keys[0].key_id;
    await expect(svc.setStatus(org.customer_id, w.wallet_id, 'frozen', 'x')).rejects.toThrow(/reason/);
    const frozen = await svc.setStatus(org.customer_id, w.wallet_id, 'frozen', 'suspected account takeover');
    expect(frozen).toMatchObject({ status: 'frozen', status_reason: 'suspected account takeover' });

    await expect(svc.transfer(org, w.wallet_id, { blockchain: 'solana', destination: '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM', amount: '1' }, { userId: null, label: 'api-key' }))
      .rejects.toThrow(ConflictException);
    expect(submitted).toHaveLength(0);

    // What every signing path loads: the key row carries its wallet's status.
    const row = await postgres.getKey(keyId, org.customer_id);
    expect(row).toMatchObject({ wallet_id: w.wallet_id, wallet_status: 'frozen' });
    expect(() => assertWalletNotFrozen(row, keyId)).toThrow(ForbiddenException);
    await expect(svc.create(org, { externalRef: w.external_ref, blockchains: ['bitcoin'] })).rejects.toThrow(/frozen/);

    await svc.setStatus(org.customer_id, w.wallet_id, 'active', 'cleared after review');
    const cleared = await postgres.getKey(keyId, org.customer_id);
    expect(() => assertWalletNotFrozen(cleared, keyId)).not.toThrow();
    await svc.transfer(org, w.wallet_id, { blockchain: 'solana', destination: '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM', amount: '1' }, { userId: null, label: 'api-key' });
    expect(submitted[0].slice(1, 3)).toEqual([keyId, 'solana']);
  });

  it('a key outside any wallet is unaffected', async () => {
    if (skipped()) return;
    const k = randomUUID();
    await postgres.createKey({ key_id: k, customer_id: org.customer_id, name: `plain-${k}`, blockchain: 'solana', threshold: 2, total_parties: 3, status: 'pending_dkg' });
    const row = await postgres.getKey(k, org.customer_id);
    expect(row).toMatchObject({ key_id: k, wallet_id: null, wallet_status: null });
  });

  it('pages the list newest first', async () => {
    if (skipped()) return;
    const fresh: any = { customer_id: (await admin.query(`INSERT INTO customers (name, api_key_hash) VALUES ('wallets-page', decode(md5(random()::text), 'hex')) RETURNING customer_id`)).rows[0].customer_id };
    for (let i = 0; i < 3; i++) await svc.create(fresh, { externalRef: `p-${i}`, blockchains: ['solana'] });
    const first = await svc.list(fresh.customer_id, { limit: 2 });
    expect(first.wallets.map((w) => w.external_ref)).toEqual(['p-2', 'p-1']);
    const second = await svc.list(fresh.customer_id, { limit: 2, before: first.next! });
    expect(second.wallets.map((w) => w.external_ref)).toEqual(['p-0']);
    expect(second.next).toBeNull();
  });
});
