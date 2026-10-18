import { INestApplication, ValidationPipe } from '@nestjs/common';
import { Test } from '@nestjs/testing';
import { JwtModule, JwtService } from '@nestjs/jwt';
import { PassportModule } from '@nestjs/passport';
import { authenticator } from 'otplib';
import { Pool } from 'pg';
import { createHash, randomUUID } from 'crypto';
import { ApprovalsService } from '../approvals/approvals.service';
import { TenantRoleGuard } from '../approvals/tenant-role.guard';
import { CustomerService } from '../customers/customer.service';
import { UsersService } from '../identity/users.service';
import { JwtAuthStrategy, jwtSecret } from '../identity/jwt.strategy';
import { AuditService } from '../database/audit.service';
import { PG_POOL } from '../database/pg-pool.token';
import { AccessReviewController } from './access-review.controller';
import { AccessReviewService, canonical } from './access-review.service';

// Access reviews over real Postgres and real HTTP with real JWTs and real one-time codes.
//
//   eval "$(infrastructure/local/postgres-local.sh start)"   # migrations through 036
//   npx jest src/access-review/access-review.live.spec.ts

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';

let reachable = false;
beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try { reachable = (await p.query(`SELECT to_regclass('access_reviews') IS NOT NULL AS ok`)).rows[0].ok; } catch { reachable = false; } finally { await p.end().catch(() => undefined); }
});
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 036 is reachable');
  console.warn('skipping access review live test -- no database with migration 036');
  return true;
}

// Waiting out the end of a one-time-code window can take a few seconds.
jest.setTimeout(30_000);

