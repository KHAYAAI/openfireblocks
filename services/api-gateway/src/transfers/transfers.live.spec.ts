import { INestApplication, ValidationPipe } from '@nestjs/common';
import { Test } from '@nestjs/testing';
import { JwtModule, JwtService } from '@nestjs/jwt';
import { PassportModule } from '@nestjs/passport';
import { authenticator } from 'otplib';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
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
import { ApprovalRequiredException } from '../keys/approval-required.exception';
import { EvmRpcService } from '../tokens/evm-rpc.service';
import { OrgTransfersController } from './org-transfers.controller';
import { TransfersService } from './transfers.service';

// A transfer that needs approval, end to end over HTTP and real Postgres: the
// request is parked, the approvers decide with real JWTs and one-time codes,
// the database's triggers enforce who may and when, and the transfer runs
// exactly once when the quorum is reached. Only the chain side (KeysService)
// is replaced, by a recorder that refuses a high-value transfer until it is
// told approval was granted -- which is what the real one does.
//
//   eval "$(infrastructure/local/postgres-local.sh start)"   # migrations through 027
//   npx jest src/transfers/transfers.live.spec.ts

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';

class FakeKeys {
  calls: Array<{ kind: string; dto: any; granted: boolean }> = [];
  failNext = false;
  readonly keyId = randomUUID();
  async getKey(keyId: string) {
    return keyId === this.keyId ? { key_id: keyId, name: 'Solana ops', blockchain: 'solana', address: 'A' } : null;
  }
  assertTravelRuleComplete() {}
  async sendSolana(_c: unknown, _k: string, dto: any, opts: { approvalGranted?: boolean } = {}) {
    this.calls.push({ kind: 'solana', dto, granted: Boolean(opts.approvalGranted) });
    if (!opts.approvalGranted) throw new ApprovalRequiredException(['high-value transaction (> 10 ETH) requires approval'], 'r');
    if (this.failNext) {
      this.failNext = false;
      throw new Error('the chain signer is unreachable');
    }
    return { signature: 'SolSig-' + dto.idempotencyKey, broadcast: true };
  }
}

let reachable = false;
beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try {
    reachable = (await p.query(`SELECT to_regclass('pending_transfers') IS NOT NULL AS ok`)).rows[0].ok;
  } catch {
    reachable = false;
  } finally {
    await p.end().catch(() => undefined);
  }
});
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 027 is reachable');
  console.warn('skipping transfers live test -- no database with migration 027');
  return true;
}

