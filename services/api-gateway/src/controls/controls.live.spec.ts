import { INestApplication, ValidationPipe } from '@nestjs/common';
import { Test } from '@nestjs/testing';
import { JwtModule, JwtService } from '@nestjs/jwt';
import { PassportModule } from '@nestjs/passport';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { createServer, Server } from 'http';
import { AddressInfo } from 'net';
import { ApprovalsService } from '../approvals/approvals.service';
import { TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CustomerService } from '../customers/customer.service';
import { UsersService } from '../identity/users.service';
import { JwtAuthStrategy, jwtSecret } from '../identity/jwt.strategy';
import { AuditService } from '../database/audit.service';
import { PG_POOL } from '../database/pg-pool.token';
import { ControlsController } from './controls.controller';
import { ControlsService, FrozenException } from './controls.service';
import { AlertsService } from './alerts.service';
import { WebhookEmitter } from '../webhooks/webhooks.service';

// Freeze and whitelist over real Postgres and real HTTP with real JWTs.
//
//   eval "$(infrastructure/local/postgres-local.sh start)"   # migrations through 029
//   npx jest src/controls/controls.live.spec.ts

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const EVM = '0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045';
const SOL = '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM';

let reachable = false;
beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try { reachable = (await p.query(`SELECT to_regclass('address_whitelist') IS NOT NULL AS ok`)).rows[0].ok; } catch { reachable = false; } finally { await p.end().catch(() => undefined); }
});
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 029 is reachable');
  console.warn('skipping controls live test -- no database with migration 029');
  return true;
}

