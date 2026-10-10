import { INestApplication, ValidationPipe } from '@nestjs/common';
import { Test } from '@nestjs/testing';
import { JwtModule, JwtService } from '@nestjs/jwt';
import { PassportModule } from '@nestjs/passport';
import { authenticator } from 'otplib';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { createServer, IncomingMessage, Server } from 'http';
import { AddressInfo } from 'net';
import { ApprovalsController } from '../approvals/approvals.controller';
import { ApprovalsService } from '../approvals/approvals.service';
import { NativeApprovalHooks } from '../approvals/native-approval-hooks';
import { TenantRoleGuard } from '../approvals/tenant-role.guard';
import { TemporalService } from '../settlements/temporal.service';
import { CustomerService } from '../customers/customer.service';
import { UsersService } from '../identity/users.service';
import { JwtAuthStrategy, jwtSecret } from '../identity/jwt.strategy';
import { AuditService } from '../database/audit.service';
import { PG_POOL } from '../database/pg-pool.token';
import { KeysService } from '../keys/keys.service';
import { EvmRpcService } from '../tokens/evm-rpc.service';
import { TransfersService } from '../transfers/transfers.service';
import { CustodyExecutors } from '../transfers/custody-executor';
import { ControlsService } from '../controls/controls.service';
import { AlertsService } from '../controls/alerts.service';
import { WebhookEmitter } from '../webhooks/webhooks.service';
import { ControlsController } from '../controls/controls.controller';
import { CustodyController } from './custody.controller';
import { CustodyService } from './custody.service';

// Multi-custodian orchestration end to end: a real HTTP connector stand-in speaking the
// documented contract, real Postgres, real approvals with real JWTs and one-time codes,
// the real freeze and whitelist. Only the platform's own keys are faked (a fixed list).
//
//   eval "$(infrastructure/local/postgres-local.sh start)"   # migrations through 033
//   npx jest src/custody

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ETH_DEST = '0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045';
const TOKEN = 'connector-secret';

let reachable = false;
beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try { reachable = (await p.query(`SELECT to_regclass('custodians') IS NOT NULL AS ok`)).rows[0].ok; } catch { reachable = false; } finally { await p.end().catch(() => undefined); }
});
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 033 is reachable');
  console.warn('skipping custody live test -- no database with migration 033');
  return true;
}

// The custodian: the connector contract in docs/deployment/MULTI-CUSTODIAN.md.
function connector() {
  const posts: Array<{ body: any; headers: IncomingMessage['headers'] }> = [];
  const state = { failTransfers: false, transferStatus: 'pending' as string, usdcDecimals: 6 };
  const accounts = [
    { id: 'cold-1', name: 'Cold vault', blockchain: 'ethereum', address: ETH_DEST },
    { id: 'hot-2', name: 'Hot payouts', blockchain: 'solana' },
  ];
  const server: Server = createServer((req, res) => {
    let b = ''; req.on('data', (d) => (b += d));
    req.on('end', () => {
      const send = (code: number, body: unknown) => { res.writeHead(code, { 'content-type': 'application/json' }); res.end(JSON.stringify(body)); };
      if (req.headers.authorization !== `Bearer ${TOKEN}`) return send(401, { error: 'bad token' });
      const url = req.url ?? '';
      if (req.method === 'GET' && url === '/accounts') return send(200, { accounts });
      const bal = url.match(/^\/accounts\/([^/]+)\/balances$/);
      if (req.method === 'GET' && bal) {
        return send(200, { balances: bal[1] === 'cold-1'
          ? [{ asset: 'ETH', amount: '5000000000000000000', decimals: 18 }, { asset: 'USDC', amount: '1000000', decimals: state.usdcDecimals }]
          : [{ asset: 'SOL', amount: '2000000000', decimals: 9 }] });
      }
      if (req.method === 'POST' && url === '/transfers') {
        posts.push({ body: JSON.parse(b), headers: req.headers });
        if (state.failTransfers) return send(500, { error: 'custodian is down' });
        return send(200, { id: `ext-${posts.length}`, status: state.transferStatus });
      }
      const t = url.match(/^\/transfers\/([^/]+)$/);
      if (req.method === 'GET' && t) return send(200, { id: t[1], status: 'completed', txHash: '0xabc' });
      send(404, {});
    });
  });
  return { server, posts, state };
}

