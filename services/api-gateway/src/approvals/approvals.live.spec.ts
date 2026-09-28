import { INestApplication, ValidationPipe } from '@nestjs/common';
import { Test } from '@nestjs/testing';
import { JwtModule, JwtService } from '@nestjs/jwt';
import { PassportModule } from '@nestjs/passport';
import { authenticator } from 'otplib';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { ApprovalsController } from './approvals.controller';
import { OrganisationBootstrapController } from './bootstrap.controller';
import { ApprovalsService } from './approvals.service';
import { TenantRoleGuard } from './tenant-role.guard';
import { SettlementsController } from '../settlements/settlements.controller';
import { TemporalService } from '../settlements/temporal.service';
import { CustomerService } from '../customers/customer.service';
import { ApiKeyGuard } from '../auth/api-key.guard';
import { UsersService } from '../identity/users.service';
import { JwtAuthStrategy, jwtSecret } from '../identity/jwt.strategy';
import { AuditService } from '../database/audit.service';
import { PG_POOL } from '../database/pg-pool.token';

// Segregation of duties, end to end over HTTP: real guards, real JWTs,
// real TOTP codes, and a real Postgres with migration 023's rules. Only
// Temporal is replaced, by a recorder -- the workflow's side has its own
// tests (temporal-worker/workflows).
//
// Skips unless a database with migration 023 is reachable:
//
//   eval "$(infrastructure/local/postgres-local.sh start)"
//   npx jest src/approvals/approvals.live.spec.ts

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN =
  process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';

class RecordingTemporal {
  started: Array<{ customerId: string; initiator?: { userId: string; label: string } }> = [];
  signals: Array<{ workflowId: string; approverUserId: string; decision: string }> = [];
  failNextSignal = false;
  async start(customerId: string, _tier: string, _req: unknown, initiator?: { userId: string; label: string }) {
    this.started.push({ customerId, initiator });
    return { workflowId: `settlement-${customerId}-${randomUUID()}` };
  }
  async signalDecision(_customerId: string, workflowId: string, d: { approverUserId: string; decision: string }) {
    if (this.failNextSignal) {
      this.failNextSignal = false;
      throw new Error('temporal unavailable');
    }
    this.signals.push({ workflowId, ...d });
  }
  async status() {
    return {};
  }
}

let reachable = false;

beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try {
    const r = await p.query(`SELECT to_regclass('approval_requests') IS NOT NULL AS ok`);
    reachable = r.rows[0].ok;
  } catch {
    reachable = false;
  } finally {
    await p.end().catch(() => undefined);
  }
});

// Returns true when the test should not run. With REQUIRE_LIVE_DB set --
// as CI sets it -- an unreachable database fails instead, so the suite
// cannot pass by testing nothing.
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 023 is reachable');
  console.warn('skipping approvals live test -- no database with migration 023');
  return true;
}