describe('safety controls (live Postgres)', () => {
  let app: INestApplication; let base: string; let admin: Pool; let tenantPool: Pool; let jwt: JwtService;
  let ownPools: Pool[] = []; let svc: ControlsService; let customerId = ''; let otherCustomerId = '';
  let hook: Server; const alertsSeen: string[] = []; const eventsSeen: string[] = [];
  const people: Record<string, { id: string; email: string; token: string }> = {};

  async function person(name: string) {
    const email = `${name}-${randomUUID().slice(0, 8)}@example.test`;
    const r = await admin.query(`INSERT INTO users (email, password_hash, full_name) VALUES ($1, 'x', $2) RETURNING id`, [email, name]);
    people[name] = { id: r.rows[0].id, email, token: await jwt.signAsync({ sub: r.rows[0].id, email, role: 'user' }) };
  }
  async function call(method: string, path: string, who: string, body?: unknown) {
    const res = await fetch(`${base}/organisations/${customerId}/controls${path}`, { method, headers: { 'content-type': 'application/json', authorization: `Bearer ${people[who].token}` }, body: body ? JSON.stringify(body) : undefined });
    const text = await res.text();
    return { status: res.status, body: text ? JSON.parse(text) : null };
  }

  beforeAll(async () => {
    if (!reachable) return;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN;
    hook = createServer((req, res) => { let b = ''; req.on('data', (d) => (b += d)); req.on('end', () => { alertsSeen.push(JSON.parse(b).text); res.end('ok'); }); });
    await new Promise<void>((r) => hook.listen(0, '127.0.0.1', r));
    process.env.ALERT_WEBHOOK_URL = `http://127.0.0.1:${(hook.address() as AddressInfo).port}/hook`;

    admin = new Pool({ connectionString: ADMIN_DSN }); tenantPool = new Pool({ connectionString: TENANT_DSN });
    const m = await Test.createTestingModule({
      imports: [PassportModule, JwtModule.register({ secret: jwtSecret(), signOptions: { issuer: 'openfireblocks' } })],
      controllers: [ControlsController],
      providers: [ControlsService, AlertsService, ApprovalsService, TenantRoleGuard, CustomerService, UsersService, JwtAuthStrategy, AuditService,
        { provide: PG_POOL, useValue: tenantPool },
        { provide: WebhookEmitter, useValue: { emit: async (_c: string, t: string) => { eventsSeen.push(t); } } },
        { provide: 'TemporalService', useValue: {} }],
    }).useMocker((tok) => (typeof tok === 'function' ? { signalDecision: jest.fn(), start: jest.fn() } : undefined)).compile();
    app = m.createNestApplication();
    app.useGlobalPipes(new ValidationPipe({ whitelist: true, forbidNonWhitelisted: true, transform: true }));
    await app.listen(0, '127.0.0.1');
    base = (await app.getUrl()).replace('[::1]', '127.0.0.1');
    jwt = m.get(JwtService); svc = m.get(ControlsService);
    ownPools = [(m.get(CustomerService) as unknown as { pool: Pool }).pool, (m.get(AuditService) as unknown as { adminPool: Pool }).adminPool];
    customerId = (await m.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    otherCustomerId = (await m.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    for (const n of ['ada', 'alice', 'oscar', 'vera']) await person(n);
    const a = m.get(ApprovalsService);
    await a.setMember(customerId, people.ada.email, 'admin');
    await a.setMember(customerId, people.alice.email, 'approver');
    await a.setMember(customerId, people.oscar.email, 'operator');
    await a.setMember(customerId, people.vera.email, 'viewer');
  });
  afterAll(async () => {
    if (!reachable) return;
    await app.close(); await Promise.all(ownPools.map((p) => p.end().catch(() => undefined)));
    await new Promise((r) => hook.close(r)); await admin.end(); await tenantPool.end();
    delete process.env.ALERT_WEBHOOK_URL;
  });

  it('starts unfrozen with no whitelist, so nothing changes for an organisation that has not asked', async () => {
    if (skipped()) return;
    const r = await call('GET', '', 'vera');
    expect(r.body).toMatchObject({ frozen: false, whitelistEnforced: false, whitelist: [] });
    await expect(svc.assertCanSign(customerId)).resolves.toBeUndefined();
    await expect(svc.assertDestinationAllowed(customerId, 'solana', SOL)).resolves.toBeUndefined();
  });

  it('lets an approver freeze (a stop is safe), refuses an operator, and stops every signing path', async () => {
    if (skipped()) return;
    expect((await call('POST', '/freeze', 'oscar', { reason: 'x' })).status).toBe(403);
    expect((await call('POST', '/freeze', 'alice', { reason: ' ' })).status).toBe(400);
    const r = await call('POST', '/freeze', 'alice', { reason: 'suspected compromise of a signer host' });
    expect(r.body).toMatchObject({ frozen: true, frozenReason: 'suspected compromise of a signer host' });
    await expect(svc.assertCanSign(customerId)).rejects.toBeInstanceOf(FrozenException);
    // Another organisation is untouched.
    await expect(svc.assertCanSign(otherCustomerId)).resolves.toBeUndefined();
    // And somebody was told.
    await new Promise((r2) => setTimeout(r2, 200));
    expect(alertsSeen.some((t) => /FROZEN/.test(t) && /suspected compromise/.test(t))).toBe(true);
    expect(eventsSeen).toContain('org.frozen');
  });

  it('is lifted only by an admin', async () => {
    if (skipped()) return;
    expect((await call('POST', '/unfreeze', 'alice')).status).toBe(403);
    expect((await call('POST', '/unfreeze', 'oscar')).status).toBe(403);
    expect((await call('POST', '/unfreeze', 'ada')).body).toMatchObject({ frozen: false, frozenReason: null });
    await expect(svc.assertCanSign(customerId)).resolves.toBeUndefined();
    expect(eventsSeen).toContain('org.unfrozen');
  });

  it('refuses to sign when it cannot tell whether the organisation is frozen', async () => {
    if (skipped()) return;
    const broken = new ControlsService({ connect: async () => { throw new Error('db down'); } } as never, { logEvent: async () => null } as never, new AlertsService());
    await expect(broken.assertCanSign(customerId)).rejects.toThrow(/could not be checked/);
    await expect(broken.assertDestinationAllowed(customerId, 'solana', SOL)).rejects.toThrow(/could not be checked/);
  });

  describe('whitelist', () => {
    it('only an admin changes it', async () => {
      if (skipped()) return;
      for (const who of ['alice', 'oscar', 'vera']) {
        expect((await call('POST', '/whitelist', who, { blockchain: 'solana', address: SOL })).status).toBe(403);
        expect((await call('PUT', '/whitelist-mode', who, { enforced: true, cooldownMinutes: 0 })).status).toBe(403);
      }
    });
    it('refuses malformed and unknown entries', async () => {
      if (skipped()) return;
      expect((await call('POST', '/whitelist', 'ada', { blockchain: 'solana', address: 'not-an-address' })).status).toBe(400);
      expect((await call('POST', '/whitelist', 'ada', { blockchain: 'dogecoin', address: SOL })).status).toBe(400);
      expect((await call('POST', '/whitelist', 'ada', { blockchain: 'ethereum', address: '0x123' })).status).toBe(400);
    });
    it('enforces the list: listed goes, unlisted is refused, EVM case does not matter', async () => {
      if (skipped()) return;
      expect((await call('POST', '/whitelist', 'ada', { blockchain: 'ethereum', address: EVM, label: 'Supplier' })).status).toBe(201);
      expect((await call('POST', '/whitelist', 'ada', { blockchain: 'ethereum', address: EVM })).status).toBe(409); // already there
      await call('PUT', '/whitelist-mode', 'ada', { enforced: true, cooldownMinutes: 0 });
      await expect(svc.assertDestinationAllowed(customerId, 'ethereum', EVM.toLowerCase())).resolves.toBeUndefined();
      await expect(svc.assertDestinationAllowed(customerId, 'polygon', EVM.toUpperCase().replace('0X', '0x'))).resolves.toBeUndefined();
      await expect(svc.assertDestinationAllowed(customerId, 'solana', SOL)).rejects.toThrow(/not on the organisation's whitelist/);
      await expect(svc.assertDestinationAllowed(customerId, 'ethereum', '0x' + '1'.repeat(40))).rejects.toThrow(/not on the organisation's whitelist/);
    });
    it('holds a new address back for the cooling-off period', async () => {
      if (skipped()) return;
      await call('PUT', '/whitelist-mode', 'ada', { enforced: true, cooldownMinutes: 60 });
      await call('POST', '/whitelist', 'ada', { blockchain: 'solana', address: SOL });
      await expect(svc.assertDestinationAllowed(customerId, 'solana', SOL)).rejects.toThrow(/cooling-off/);
      const list = (await call('GET', '', 'vera')).body.whitelist;
      expect(list.find((e: any) => e.address === SOL)).toMatchObject({ active: false });
    });
    it('stops being usable once removed, and the entry stays in the history', async () => {
      if (skipped()) return;
      const entry = (await call('GET', '', 'vera')).body.whitelist.find((e: any) => e.address === EVM.toLowerCase());
      expect((await call('DELETE', `/whitelist/${entry.entryId}`, 'ada')).status).toBe(204);
      await expect(svc.assertDestinationAllowed(customerId, 'ethereum', EVM)).rejects.toThrow();
      const row = (await admin.query(`SELECT removed_at FROM address_whitelist WHERE entry_id = $1`, [entry.entryId])).rows[0];
      expect(row.removed_at).not.toBeNull();
      expect((await call('DELETE', `/whitelist/${entry.entryId}`, 'ada')).status).toBe(404);
    });
    it('is private to the organisation', async () => {
      if (skipped()) return;
      const seen = await tenantPool.connect();
      try {
        await seen.query('BEGIN'); await seen.query("SELECT set_config('app.current_customer_id', $1, true)", [otherCustomerId]);
        expect((await seen.query('SELECT 1 FROM address_whitelist')).rows).toHaveLength(0);
        await seen.query('ROLLBACK');
      } finally { seen.release(); }
    });
    it('the database refuses to rewrite or delete an entry', async () => {
      if (skipped()) return;
      const id = (await admin.query(`SELECT entry_id FROM address_whitelist WHERE customer_id = $1 AND removed_at IS NULL LIMIT 1`, [customerId])).rows[0].entry_id;
      await expect(admin.query(`UPDATE address_whitelist SET address = '0x${'2'.repeat(40)}' WHERE entry_id = $1`, [id])).rejects.toThrow(/cannot be changed/);
      await expect(admin.query(`UPDATE address_whitelist SET active_from = now() - interval '1 day' WHERE entry_id = $1`, [id])).rejects.toThrow(/cannot be changed/);
      await expect(admin.query(`DELETE FROM address_whitelist WHERE entry_id = $1`, [id])).rejects.toThrow(/never deleted/);
    });
  });
});
