import {
  BadRequestException,
  ConflictException,
  ForbiddenException,
  Inject,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { TenantRole, isTenantRole } from './roles';
import { UUID_RE, withTenant } from './tenant-db';

export interface ApprovalPolicy {
  requiredApprovals: number;
  windowMinutes: number;
  updatedBy: string | null;
  updatedAt: string | null;
  // True when the tenant has never set one and the defaults apply.
  isDefault: boolean;
}

export interface ApprovalDecisionView {
  userId: string;
  email: string;
  fullName: string;
  decision: 'approve' | 'reject';
  reason: string | null;
  stepUp: 'totp' | 'sso';
  createdAt: string;
}

export interface ApprovalRequestView {
  approvalId: string;
  workflowId: string;
  status: 'pending' | 'approved' | 'rejected' | 'expired';
  requiredApprovals: number;
  approvals: number;
  initiatedByUserId: string | null;
  initiatedBy: string;
  summary: Record<string, unknown>;
  createdAt: string;
  expiresAt: string;
  decidedAt: string | null;
  decisions?: ApprovalDecisionView[];
}

export interface Member {
  userId: string;
  email: string;
  fullName: string;
  role: TenantRole;
  mfaEnabled: boolean;
  authProvider: string;
}

export type Decision = 'approve' | 'reject';

// Defaults when a tenant has set no policy. Must match migration 023 and
// the worker's OpenApprovalRequest.
export const DEFAULT_POLICY = { requiredApprovals: 2, windowMinutes: 60 };

// Postgres SQLSTATEs raised by migration 023's triggers.
function mapDbError(err: unknown): never {
  const code = (err as { code?: string }).code;
  const message = (err as Error).message;
  switch (code) {
    case 'OFB01':
    case 'OFB02':
      throw new ForbiddenException(message);
    case 'OFB03':
    case 'OFB04':
      throw new ConflictException(message);
  }
  throw err;
}

function toView(row: Record<string, any>): ApprovalRequestView {
  return {
    approvalId: row.approval_id,
    workflowId: row.workflow_id,
    status: row.status,
    requiredApprovals: row.required_approvals,
    approvals: Number(row.approvals ?? 0),
    initiatedByUserId: row.initiated_by_user_id,
    initiatedBy: row.initiated_by_label,
    summary: row.summary,
    createdAt: new Date(row.created_at).toISOString(),
    expiresAt: new Date(row.expires_at).toISOString(),
    decidedAt: row.decided_at ? new Date(row.decided_at).toISOString() : null,
  };
}

// Reads and writes approvals, approval policy and organisation membership.
//
// This service does not decide whether a decision is allowed. The
// database does (migration 023): its triggers refuse the initiator, a
// non-approver, a second decision by the same person, and anything after
// the request closed. Checking the same things here as well would be a
// second copy of the rules to keep in step, and the copy that drifts is
// the one that gets trusted. What this service adds is mapping those
// refusals onto HTTP.
@Injectable()
export class ApprovalsService {
  // Only for organisationsFor: which organisations a person belongs to is
  // a question across tenants, which the RLS-scoped pool cannot answer by
  // design. Same DATABASE_ADMIN_URL convention as CustomerService.
  private adminPool: Pool | null = null;

  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  private admin(): Pool {
    if (!this.adminPool) {
      this.adminPool = new Pool({
        connectionString:
          process.env.DATABASE_ADMIN_URL ?? 'postgresql://app_admin:dev-only@localhost:5432/openfireblocks',
      });
    }
    return this.adminPool;
  }

  async onModuleDestroy() {
    await this.adminPool?.end().catch(() => undefined);
  }

  // The organisations a person holds a role in, and the role. Nothing
  // else about those organisations.
  async organisationsFor(userId: string): Promise<Array<{ customerId: string; name: string; role: TenantRole }>> {
    if (!UUID_RE.test(userId)) return [];
    const r = await this.admin().query(
      `SELECT m.customer_id, c.name, m.role
         FROM user_customer_roles m JOIN customers c ON c.customer_id = m.customer_id
        WHERE m.user_id = $1 AND c.status = 'active'
        ORDER BY c.name`,
      [userId],
    );
    return r.rows.map((row) => ({ customerId: row.customer_id, name: row.name, role: row.role }));
  }

  async roleOf(userId: string, customerId: string): Promise<TenantRole | null> {
    if (!UUID_RE.test(customerId) || !UUID_RE.test(userId)) return null;
    const rows = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT role FROM user_customer_roles WHERE user_id = $1 AND customer_id = $2`, [userId, customerId]),
    );
    const role = rows.rows[0]?.role;
    return isTenantRole(role) ? role : null;
  }

  // ---------------------------------------------------------------- policy

  async getPolicy(customerId: string): Promise<ApprovalPolicy> {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT required_approvals, window_minutes, updated_by, updated_at
                 FROM approval_policies WHERE customer_id = $1`, [customerId]),
    );
    const row = r.rows[0];
    if (!row) {
      return { ...DEFAULT_POLICY, updatedBy: null, updatedAt: null, isDefault: true };
    }
    return {
      requiredApprovals: row.required_approvals,
      windowMinutes: row.window_minutes,
      updatedBy: row.updated_by,
      updatedAt: new Date(row.updated_at).toISOString(),
      isDefault: false,
    };
  }

  async setPolicy(customerId: string, userId: string, requiredApprovals: number, windowMinutes: number): Promise<ApprovalPolicy> {
    if (!Number.isInteger(requiredApprovals) || requiredApprovals < 1 || requiredApprovals > 10) {
      throw new BadRequestException('requiredApprovals must be a whole number from 1 to 10');
    }
    if (!Number.isInteger(windowMinutes) || windowMinutes < 5 || windowMinutes > 10080) {
      throw new BadRequestException('windowMinutes must be a whole number from 5 to 10080 (one week)');
    }
    // A quorum that the organisation's own approvers cannot reach would
    // freeze every transfer that needs approval. Refuse it up front rather
    // than let the first high-value payment discover it.
    const deciders = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT COUNT(*)::int AS n FROM user_customer_roles
                WHERE customer_id = $1 AND role IN ('admin','approver')`, [customerId]),
    );
    const available = deciders.rows[0].n as number;
    if (requiredApprovals > available) {
      throw new BadRequestException(
        `this organisation has ${available} people who can approve; a quorum of ${requiredApprovals} could never be met`,
      );
    }
    await withTenant(this.pool, customerId, (c) =>
      c.query(
        `INSERT INTO approval_policies (customer_id, required_approvals, window_minutes, updated_by, updated_at)
         VALUES ($1, $2, $3, $4, NOW())
         ON CONFLICT (customer_id) DO UPDATE
           SET required_approvals = EXCLUDED.required_approvals,
               window_minutes = EXCLUDED.window_minutes,
               updated_by = EXCLUDED.updated_by,
               updated_at = NOW()`,
        [customerId, requiredApprovals, windowMinutes, userId],
      ),
    );
    return this.getPolicy(customerId);
  }

  // ------------------------------------------------------------- approvals

  async list(customerId: string, status?: string, limit = 50): Promise<ApprovalRequestView[]> {
    if (status && !['pending', 'approved', 'rejected', 'expired'].includes(status)) {
      throw new BadRequestException('status must be pending, approved, rejected or expired');
    }
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT r.*,
                (SELECT COUNT(*) FROM approval_decisions d
                  WHERE d.approval_id = r.approval_id AND d.decision = 'approve') AS approvals
           FROM approval_requests r
          WHERE r.customer_id = $1 AND ($2::text IS NULL OR r.status = $2)
          ORDER BY r.created_at DESC
          LIMIT $3`,
        [customerId, status ?? null, Math.min(Math.max(limit, 1), 200)],
      ),
    );
    return r.rows.map(toView);
  }

  async get(customerId: string, approvalId: string): Promise<ApprovalRequestView> {
    if (!UUID_RE.test(approvalId)) throw new NotFoundException('approval request not found');
    return withTenant(this.pool, customerId, async (c) => {
      const r = await c.query(
        `SELECT r.*,
                (SELECT COUNT(*) FROM approval_decisions d
                  WHERE d.approval_id = r.approval_id AND d.decision = 'approve') AS approvals
           FROM approval_requests r WHERE r.approval_id = $1`,
        [approvalId],
      );
      if (!r.rows[0]) throw new NotFoundException('approval request not found');
      const view = toView(r.rows[0]);
      const d = await c.query(
        `SELECT d.user_id, u.email, u.full_name, d.decision, d.reason, d.step_up, d.created_at
           FROM approval_decisions d JOIN users u ON u.id = d.user_id
          WHERE d.approval_id = $1 ORDER BY d.created_at`,
        [approvalId],
      );
      view.decisions = d.rows.map((row) => ({
        userId: row.user_id,
        email: row.email,
        fullName: row.full_name,
        decision: row.decision,
        reason: row.reason,
        stepUp: row.step_up,
        createdAt: new Date(row.created_at).toISOString(),
      }));
      return view;
    });
  }

  // Records a decision. The database enforces every rule; see the class
  // comment. Returns the request as it stands afterwards.
  //
  // Idempotent for a retried request: if this person already made this
  // exact decision, returns { alreadyRecorded: true } rather than a
  // conflict, so a caller whose response was lost -- or whose signal to
  // the workflow failed -- can simply send the same request again.
  async recordDecision(input: {
    customerId: string;
    approvalId: string;
    userId: string;
    decision: Decision;
    reason?: string;
    stepUp: 'totp' | 'sso';
  }): Promise<{ request: ApprovalRequestView; alreadyRecorded: boolean }> {
    if (!UUID_RE.test(input.approvalId)) throw new NotFoundException('approval request not found');
    if (input.decision !== 'approve' && input.decision !== 'reject') {
      throw new BadRequestException('decision must be "approve" or "reject"');
    }
    if (input.decision === 'reject' && !input.reason?.trim()) {
      // A rejection stops a payment someone asked for; the person who
      // asked is owed a reason.
      throw new BadRequestException('a rejection needs a reason');
    }
    let alreadyRecorded = false;
    try {
      await withTenant(this.pool, input.customerId, (c) =>
        c.query(
          `INSERT INTO approval_decisions (approval_id, customer_id, user_id, decision, reason, step_up)
           VALUES ($1, $2, $3, $4, $5, $6)`,
          [input.approvalId, input.customerId, input.userId, input.decision, input.reason?.trim() || null, input.stepUp],
        ),
      );
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (code === '23505') {
        const prior = await withTenant(this.pool, input.customerId, (c) =>
          c.query(`SELECT decision FROM approval_decisions WHERE approval_id = $1 AND user_id = $2`, [
            input.approvalId,
            input.userId,
          ]),
        );
        if (prior.rows[0]?.decision !== input.decision) {
          throw new ConflictException(`you already decided "${prior.rows[0]?.decision}" on this request; decisions cannot be changed`);
        }
        alreadyRecorded = true;
      } else if (code === '23503') {
        throw new NotFoundException('approval request not found');
      } else {
        mapDbError(err);
      }
    }
    return { request: await this.get(input.customerId, input.approvalId), alreadyRecorded };
  }

  // --------------------------------------------------------------- members

  async listMembers(customerId: string): Promise<Member[]> {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT m.user_id, u.email, u.full_name, m.role, u.mfa_enabled, u.auth_provider
           FROM user_customer_roles m JOIN users u ON u.id = m.user_id
          WHERE m.customer_id = $1 ORDER BY u.email`,
        [customerId],
      ),
    );
    return r.rows.map((row) => ({
      userId: row.user_id,
      email: row.email,
      fullName: row.full_name,
      role: row.role,
      mfaEnabled: row.mfa_enabled,
      authProvider: row.auth_provider,
    }));
  }

  async countAdmins(customerId: string): Promise<number> {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT COUNT(*)::int AS n FROM user_customer_roles WHERE customer_id = $1 AND role = 'admin'`, [customerId]),
    );
    return r.rows[0].n;
  }

  // Grants (or changes) a person's role. The person must already have an
  // account -- registered, or signed in once through SSO -- because a role
  // for an email nobody controls yet is a role for whoever registers it
  // first.
  async setMember(customerId: string, email: string, role: TenantRole): Promise<Member> {
    if (!isTenantRole(role)) throw new BadRequestException(`unknown role "${role}"`);
    const u = await this.pool.query(`SELECT id FROM users WHERE email = $1 AND status = 'active'`, [
      String(email).toLowerCase(),
    ]);
    const userId = u.rows[0]?.id;
    if (!userId) {
      throw new NotFoundException('no active account with that email; the person must register or sign in with SSO first');
    }
    await withTenant(this.pool, customerId, async (c) => {
      const current = await c.query(
        `SELECT role FROM user_customer_roles WHERE user_id = $1 AND customer_id = $2 FOR UPDATE`,
        [userId, customerId],
      );
      if (current.rows[0]?.role === 'admin' && role !== 'admin') {
        await this.assertNotLastAdmin(c, customerId);
      }
      await c.query(
        `INSERT INTO user_customer_roles (user_id, customer_id, role) VALUES ($1, $2, $3)
         ON CONFLICT (user_id, customer_id) DO UPDATE SET role = EXCLUDED.role`,
        [userId, customerId, role],
      );
    });
    const members = await this.listMembers(customerId);
    return members.find((m) => m.userId === userId)!;
  }

  async removeMember(customerId: string, userId: string): Promise<void> {
    if (!UUID_RE.test(userId)) throw new NotFoundException('member not found');
    await withTenant(this.pool, customerId, async (c) => {
      const current = await c.query(
        `SELECT role FROM user_customer_roles WHERE user_id = $1 AND customer_id = $2 FOR UPDATE`,
        [userId, customerId],
      );
      if (!current.rows[0]) throw new NotFoundException('member not found');
      if (current.rows[0].role === 'admin') await this.assertNotLastAdmin(c, customerId);
      await c.query(`DELETE FROM user_customer_roles WHERE user_id = $1 AND customer_id = $2`, [userId, customerId]);
    });
  }

  private async assertNotLastAdmin(c: { query: Pool['query'] }, customerId: string) {
    const r = await c.query(
      `SELECT COUNT(*)::int AS n FROM user_customer_roles WHERE customer_id = $1 AND role = 'admin'`,
      [customerId],
    );
    if ((r.rows[0] as { n: number }).n <= 1) {
      throw new ConflictException('an organisation must keep at least one admin');
    }
  }
}