describe('approvals with segregation of duties (live Postgres)', () => {
  let app: INestApplication;
  let base: string;
  let admin: Pool;
  let tenantPool: Pool;
  let jwt: JwtService;
  let ownPools: Pool[] = [];
  const temporal = new RecordingTemporal();

  let customerId = '';
  let apiKey = '';
  const people: Record<string, { id: string; email: string; secret?: string; token: string }> = {};

  async function person(name: string, opts: { mfa?: boolean; sso?: boolean } = {}) {
    const email = `${name}-${randomUUID().slice(0, 8)}@example.test`;
    const secret = opts.mfa ? authenticator.generateSecret() : null;
    const r = await admin.query(
      opts.sso
        ? `INSERT INTO users (email, full_name, auth_provider, workos_user_id) VALUES ($1, $2, 'workos_sso', $3) RETURNING id`
        : `INSERT INTO users (email, password_hash, full_name, mfa_secret, mfa_enabled) VALUES ($1, 'x', $2, $3, $4) RETURNING id`,
      opts.sso ? [email, name, `wos_${randomUUID()}`] : [email, name, secret, Boolean(secret)],
    );
    const id = r.rows[0].id;
    const token = await jwt.signAsync({ sub: id, email, role: 'user' });
    people[name] = { id, email, secret: secret ?? undefined, token };
    return people[name];
  }

  async function call(method: string, path: string, who: string | null, body?: unknown, apiKeyAuth = false) {
    const headers: Record<string, string> = { 'content-type': 'application/json' };
    if (apiKeyAuth) headers.authorization = `Bearer ${apiKey}`;
    else if (who) headers.authorization = `Bearer ${people[who].token}`;
    const res = await fetch(`${base}${path}`, { method, headers, body: body ? JSON.stringify(body) : undefined });
    const text = await res.text();
    return { status: res.status, body: text ? JSON.parse(text) : null };
  }

  const org = () => `/organisations/${customerId}`;
  const code = (who: string) => authenticator.generate(people[who].secret!);

  // What the worker's OpenApprovalRequest does, done directly.
  async function openApproval(initiatorUserId: string | null) {
    const workflowId = `settlement-${customerId}-${randomUUID()}`;
    const r = await admin.query(
      `INSERT INTO approval_requests (customer_id, workflow_id, initiated_by_user_id, initiated_by_label,
         required_approvals, summary, expires_at)
       SELECT $1, $2, $3, 'someone', COALESCE(p.required_approvals, 2), '{"to":"0xabc","valueWei":"20000000000000000000"}',
              NOW() + interval '1 hour'
         FROM (SELECT 1) one LEFT JOIN approval_policies p ON p.customer_id = $1
       RETURNING approval_id`,
      [customerId, workflowId, initiatorUserId],
    );
    return { approvalId: r.rows[0].approval_id as string, workflowId };
  }

  beforeAll(async () => {
    if (!reachable) return;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN;
    admin = new Pool({ connectionString: ADMIN_DSN });
    tenantPool = new Pool({ connectionString: TENANT_DSN });

    const moduleRef = await Test.createTestingModule({
      imports: [PassportModule, JwtModule.register({ secret: jwtSecret(), signOptions: { issuer: 'openfireblocks' } })],
      controllers: [ApprovalsController, OrganisationBootstrapController, SettlementsController],
      providers: [
        ApprovalsService,
        TenantRoleGuard,
        CustomerService,
        ApiKeyGuard,
        UsersService,
        JwtAuthStrategy,
        AuditService,
        { provide: PG_POOL, useValue: tenantPool },
        { provide: TemporalService, useValue: temporal },
      ],
    }).compile();

    app = moduleRef.createNestApplication();
    app.useGlobalPipes(new ValidationPipe({ whitelist: true, forbidNonWhitelisted: true, transform: true }));
    await app.listen(0, '127.0.0.1');
    base = await app.getUrl();
    base = base.replace('[::1]', '127.0.0.1');
    jwt = moduleRef.get(JwtService);
    // CustomerService and AuditService open their own admin pools.
    ownPools = [
      (moduleRef.get(CustomerService) as unknown as { pool: Pool }).pool,
      (moduleRef.get(AuditService) as unknown as { adminPool: Pool }).adminPool,
    ];

    const customer = await moduleRef.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' });
    customerId = customer.customer_id;
    apiKey = customer.api_key!;

    await person('ada', { mfa: true }); // first admin
    await person('alice', { mfa: true }); // approver
    await person('bob', { mfa: true }); // approver
    await person('sam', { sso: true }); // approver via SSO
    await person('nomfa'); // approver without two-factor
    await person('oscar', { mfa: true }); // operator
    await person('mallory', { mfa: true }); // no role here
  });

  afterAll(async () => {
    if (!reachable) return;
    await app.close();
    await Promise.all(ownPools.map((p) => p.end().catch(() => undefined)));
    await admin.end();
    await tenantPool.end();
  });

  it('an API key can appoint the first admin once, and only an admin', async () => {
    if (skipped()) return;
    expect((await call('POST', '/organisation/first-admin', null, { email: people.alice.email, role: 'approver' }, true)).status).toBe(403);
    const r = await call('POST', '/organisation/first-admin', null, { email: people.ada.email, role: 'admin' }, true);
    expect(r.status).toBe(201);
    expect(r.body.role).toBe('admin');
    const again = await call('POST', '/organisation/first-admin', null, { email: people.mallory.email, role: 'admin' }, true);
    expect(again.status).toBe(409);
  });

  it('admins appoint approvers and operators; nobody else can', async () => {
    if (skipped()) return;
    for (const [who, role] of [
      ['alice', 'approver'],
      ['bob', 'approver'],
      ['sam', 'approver'],
      ['nomfa', 'approver'],
      ['oscar', 'operator'],
    ]) {
      const r = await call('PUT', `${org()}/members`, 'ada', { email: people[who].email, role });
      expect(r.status).toBe(200);
    }
    expect((await call('PUT', `${org()}/members`, 'alice', { email: people.mallory.email, role: 'approver' })).status).toBe(403);
    expect((await call('PUT', `${org()}/members`, 'mallory', { email: people.mallory.email, role: 'admin' })).status).toBe(404);
    const members = await call('GET', `${org()}/members`, 'oscar');
    expect(members.body.map((m: { role: string }) => m.role).sort()).toEqual(
      ['admin', 'approver', 'approver', 'approver', 'approver', 'operator'].sort(),
    );
  });

  it('an organisation cannot lose its last admin', async () => {
    if (skipped()) return;
    expect((await call('DELETE', `${org()}/members/${people.ada.id}`, 'ada')).status).toBe(409);
    expect((await call('PUT', `${org()}/members`, 'ada', { email: people.ada.email, role: 'viewer' })).status).toBe(409);
  });

  it('the approval policy defaults to dual control and refuses an unreachable quorum', async () => {
    if (skipped()) return;
    const d = await call('GET', `${org()}/approval-policy`, 'alice');
    expect(d.body).toMatchObject({ requiredApprovals: 2, windowMinutes: 60, isDefault: true });
    expect((await call('PUT', `${org()}/approval-policy`, 'ada', { requiredApprovals: 9, windowMinutes: 60 })).status).toBe(400);
    expect((await call('PUT', `${org()}/approval-policy`, 'alice', { requiredApprovals: 1, windowMinutes: 60 })).status).toBe(403);
    const set = await call('PUT', `${org()}/approval-policy`, 'ada', { requiredApprovals: 2, windowMinutes: 120 });
    expect(set.status).toBe(200);
    expect(set.body).toMatchObject({ requiredApprovals: 2, windowMinutes: 120, isDefault: false, updatedBy: people.ada.id });
  });

  it('operators start transfers as themselves; approvers cannot start them', async () => {
    if (skipped()) return;
    const tx = { chainId: 11155111, to: '0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045', value: '1', gasLimit: 21000, gasPrice: '1', nonce: 0 };
    const r = await call('POST', `${org()}/settlements`, 'oscar', tx);
    expect(r.status).toBe(202);
    expect(temporal.started.at(-1)!.initiator).toEqual({ userId: people.oscar.id, label: people.oscar.email });
    expect((await call('POST', `${org()}/settlements`, 'alice', tx)).status).toBe(403);
  });

  it('two distinct approvers with fresh codes approve; the workflow hears each decision', async () => {
    if (skipped()) return;
    const { approvalId, workflowId } = await openApproval(people.oscar.id);
    const path = `${org()}/approvals/${approvalId}/decisions`;

    // The initiator is an operator: blocked by role before the database is asked.
    expect((await call('POST', path, 'oscar', { decision: 'approve', totpCode: code('oscar') })).status).toBe(403);
    // No code, a wrong code: refused.
    expect((await call('POST', path, 'alice', { decision: 'approve' })).status).toBe(401);
    expect((await call('POST', path, 'alice', { decision: 'approve', totpCode: '000000' })).status).toBe(401);
    // No two-factor at all: told to turn it on.
    expect((await call('POST', path, 'nomfa', { decision: 'approve' })).status).toBe(403);

    const first = await call('POST', path, 'alice', { decision: 'approve', totpCode: code('alice') });
    expect(first.status).toBe(200);
    expect(first.body).toMatchObject({ status: 'pending', approvals: 1, requiredApprovals: 2 });

    // Same person, same decision, again: idempotent, and re-delivered.
    const again = await call('POST', path, 'alice', { decision: 'approve', totpCode: code('alice') });
    expect(again.status).toBe(200);
    expect(again.body.approvals).toBe(1);
    // Same person, different decision: decisions cannot be changed.
    expect((await call('POST', path, 'alice', { decision: 'reject', reason: 'changed my mind', totpCode: code('alice') })).status).toBe(409);

    const second = await call('POST', path, 'sam', { decision: 'approve' }); // SSO: the IdP is the step-up
    expect(second.status).toBe(200);
    expect(second.body.status).toBe('approved');

    const view = await call('GET', `${org()}/approvals/${approvalId}`, 'oscar');
    expect(view.body.decisions.map((d: { email: string; stepUp: string }) => [d.email, d.stepUp])).toEqual([
      [people.alice.email, 'totp'],
      [people.sam.email, 'sso'],
    ]);
    const delivered = temporal.signals.filter((s) => s.workflowId === workflowId);
    expect(delivered.map((s) => s.approverUserId)).toEqual([people.alice.id, people.alice.id, people.sam.id]);

    // Closed: a third approver is refused.
    expect((await call('POST', path, 'bob', { decision: 'approve', totpCode: code('bob') })).status).toBe(409);
  });

  it('an admin cannot approve a transfer they started', async () => {
    if (skipped()) return;
    const { approvalId } = await openApproval(people.ada.id);
    const r = await call('POST', `${org()}/approvals/${approvalId}/decisions`, 'ada', { decision: 'approve', totpCode: code('ada') });
    expect(r.status).toBe(403);
    expect(r.body.message).toMatch(/segregation of duties/);
  });

  it('one rejection rejects, and it needs a reason', async () => {
    if (skipped()) return;
    const { approvalId } = await openApproval(people.oscar.id);
    const path = `${org()}/approvals/${approvalId}/decisions`;
    expect((await call('POST', path, 'bob', { decision: 'reject', totpCode: code('bob') })).status).toBe(400);
    const r = await call('POST', path, 'bob', { decision: 'reject', reason: 'beneficiary not recognised', totpCode: code('bob') });
    expect(r.status).toBe(200);
    expect(r.body.status).toBe('rejected');
  });

  it('a decision whose delivery fails is kept, and resending delivers it', async () => {
    if (skipped()) return;
    const { approvalId, workflowId } = await openApproval(null);
    const path = `${org()}/approvals/${approvalId}/decisions`;
    temporal.failNextSignal = true;
    const failed = await call('POST', path, 'bob', { decision: 'approve', totpCode: code('bob') });
    expect(failed.status).toBe(502);
    expect(temporal.signals.some((s) => s.workflowId === workflowId)).toBe(false);
    const retried = await call('POST', path, 'bob', { decision: 'approve', totpCode: code('bob') });
    expect(retried.status).toBe(200);
    expect(retried.body.approvals).toBe(1);
    expect(temporal.signals.filter((s) => s.workflowId === workflowId)).toHaveLength(1);
  });

  it('people outside the organisation see nothing', async () => {
    if (skipped()) return;
    expect((await call('GET', `${org()}/approvals`, 'mallory')).status).toBe(404);
    expect((await call('GET', `/organisations/${randomUUID()}/approvals`, 'alice')).status).toBe(404);
    expect((await call('GET', `${org()}/approvals`, null)).status).toBe(401);
  });

  it('the queue lists pending approvals for every role that may read it', async () => {
    if (skipped()) return;
    const r = await call('GET', `${org()}/approvals?status=pending`, 'oscar');
    expect(r.status).toBe(200);
    expect(r.body.every((a: { status: string }) => a.status === 'pending')).toBe(true);
    expect(r.body.length).toBeGreaterThan(0);
  });

  it('approving with an API key is gone', async () => {
    if (skipped()) return;
    const r = await call('POST', `/settlements/settlement-${customerId}-x/approve`, null, { approved: true }, true);
    expect(r.status).toBe(410);
  });
});
