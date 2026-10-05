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
import { v4 as uuidv4 } from 'uuid';
import type { TransferExecution } from './native-approval-hooks';

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
  // For a transfer the gateway signs itself: what became of it.
  execution?: TransferExecution;
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

// Approval requests for transfers the gateway signs itself carry this prefix in
// their workflow id (the column is unique, so it doubles as the request's
// natural key). Settlements keep their Temporal workflow ids.
export const NATIVE_WORKFLOW_PREFIX = 'native:';

export type TransferKind = 'bitcoin' | 'solana' | 'cosmos' | 'evm';

export interface PendingTransferRow {
  approvalId: string;
  customerId: string;
  keyId: string;
  kind: TransferKind;
  request: Record<string, any>;
  prepared: Record<string, any> | null;
  status: TransferExecution['status'];
  result: Record<string, unknown> | null;
  error: string | null;
}

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
    await this.expireStaleTransfers(customerId);
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
    await this.expireStaleTransfers(customerId);
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
      const pt = await c.query(`SELECT status, result, error FROM pending_transfers WHERE approval_id = $1`, [approvalId]);
      if (pt.rows[0]) {
        view.execution = { status: pt.rows[0].status, result: pt.rows[0].result, error: pt.rows[0].error };
      }
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

  // ------------------------------------------- transfers the gateway signs

  // Opens an approval request and the pending transfer it is for, together: a
  // request with nothing to execute, or a transfer with nothing to approve,
  // would each be a way for money to wait forever or move unasked.
  async openTransferRequest(input: {
    customerId: string;
    initiator: { userId: string | null; label: string };
    requiredApprovals: number;
    windowMinutes: number;
    summary: Record<string, unknown>;
    keyId: string;
    kind: TransferKind;
    request: Record<string, unknown>;
  }): Promise<{ approvalId: string; expiresAt: string }> {
    const workflowId = NATIVE_WORKFLOW_PREFIX + uuidv4();
    return withTenant(this.pool, input.customerId, async (c) => {
      const r = await c.query(
        `INSERT INTO approval_requests
           (customer_id, workflow_id, initiated_by_user_id, initiated_by_label, required_approvals, summary, expires_at)
         VALUES ($1, $2, $3, $4, $5, $6, NOW() + ($7 || ' minutes')::interval)
         RETURNING approval_id, expires_at`,
        [input.customerId, workflowId, input.initiator.userId, input.initiator.label, input.requiredApprovals,
          JSON.stringify(input.summary), String(input.windowMinutes)],
      );
      await c.query(
        `INSERT INTO pending_transfers (approval_id, customer_id, key_id, kind, request) VALUES ($1, $2, $3, $4, $5)`,
        [r.rows[0].approval_id, input.customerId, input.keyId, input.kind, JSON.stringify(input.request)],
      );
      return { approvalId: r.rows[0].approval_id, expiresAt: new Date(r.rows[0].expires_at).toISOString() };
    });
  }

  async pendingTransfer(customerId: string, approvalId: string): Promise<PendingTransferRow | null> {
    if (!UUID_RE.test(approvalId)) return null;
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT * FROM pending_transfers WHERE approval_id = $1`, [approvalId]),
    );
    const x = r.rows[0];
    return x
      ? { approvalId: x.approval_id, customerId: x.customer_id, keyId: x.key_id, kind: x.kind, request: x.request,
          prepared: x.prepared, status: x.status, result: x.result, error: x.error }
      : null;
  }

  // Moves a pending transfer along its lifecycle, returning whether THIS call
  // made the move. The database allows only one caller to take "executing"
  // (and refuses it unless the approval is approved), so a decision delivered
  // twice, or an admin retrying while a run is in flight, cannot send twice.
  async transitionTransfer(
    customerId: string,
    approvalId: string,
    from: string[],
    to: TransferExecution['status'],
    patch: { result?: Record<string, unknown>; error?: string | null; prepared?: Record<string, unknown> } = {},
  ): Promise<boolean> {
    try {
      const r = await withTenant(this.pool, customerId, (c) =>
        c.query(
          `UPDATE pending_transfers
              SET status = $3, result = COALESCE($4::jsonb, result), error = $5, prepared = COALESCE($6::jsonb, prepared)
            WHERE approval_id = $1 AND customer_id = $2 AND status = ANY($7::text[])
            RETURNING approval_id`,
          [approvalId, customerId, to,
            patch.result ? JSON.stringify(patch.result) : null,
            patch.error ?? null,
            patch.prepared ? JSON.stringify(patch.prepared) : null,
            from],
        ),
      );
      return (r.rowCount ?? 0) > 0;
    } catch (err) {
      const code = (err as { code?: string }).code;
      if (code === 'OFB01' || code === 'OFB03') return false;
      throw err;
    }
  }

  // A request nobody decided in time. Done when requests are read, per
  // organisation, rather than by a scheduler: there is nothing to do until
  // someone looks, and the database refuses a decision after expiry anyway.
  async expireStaleTransfers(customerId: string): Promise<void> {
    await withTenant(this.pool, customerId, async (c) => {
      const r = await c.query(
        `UPDATE approval_requests SET status = 'expired', decided_at = NOW()
          WHERE customer_id = $1 AND workflow_id LIKE $2 AND status = 'pending' AND expires_at < NOW()
          RETURNING approval_id`,
        [customerId, NATIVE_WORKFLOW_PREFIX + '%'],
      );
      if (r.rows.length) {
        await c.query(
          `UPDATE pending_transfers SET status = 'expired' WHERE approval_id = ANY($1::uuid[]) AND status = 'awaiting_approval'`,
          [r.rows.map((x) => x.approval_id)],
        );
      }
    });
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