describe('access reviews (live Postgres)', () => {
  let app: INestApplication; let base: string; let admin: Pool; let tenantPool: Pool; let jwt: JwtService;
  let ownPools: Pool[] = []; let customerId = ''; let otherCustomerId = '';
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
    people[name] = { id: r.rows[0].id, email, secret: secret ?? undefined, token: await jwt.signAsync({ sub: r.rows[0].id, email, role: 'user' }) };
  }
  // A code with a few seconds left in its window, so the server sees the same one.
  async function code(who: string) {
    const left = authenticator.timeRemaining();
    if (left < 5) await new Promise((r) => setTimeout(r, (left + 1) * 1000));
    return authenticator.generate(people[who].secret!);
  }
  async function call(method: string, path: string, who: string, body?: unknown, org = customerId) {
    const res = await fetch(`${base}/organisations/${org}${path}`, { method, headers: { 'content-type': 'application/json', authorization: `Bearer ${people[who].token}` }, body: body ? JSON.stringify(body) : undefined });
    const text = await res.text();
    return { status: res.status, body: text ? JSON.parse(text) : null };
  }

  beforeAll(async () => {
    if (!reachable) return;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN;
    admin = new Pool({ connectionString: ADMIN_DSN }); tenantPool = new Pool({ connectionString: TENANT_DSN });
    const m = await Test.createTestingModule({
      imports: [PassportModule, JwtModule.register({ secret: jwtSecret(), signOptions: { issuer: 'openfireblocks' } })],
      controllers: [AccessReviewController],
      providers: [AccessReviewService, ApprovalsService, TenantRoleGuard, CustomerService, UsersService, JwtAuthStrategy, AuditService,
        { provide: PG_POOL, useValue: tenantPool }],
    }).useMocker((tok) => (typeof tok === 'function' ? { signalDecision: jest.fn(), start: jest.fn() } : undefined)).compile();
    app = m.createNestApplication();
    app.useGlobalPipes(new ValidationPipe({ whitelist: true, forbidNonWhitelisted: true, transform: true }));
    await app.listen(0, '127.0.0.1');
    base = (await app.getUrl()).replace('[::1]', '127.0.0.1');
    jwt = m.get(JwtService);
    ownPools = [(m.get(CustomerService) as unknown as { pool: Pool }).pool, (m.get(AuditService) as unknown as { adminPool: Pool }).adminPool];
    customerId = (await m.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    otherCustomerId = (await m.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    await person('ada', { mfa: true }); await person('aud', { sso: true }); await person('oscar', { mfa: true });
    await person('nomfa'); await person('leaver', { mfa: true });
    const a = m.get(ApprovalsService);
    await a.setMember(customerId, people.ada.email, 'admin');
    await a.setMember(customerId, people.aud.email, 'auditor');
    await a.setMember(customerId, people.oscar.email, 'operator');
    await a.setMember(customerId, people.nomfa.email, 'admin');
    await a.setMember(customerId, people.leaver.email, 'approver');
    await admin.query(`UPDATE users SET status = 'suspended' WHERE id = $1`, [people.leaver.id]);
  });
  afterAll(async () => {
    if (!reachable) return;
    await app.close(); await Promise.all(ownPools.map((p) => p.end().catch(() => undefined)));
    await admin.end(); await tenantPool.end();
  });

  it('shows admins and auditors who has access, with what deserves a look; nobody else', async () => {
    if (skipped()) return;
    expect((await call('GET', '/access-review', 'oscar')).status).toBe(403);
    const r = await call('GET', '/access-review', 'aud');
    expect(r.status).toBe(200);
    expect(r.body.snapshot.members.map((x: { email: string }) => x.email).sort()).toEqual(
      ['ada', 'aud', 'oscar', 'nomfa', 'leaver'].map((n) => people[n].email).sort());
    const f = r.body.findings.map((x: { code: string; subject: string }) => `${x.code}:${x.subject}`);
    expect(f).toEqual(expect.arrayContaining([
      `inactive_account_has_role:${people.leaver.email}`, `no_mfa:${people.nomfa.email}`, 'first_review:organisation',
    ]));
    expect(r.body.findings[0].severity).toBe('high');
  });

  it('only takes a sign-off with proof of presence, and asks for notes when something changed', async () => {
    if (skipped()) return;
    expect((await call('POST', '/access-reviews', 'oscar', { outcome: 'no_changes', totpCode: await code('oscar') })).status).toBe(403);
    expect((await call('POST', '/access-reviews', 'ada', { outcome: 'no_changes' })).status).toBe(401);
    expect((await call('POST', '/access-reviews', 'ada', { outcome: 'no_changes', totpCode: '000000' })).status).toBe(401);
    expect((await call('POST', '/access-reviews', 'nomfa', { outcome: 'no_changes' })).status).toBe(403);
    expect((await call('POST', '/access-reviews', 'ada', { outcome: 'changes_required', totpCode: await code('ada') })).status).toBe(400);
    expect((await call('POST', '/access-reviews', 'ada', { outcome: 'rubber_stamp', totpCode: await code('ada') })).status).toBe(400);
  });

  it('records a signed review with the snapshot the server took, and its hash', async () => {
    if (skipped()) return;
    const r = await call('POST', '/access-reviews', 'aud', { outcome: 'changes_required', notes: 'Remove the leaver; nomfa to enrol MFA by Friday.' });
    expect(r.status).toBe(201);
    expect(r.body).toMatchObject({ outcome: 'changes_required', reviewerEmail: people.aud.email, reviewerRole: 'auditor', verifiedBy: 'sso' });
    expect(r.body.snapshotSha256).toBe(createHash('sha256').update(canonical(r.body.snapshot)).digest('hex'));
    const listed = await call('GET', '/access-reviews', 'ada');
    expect(listed.body.map((x: { reviewId: string }) => x.reviewId)).toContain(r.body.reviewId);
    expect((await call('GET', `/access-reviews/${r.body.reviewId}`, 'ada')).body.notes).toMatch(/leaver/);
    // The next snapshot knows about it.
    const next = await call('GET', '/access-review', 'ada');
    expect(next.body.snapshot.previousReview).toMatchObject({ reviewId: r.body.reviewId, outcome: 'changes_required' });
    expect(next.body.findings.map((x: { code: string }) => x.code)).not.toContain('first_review');
    const audit = await admin.query(`SELECT 1 FROM audit.events WHERE customer_id = $1 AND event_type = 'access_review.recorded'`, [customerId]);
    expect(audit.rowCount).toBe(1);
  });

  it('cannot be edited or deleted, even by the database owner role the app uses for admin work', async () => {
    if (skipped()) return;
    const id = (await call('GET', '/access-reviews', 'ada')).body[0].reviewId;
    await expect(admin.query(`UPDATE access_reviews SET outcome = 'no_changes' WHERE review_id = $1`, [id])).rejects.toMatchObject({ code: 'OFB06' });
    await expect(admin.query(`DELETE FROM access_reviews WHERE review_id = $1`, [id])).rejects.toMatchObject({ code: 'OFB06' });
  });

  it('keeps one organisation out of another', async () => {
    if (skipped()) return;
    const id = (await call('GET', '/access-reviews', 'ada')).body[0].reviewId;
    // Not a member there: the guard answers as if the organisation did not exist.
    expect((await call('GET', '/access-reviews', 'ada', undefined, otherCustomerId)).status).toBe(404);
    // Row-level security, underneath the role check.
    const c = await tenantPool.connect();
    try {
      await c.query('BEGIN'); await c.query(`SELECT set_config('app.current_customer_id', $1, true)`, [otherCustomerId]);
      expect((await c.query(`SELECT 1 FROM access_reviews WHERE review_id = $1`, [id])).rowCount).toBe(0);
    } finally { await c.query('ROLLBACK'); c.release(); }
  });
});