describe('transfers that need approval (live Postgres)', () => {
  let app: INestApplication;
  let base: string;
  let admin: Pool;
  let tenantPool: Pool;
  let jwt: JwtService;
  let ownPools: Pool[] = [];
  const keys = new FakeKeys();
  let customerId = '';
  const people: Record<string, { id: string; email: string; secret: string; token: string }> = {};

  async function person(name: string) {
    const email = `${name}-${randomUUID().slice(0, 8)}@example.test`;
    const secret = authenticator.generateSecret();
    const r = await admin.query(
      `INSERT INTO users (email, password_hash, full_name, mfa_secret, mfa_enabled) VALUES ($1, 'x', $2, $3, true) RETURNING id`,
      [email, name, secret],
    );
    const id = r.rows[0].id;
    people[name] = { id, email, secret, token: await jwt.signAsync({ sub: id, email, role: 'user' }) };
  }
  async function call(method: string, path: string, who: string, body?: unknown) {
    const res = await fetch(`${base}${path}`, {
      method, headers: { 'content-type': 'application/json', authorization: `Bearer ${people[who].token}` },
      body: body ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    return { status: res.status, body: text ? JSON.parse(text) : null };
  }
  const org = () => `/organisations/${customerId}`;
  const decide = (who: string, approvalId: string, decision = 'approve', reason?: string) =>
    call('POST', `${org()}/approvals/${approvalId}/decisions`, who, { decision, reason, totpCode: authenticator.generate(people[who].secret) });
  const send = (who: string, amount = '15000000000') =>
    call('POST', `${org()}/keys/${keys.keyId}/transfers`, who, { destination: '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM', amount });
  const granted = () => keys.calls.filter((c) => c.granted);

  beforeAll(async () => {
    if (!reachable) return;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN;
    admin = new Pool({ connectionString: ADMIN_DSN });
    tenantPool = new Pool({ connectionString: TENANT_DSN });
    const moduleRef = await Test.createTestingModule({
      imports: [PassportModule, JwtModule.register({ secret: jwtSecret(), signOptions: { issuer: 'openfireblocks' } })],
      controllers: [ApprovalsController, OrgTransfersController],
      providers: [
        ApprovalsService, TenantRoleGuard, CustomerService, UsersService, JwtAuthStrategy, AuditService, NativeApprovalHooks, TransfersService,
        { provide: PG_POOL, useValue: tenantPool },
        { provide: TemporalService, useValue: { signalDecision: jest.fn(), start: jest.fn() } },
        { provide: KeysService, useValue: keys },
        { provide: EvmRpcService, useValue: { configured: () => false } },
      ],
    }).compile();
    app = moduleRef.createNestApplication();
    app.useGlobalPipes(new ValidationPipe({ whitelist: true, forbidNonWhitelisted: true, transform: true }));
    await app.listen(0, '127.0.0.1');
    base = (await app.getUrl()).replace('[::1]', '127.0.0.1');
    jwt = moduleRef.get(JwtService);
    ownPools = [
      (moduleRef.get(CustomerService) as unknown as { pool: Pool }).pool,
      (moduleRef.get(AuditService) as unknown as { adminPool: Pool }).adminPool,
    ];
    customerId = (await moduleRef.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    for (const n of ['ada', 'alice', 'bob', 'oscar']) await person(n);
    const apps = moduleRef.get(ApprovalsService);
    await apps.setMember(customerId, people.ada.email, 'admin');
    await apps.setMember(customerId, people.alice.email, 'approver');
    await apps.setMember(customerId, people.bob.email, 'approver');
    await apps.setMember(customerId, people.oscar.email, 'operator');
    await call('PUT', `${org()}/approval-policy`, 'ada', { requiredApprovals: 2, windowMinutes: 60 });
  });

  afterAll(async () => {
    if (!reachable) return;
    await app.close();
    await Promise.all(ownPools.map((p) => p.end().catch(() => undefined)));
    await admin.end();
    await tenantPool.end();
  });

  it('parks a high-value transfer and asks the approvers instead of signing it', async () => {
    if (skipped()) return;
    const before = keys.calls.length;
    const r = await send('oscar');
    expect(r.status).toBe(202);
    expect(r.body).toMatchObject({ status: 'pending_approval', requiredApprovals: 2 });
    expect(granted()).toHaveLength(0);

    const a = (await call('GET', `${org()}/approvals/${r.body.approvalId}`, 'alice')).body;
    expect(a).toMatchObject({ status: 'pending', initiatedBy: people.oscar.email });
    expect(a.summary).toMatchObject({ asset: 'SOL', amount: '15000000000', to: expect.any(String), keyName: 'Solana ops' });
    expect(a.execution).toMatchObject({ status: 'awaiting_approval' });
    expect(keys.calls.length).toBe(before + 1); // the refused attempt only
  });

  it('runs the transfer exactly once when the quorum is reached, and not before', async () => {
    if (skipped()) return;
    const { approvalId } = (await send('oscar')).body;
    const first = await decide('alice', approvalId);
    expect(first.status).toBe(200);
    expect(first.body.status).toBe('pending');
    expect(first.body.execution).toMatchObject({ status: 'awaiting_approval' });
    expect(granted().filter((c) => c.dto.idempotencyKey === `approval:${approvalId}`)).toHaveLength(0);

    const second = await decide('bob', approvalId);
    expect(second.body.status).toBe('approved');
    expect(second.body.execution).toMatchObject({ status: 'completed', result: { signature: `SolSig-approval:${approvalId}` } });

    // The same decision sent again (a lost response, a retry) is refused and does not send twice.
    const again = await decide('bob', approvalId);
    expect(again.status).toBe(409);
    expect(granted().filter((c) => c.dto.idempotencyKey === `approval:${approvalId}`)).toHaveLength(1);

    const row = (await admin.query(`SELECT status, result FROM pending_transfers WHERE approval_id = $1`, [approvalId])).rows[0];
    expect(row.status).toBe('completed');
  });

  it('executes what was asked for: the stored request, not anything supplied later', async () => {
    if (skipped()) return;
    const { approvalId } = (await send('oscar', '25000000000')).body;
    await decide('alice', approvalId);
    await decide('bob', approvalId);
    const run = granted().find((c) => c.dto.idempotencyKey === `approval:${approvalId}`)!;
    expect(run.dto).toMatchObject({ amount: '25000000000', destination: '9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM' });
  });

  it('never lets the person who asked for a transfer approve it', async () => {
    if (skipped()) return;
    const { approvalId } = (await send('ada')).body; // an admin is allowed to decide in general
    const own = await decide('ada', approvalId);
    expect([403, 409]).toContain(own.status);
    const a = (await call('GET', `${org()}/approvals/${approvalId}`, 'ada')).body;
    expect(a.approvals).toBe(0);
    expect(a.execution.status).toBe('awaiting_approval');
  });

  it('does not run a rejected transfer', async () => {
    if (skipped()) return;
    const { approvalId } = (await send('oscar')).body;
    const r = await decide('alice', approvalId, 'reject', 'recipient is not on the supplier list');
    expect(r.body.status).toBe('rejected');
    expect(r.body.execution).toMatchObject({ status: 'rejected' });
    expect(granted().filter((c) => c.dto.idempotencyKey === `approval:${approvalId}`)).toHaveLength(0);
    // And a decision after rejection is refused by the database.
    expect((await decide('bob', approvalId)).status).toBe(409);
  });

  it('reports an execution failure, keeps the approval, and lets an admin retry', async () => {
    if (skipped()) return;
    const { approvalId } = (await send('oscar')).body;
    keys.failNext = true;
    await decide('alice', approvalId);
    const out = await decide('bob', approvalId);
    expect(out.status).toBe(200);
    expect(out.body.status).toBe('approved');
    expect(out.body.execution).toMatchObject({ status: 'failed', error: 'the chain signer is unreachable' });

    expect((await call('POST', `${org()}/approvals/${approvalId}/execute`, 'alice')).status).toBe(403); // not an admin
    const retried = await call('POST', `${org()}/approvals/${approvalId}/execute`, 'ada');
    expect(retried.body).toMatchObject({ status: 'completed' });
    expect(granted().filter((c) => c.dto.idempotencyKey === `approval:${approvalId}`)).toHaveLength(2); // the failed run and the retry, one success
  });

  it('an operator may start a transfer but an approver may not', async () => {
    if (skipped()) return;
    expect((await send('alice')).status).toBe(403);
  });

  it('marks a transfer nobody decided in time as expired and never runs it', async () => {
    if (skipped()) return;
    const wf = `native:${randomUUID()}`;
    const r = await admin.query(
      `INSERT INTO approval_requests (customer_id, workflow_id, initiated_by_label, required_approvals, summary, created_at, expires_at)
       VALUES ($1, $2, 'api-key', 2, '{}', NOW() - interval '2 hours', NOW() - interval '1 hour') RETURNING approval_id`,
      [customerId, wf],
    );
    const id = r.rows[0].approval_id;
    await admin.query(`INSERT INTO pending_transfers (approval_id, customer_id, key_id, kind, request) VALUES ($1, $2, $3, 'solana', '{}')`, [id, customerId, keys.keyId]);
    const a = (await call('GET', `${org()}/approvals/${id}`, 'alice')).body;
    expect(a.status).toBe('expired');
    expect(a.execution.status).toBe('expired');
  });

  describe('what the database refuses regardless of the application', () => {
    it('will not change a pending transfer\'s request', async () => {
      if (skipped()) return;
      const { approvalId } = (await send('oscar')).body;
      await expect(admin.query(`UPDATE pending_transfers SET request = '{"amount":"1"}' WHERE approval_id = $1`, [approvalId])).rejects.toThrow(/cannot change/);
    });
    it('will not let a transfer start executing before its approval is approved', async () => {
      if (skipped()) return;
      const { approvalId } = (await send('oscar')).body;
      await expect(admin.query(`UPDATE pending_transfers SET status = 'executing' WHERE approval_id = $1`, [approvalId])).rejects.toThrow(/not approved/);
    });
    it('will not skip the lifecycle', async () => {
      if (skipped()) return;
      const { approvalId } = (await send('oscar')).body;
      await expect(admin.query(`UPDATE pending_transfers SET status = 'completed' WHERE approval_id = $1`, [approvalId])).rejects.toThrow(/cannot go from/);
    });
    it('will not delete one', async () => {
      if (skipped()) return;
      const { approvalId } = (await send('oscar')).body;
      await expect(admin.query(`DELETE FROM pending_transfers WHERE approval_id = $1`, [approvalId])).rejects.toThrow();
    });
  });
});