describe('multi-custodian orchestration (live Postgres, real HTTP connector)', () => {
  let app: INestApplication; let base: string; let admin: Pool; let tenantPool: Pool; let jwt: JwtService;
  let ownPools: Pool[] = []; let customerId = ''; let otherId = '';
  const conn = connector(); let connUrl = '';
  const people: Record<string, { id: string; email: string; secret: string; token: string }> = {};
  const nativeKeys = [{ key_id: randomUUID(), name: 'Treasury SOL', blockchain: 'solana', address: 'SolAddr', status: 'active' }];
  let custodianId = '';

  async function person(name: string) {
    const email = `${name}-${randomUUID().slice(0, 8)}@example.test`; const secret = authenticator.generateSecret();
    const r = await admin.query(`INSERT INTO users (email, password_hash, full_name, mfa_secret, mfa_enabled) VALUES ($1,'x',$2,$3,true) RETURNING id`, [email, name, secret]);
    people[name] = { id: r.rows[0].id, email, secret, token: await jwt.signAsync({ sub: r.rows[0].id, email, role: 'user' }) };
  }
  async function call(method: string, path: string, who: string, body?: unknown, org = customerId) {
    const res = await fetch(`${base}/organisations/${org}${path}`, { method, headers: { 'content-type': 'application/json', authorization: `Bearer ${people[who].token}` }, body: body ? JSON.stringify(body) : undefined });
    const text = await res.text();
    return { status: res.status, body: text ? JSON.parse(text) : null };
  }
  const decide = (who: string, approvalId: string, decision = 'approve') =>
    call('POST', `/approvals/${approvalId}/decisions`, who, { decision, totpCode: authenticator.generate(people[who].secret) });
  const send = (who: string, over: Record<string, unknown> = {}) =>
    call('POST', '/custody/transfers', who, { custodianId, accountId: 'cold-1', destination: ETH_DEST, asset: 'ETH', amount: '1000000000000000000', ...over });

  beforeAll(async () => {
    if (!reachable) return;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN; process.env.CUSTODY_TOKEN_TESTBANK = TOKEN; process.env.CUSTODY_TOKEN_DEADBANK = TOKEN;
    await new Promise<void>((r) => conn.server.listen(0, '127.0.0.1', r));
    connUrl = `http://127.0.0.1:${(conn.server.address() as AddressInfo).port}`;
    admin = new Pool({ connectionString: ADMIN_DSN }); tenantPool = new Pool({ connectionString: TENANT_DSN });
    const fakeKeys = {
      listKeys: async () => nativeKeys,
      getBalancesForKey: async () => ({ balances: [{ asset: 'SOL', amount: '1000000000', decimals: 9 }, { asset: 'USDC', amount: '5', decimals: 18 }] }),
      getKey: async () => null, assertTravelRuleComplete() {},
    };
    const m = await Test.createTestingModule({
      imports: [PassportModule, JwtModule.register({ secret: jwtSecret(), signOptions: { issuer: 'openfireblocks' } })],
      controllers: [ApprovalsController, CustodyController, ControlsController],
      providers: [ApprovalsService, TenantRoleGuard, CustomerService, UsersService, JwtAuthStrategy, AuditService, NativeApprovalHooks, TransfersService, CustodyExecutors, CustodyService,
        ControlsService, AlertsService,
        { provide: PG_POOL, useValue: tenantPool },
        { provide: WebhookEmitter, useValue: { emit: async () => undefined } },
        { provide: TemporalService, useValue: { signalDecision: jest.fn(), start: jest.fn() } },
        { provide: KeysService, useValue: fakeKeys },
        { provide: EvmRpcService, useValue: { configured: () => false } }],
    }).compile();
    app = m.createNestApplication();
    app.useGlobalPipes(new ValidationPipe({ whitelist: true, forbidNonWhitelisted: true, transform: true }));
    await app.init(); await app.listen(0, '127.0.0.1');
    base = (await app.getUrl()).replace('[::1]', '127.0.0.1');
    jwt = m.get(JwtService);
    ownPools = [(m.get(CustomerService) as unknown as { pool: Pool }).pool, (m.get(AuditService) as unknown as { adminPool: Pool }).adminPool];
    customerId = (await m.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    otherId = (await m.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    for (const n of ['ada', 'alice', 'bob', 'oscar']) await person(n);
    const a = m.get(ApprovalsService);
    await a.setMember(customerId, people.ada.email, 'admin'); await a.setMember(customerId, people.alice.email, 'approver');
    await a.setMember(customerId, people.bob.email, 'approver'); await a.setMember(customerId, people.oscar.email, 'operator');
    await a.setMember(otherId, people.ada.email, 'admin');
    await call('PUT', '/approval-policy', 'ada', { requiredApprovals: 2, windowMinutes: 60 });
  }, 60000);
  afterAll(async () => {
    if (!reachable) return;
    await app.close(); await Promise.all(ownPools.map((p) => p.end().catch(() => undefined)));
    await new Promise((r) => conn.server.close(r)); await admin.end(); await tenantPool.end();
  });

  it('registers a custodian only for an admin, only with a safe URL and a provisioned token, and fixes its connection', async () => {
    if (skipped()) return;
    const good = { name: 'Test Bank', baseUrl: connUrl, tokenEnv: 'CUSTODY_TOKEN_TESTBANK' };
    expect((await call('POST', '/custody/custodians', 'oscar', good)).status).toBe(403);
    expect((await call('POST', '/custody/custodians', 'ada', { ...good, tokenEnv: 'DATABASE_URL' })).status).toBe(400); // cannot name an arbitrary secret
    expect((await call('POST', '/custody/custodians', 'ada', { ...good, tokenEnv: 'CUSTODY_TOKEN_UNSET' })).status).toBe(422);
    expect((await call('POST', '/custody/custodians', 'ada', { ...good, baseUrl: 'http://bank.example.com' })).status).toBe(400);
    expect((await call('POST', '/custody/custodians', 'ada', { ...good, baseUrl: 'https://user:pw@bank.example.com' })).status).toBe(400);
    const r = await call('POST', '/custody/custodians', 'ada', good);
    expect(r.status).toBe(201);
    custodianId = r.body.custodianId;
    expect((await call('POST', '/custody/custodians', 'ada', good)).status).toBe(409);
    await expect(admin.query(`UPDATE custodians SET base_url = 'https://evil.example' WHERE custodian_id = $1`, [custodianId])).rejects.toMatchObject({ code: 'OFB08' });
    await expect(admin.query(`DELETE FROM custodians WHERE custodian_id = $1`, [custodianId])).rejects.toMatchObject({ code: 'OFB08' });
    expect((await call('GET', '/custody/custodians', 'ada', undefined, otherId)).body).toEqual([]);
  });

  it('shows one balance sheet across this platform and the custodian, with totals by asset', async () => {
    if (skipped()) return;
    const o = (await call('GET', '/custody/overview', 'oscar')).body;
    expect(o.sources.map((s: any) => s.source)).toEqual(['native', 'custodian']);
    const cold = o.sources[1].accounts.find((a: any) => a.id === 'cold-1');
    expect(cold.balances).toContainEqual({ asset: 'ETH', amount: '5000000000000000000', decimals: 18 });
    // SOL: 1 held natively + 2 at the custodian.
    expect(o.totals.find((t: any) => t.asset === 'SOL')).toEqual({ asset: 'SOL', decimals: 9, amount: '3000000000' });
    expect(o.totals.find((t: any) => t.asset === 'ETH').amount).toBe('5000000000000000000');
  });

  it('shows what it can when one custodian is unreachable, instead of failing the whole picture', async () => {
    if (skipped()) return;
    const dead = await call('POST', '/custody/custodians', 'ada', { name: 'Dead Bank', baseUrl: 'http://127.0.0.1:9', tokenEnv: 'CUSTODY_TOKEN_DEADBANK' });
    const o = (await call('GET', '/custody/overview', 'oscar')).body;
    const d = o.sources.find((s: any) => s.name === 'Dead Bank');
    expect(d.error).toMatch(/could not reach/);
    expect(o.sources.find((s: any) => s.name === 'Test Bank').accounts.length).toBe(2);
    await call('PUT', `/custody/custodians/${dead.body?.custodianId ?? d.custodianId}/enabled`, 'ada', { enabled: false });
    expect((await call('GET', '/custody/overview', 'oscar')).body.sources.find((s: any) => s.name === 'Dead Bank')).toBeUndefined();
  });

  it('does not add two sources\' balances of one asset when they disagree about its decimals', async () => {
    if (skipped()) return;
    // Natively USDC has 18 decimals here, at the custodian 6: adding them would be a silently wrong total.
    const o = (await call('GET', '/custody/overview', 'oscar')).body;
    expect(o.totals.find((t: any) => t.asset === 'USDC')).toMatchObject({ amount: null, error: expect.stringMatching(/decimals/) });
    // Agreeing sources are added.
    expect(o.totals.find((t: any) => t.asset === 'SOL').amount).toBe('3000000000');
  });

  it('holds every transfer from the custodian for approval, shows approvers exactly what was asked, and sends it once on quorum', async () => {
    if (skipped()) return;
    conn.posts.length = 0;
    const r = await send('oscar', { memo: 'invoice 42' });
    expect(r.status).toBe(202);
    expect(r.body).toMatchObject({ status: 'pending_approval', requiredApprovals: 2 });
    expect(conn.posts).toHaveLength(0);
    const id = r.body.approvalId;
    const a = (await call('GET', `/approvals/${id}`, 'alice')).body;
    expect(a.summary).toMatchObject({ asset: 'ETH', amount: '1000000000000000000', to: ETH_DEST, blockchain: 'ethereum', keyName: 'Test Bank / Cold vault' });

    expect((await decide('alice', id)).body.execution).toMatchObject({ status: 'awaiting_approval' });
    expect(conn.posts).toHaveLength(0);
    const done = await decide('bob', id);
    expect(done.body.execution).toMatchObject({ status: 'completed', result: { externalTransferId: 'ext-1', externalStatus: 'pending', custodian: 'Test Bank' } });
    expect(conn.posts).toHaveLength(1);
    expect(conn.posts[0].body).toEqual({ accountId: 'cold-1', destination: ETH_DEST, asset: 'ETH', amount: '1000000000000000000', memo: 'invoice 42' });
    expect(conn.posts[0].headers['idempotency-key']).toBe(`approval:${id}`);
    expect(conn.posts[0].headers.authorization).toBe(`Bearer ${TOKEN}`);

    // A repeated decision does not send twice.
    expect((await decide('bob', id)).status).toBe(409);
    expect(conn.posts).toHaveLength(1);

    // And the live state is read back from the custodian.
    const st = (await call('GET', `/custody/transfers/${id}`, 'oscar')).body;
    expect(st).toMatchObject({ status: 'completed', live: { id: 'ext-1', status: 'completed', txHash: '0xabc' } });
  });

  it('refuses a transfer from an account the custodian does not have, and an operator cannot approve their own', async () => {
    if (skipped()) return;
    expect((await send('oscar', { accountId: 'nope' })).status).toBe(400);
    expect((await send('oscar', { amount: '0' })).status).toBe(400);
    expect((await send('oscar', { amount: '1.5' })).status).toBe(400);
    const { approvalId } = (await send('ada')).body;
    expect([403, 409]).toContain((await decide('ada', approvalId)).status);
  });

  it('a freeze after approval still stops it: the last decision is refused and nothing is sent until it is lifted', async () => {
    if (skipped()) return;
    conn.posts.length = 0;
    const { approvalId } = (await send('oscar')).body;
    await decide('alice', approvalId);
    expect((await call('POST', '/controls/freeze', 'alice', { reason: 'suspected compromise' })).status).toBe(200);
    try {
      const refused = await decide('bob', approvalId);
      expect(refused.status).toBe(403);
      expect(JSON.stringify(refused.body)).toMatch(/frozen/);
      expect(conn.posts).toHaveLength(0);
      // While frozen, nothing new can even be asked.
      expect((await send('oscar')).status).toBe(403);
    } finally {
      expect((await call('POST', '/controls/unfreeze', 'ada')).status).toBe(200);
    }
    // Lifted: the last approver decides, and it goes out once.
    const done = await decide('bob', approvalId);
    expect(done.body.execution).toMatchObject({ status: 'completed' });
    expect(conn.posts).toHaveLength(1);
  });

  it('the executor itself re-checks the freeze, for a transfer approved earlier whose run is retried while frozen', async () => {
    if (skipped()) return;
    conn.posts.length = 0;
    const { approvalId } = (await send('oscar')).body;
    conn.state.failTransfers = true;
    await decide('alice', approvalId);
    expect((await decide('bob', approvalId)).body.execution).toMatchObject({ status: 'failed' });
    conn.state.failTransfers = false;
    await call('POST', '/controls/freeze', 'alice', { reason: 'drill' });
    try {
      const retried = await call('POST', `/approvals/${approvalId}/execute`, 'ada');
      expect(JSON.stringify(retried.body)).toMatch(/frozen/);
      expect(conn.posts).toHaveLength(1); // only the earlier failed attempt
    } finally { await call('POST', '/controls/unfreeze', 'ada'); }
  });

  it('applies the address whitelist to the chain the account is really on', async () => {
    if (skipped()) return;
    expect((await call('PUT', '/controls/whitelist-mode', 'ada', { enforced: true, cooldownMinutes: 0 })).status).toBe(200);
    const refused = await send('oscar');
    expect(refused.status).toBe(403);
    expect(JSON.stringify(refused.body)).toMatch(/whitelist/);
    expect((await call('POST', '/controls/whitelist', 'ada', { blockchain: 'ethereum', address: ETH_DEST, label: 'Treasury' })).status).toBeLessThan(300);
    expect((await send('oscar')).status).toBe(202);
    // Asking for the Solana account with an Ethereum address on the list is refused: the chain is the account's.
    expect((await send('oscar', { accountId: 'hot-2', asset: 'SOL', amount: '1000' })).status).toBe(403);
    await call('PUT', '/controls/whitelist-mode', 'ada', { enforced: false, cooldownMinutes: 0 });
  });

  it('routes a transfer to the account a rule names, and refuses what no rule covers', async () => {
    if (skipped()) return;
    conn.posts.length = 0;
    expect((await call('POST', '/custody/routes', 'oscar', { asset: 'ETH', custodianId, accountId: 'cold-1' })).status).toBe(403);
    expect((await call('POST', '/custody/routes', 'ada', { asset: 'ETH', custodianId, accountId: 'ghost' })).status).toBe(400);
    expect((await call('POST', '/custody/routes', 'ada', { asset: 'ETH', maxAmount: '2000000000000000000', custodianId, accountId: 'cold-1', priority: 10 })).status).toBe(201);
    const ok = await call('POST', '/custody/transfers', 'oscar', { route: true, destination: ETH_DEST, asset: 'ETH', amount: '1500000000000000000' });
    expect(ok.status).toBe(202);
    expect((await call('GET', `/approvals/${ok.body.approvalId}`, 'alice')).body.summary.keyName).toBe('Test Bank / Cold vault');
    const over = await call('POST', '/custody/transfers', 'oscar', { route: true, destination: ETH_DEST, asset: 'ETH', amount: '3000000000000000000' });
    expect(over.status).toBe(422);
    expect(JSON.stringify(over.body)).toMatch(/no routing rule/);
    expect((await call('POST', '/custody/transfers', 'oscar', { destination: ETH_DEST, asset: 'ETH', amount: '1' })).status).toBe(400);
    const routes = (await call('GET', '/custody/routes', 'oscar')).body;
    await call('DELETE', `/custody/routes/${routes[0].routeId}`, 'ada');
    expect((await call('GET', '/custody/routes', 'oscar')).body).toEqual([]);
  });

  it('reports a custodian that fails at execution, keeps the approval, and lets an admin retry with the same idempotency key', async () => {
    if (skipped()) return;
    conn.posts.length = 0;
    const { approvalId } = (await send('oscar')).body;
    conn.state.failTransfers = true;
    await decide('alice', approvalId);
    const out = await decide('bob', approvalId);
    expect(out.body.execution).toMatchObject({ status: 'failed' });
    expect(out.body.execution.error).toMatch(/custodian answered 500/);
    conn.state.failTransfers = false;
    const retried = await call('POST', `/approvals/${approvalId}/execute`, 'ada');
    expect(retried.body).toMatchObject({ status: 'completed' });
    expect(conn.posts.map((p) => p.headers['idempotency-key'])).toEqual([`approval:${approvalId}`, `approval:${approvalId}`]);
  });

  it('refuses transfers from a custodian that has been switched off', async () => {
    if (skipped()) return;
    await call('PUT', `/custody/custodians/${custodianId}/enabled`, 'ada', { enabled: false });
    expect((await send('oscar')).status).toBe(409);
    await call('PUT', `/custody/custodians/${custodianId}/enabled`, 'ada', { enabled: true });
  });
});
