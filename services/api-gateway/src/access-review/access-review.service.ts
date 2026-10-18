import {
  BadRequestException,
  Inject,
  Injectable,
  NotFoundException,
} from "@nestjs/common";
import { createHash } from "crypto";
import { Pool } from "pg";
import { PG_POOL } from "../database/pg-pool.token";
import { withTenant, UUID_RE } from "../approvals/tenant-db";
import { findingsFor, Finding, ReviewSnapshot } from "./findings";

export type ReviewOutcome = "no_changes" | "changes_made" | "changes_required";
export const OUTCOMES: readonly ReviewOutcome[] = [
  "no_changes",
  "changes_made",
  "changes_required",
];

export interface AccessReview {
  reviewId: string;
  reviewedBy: string;
  reviewerEmail: string;
  reviewerRole: string;
  verifiedBy: "totp" | "sso";
  outcome: ReviewOutcome;
  notes: string | null;
  findings: Finding[];
  snapshot?: ReviewSnapshot;
  snapshotSha256: string;
  createdAt: string;
}

const iso = (v: unknown) => (v ? new Date(v as string).toISOString() : null);

// Canonical JSON (sorted keys) so the digest of a snapshot is the same wherever it is computed.
export function canonical(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`;
  if (v && typeof v === "object") {
    return `{${Object.keys(v as object)
      .sort()
      .map(
        (k) =>
          `${JSON.stringify(k)}:${canonical((v as Record<string, unknown>)[k])}`,
      )
      .join(",")}}`;
  }
  return JSON.stringify(v);
}

function toReview(
  row: Record<string, any>,
  withSnapshot: boolean,
): AccessReview {
  return {
    reviewId: row.review_id,
    reviewedBy: row.reviewed_by,
    reviewerEmail: row.reviewer_email,
    reviewerRole: row.reviewer_role,
    verifiedBy: row.verified_by,
    outcome: row.outcome,
    notes: row.notes,
    findings: row.findings,
    ...(withSnapshot ? { snapshot: row.snapshot } : {}),
    snapshotSha256: row.snapshot_sha256,
    createdAt: new Date(row.created_at).toISOString(),
  };
}

// Who can do what in an organisation, read in one transaction so the picture is consistent,
// and the record of each time a person reviewed it.
@Injectable()
export class AccessReviewService {
  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  async snapshot(
    customerId: string,
  ): Promise<{ snapshot: ReviewSnapshot; findings: Finding[] }> {
    const snapshot = await withTenant(this.pool, customerId, async (c) => {
      const org = await c.query(
        `SELECT customer_id, name, created_at, api_key_rotated_at, api_key_expires_at FROM customers WHERE customer_id = $1`,
        [customerId],
      );
      if (!org.rows[0]) throw new NotFoundException("no such organisation");
      // One client, so one query at a time: pg queues them anyway, and newer pg warns on overlap.
      const members = await c.query(
        `SELECT m.user_id, u.email, u.full_name, m.role, u.status, u.auth_provider, u.mfa_enabled, u.last_login_at, m.created_at
             FROM user_customer_roles m JOIN users u ON u.id = m.user_id
            WHERE m.customer_id = $1 ORDER BY m.role, u.email`,
        [customerId],
      );
      const agents = await c.query(
        `SELECT a.agent_id, a.name, a.status, a.allowed_tokens, a.allowed_recipients, a.per_transfer_limit_zar,
                  a.daily_limit_zar, a.expires_at, a.created_at, u.email AS created_by
             FROM agents a LEFT JOIN users u ON u.id = a.created_by
            WHERE a.customer_id = $1 ORDER BY a.status, a.created_at DESC`,
        [customerId],
      );
      const prev = await c.query(
        `SELECT review_id, created_at, reviewer_email, outcome FROM access_reviews
            WHERE customer_id = $1 ORDER BY created_at DESC LIMIT 1`,
        [customerId],
      );
      const o = org.rows[0];
      const s: ReviewSnapshot = {
        takenAt: new Date().toISOString(),
        organisation: { customerId: o.customer_id, name: o.name },
        members: members.rows.map((m) => ({
          userId: m.user_id,
          email: m.email,
          fullName: m.full_name,
          role: m.role,
          status: m.status,
          authProvider: m.auth_provider,
          mfaEnabled: !!m.mfa_enabled,
          lastLoginAt: iso(m.last_login_at),
          memberSince: iso(m.created_at)!,
        })),
        agents: agents.rows.map((a) => ({
          agentId: a.agent_id,
          name: a.name,
          status: a.status,
          allowedTokens: a.allowed_tokens,
          allowedRecipients: a.allowed_recipients
            ? a.allowed_recipients.length
            : null,
          perTransferLimitZar: Number(a.per_transfer_limit_zar),
          dailyLimitZar: Number(a.daily_limit_zar),
          expiresAt: iso(a.expires_at)!,
          createdBy: a.created_by ?? "unknown",
          createdAt: iso(a.created_at)!,
        })),
        apiKey: {
          createdAt: iso(o.created_at)!,
          rotatedAt: iso(o.api_key_rotated_at),
          expiresAt: iso(o.api_key_expires_at),
        },
        previousReview: prev.rows[0]
          ? {
              reviewId: prev.rows[0].review_id,
              at: iso(prev.rows[0].created_at)!,
              by: prev.rows[0].reviewer_email,
              outcome: prev.rows[0].outcome,
            }
          : null,
      };
      return s;
    });
    return { snapshot, findings: findingsFor(snapshot) };
  }

  // The snapshot is taken here, at the moment of signing, never accepted from the caller.
  async record(
    customerId: string,
    reviewer: {
      userId: string;
      email: string;
      role: string;
      verifiedBy: "totp" | "sso";
    },
    input: { outcome: ReviewOutcome; notes?: string },
  ): Promise<AccessReview> {
    if (!OUTCOMES.includes(input.outcome))
      throw new BadRequestException(
        `outcome must be one of ${OUTCOMES.join(", ")}`,
      );
    if (input.outcome !== "no_changes" && !input.notes?.trim()) {
      throw new BadRequestException(
        "say what was changed, or what needs changing, in notes",
      );
    }
    const { snapshot, findings } = await this.snapshot(customerId);
    const digest = createHash("sha256")
      .update(canonical(snapshot))
      .digest("hex");
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `INSERT INTO access_reviews (customer_id, reviewed_by, reviewer_email, reviewer_role, verified_by, outcome, notes, snapshot, findings, snapshot_sha256)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING *`,
        [
          customerId,
          reviewer.userId,
          reviewer.email,
          reviewer.role,
          reviewer.verifiedBy,
          input.outcome,
          input.notes?.trim() || null,
          JSON.stringify(snapshot),
          JSON.stringify(findings),
          digest,
        ],
      ),
    );
    return toReview(r.rows[0], true);
  }

  async list(customerId: string, limit = 20): Promise<AccessReview[]> {
    const n = Math.min(Math.max(Math.trunc(limit) || 20, 1), 100);
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT * FROM access_reviews WHERE customer_id = $1 ORDER BY created_at DESC LIMIT $2`,
        [customerId, n],
      ),
    );
    return r.rows.map((row) => toReview(row, false));
  }

  async get(customerId: string, reviewId: string): Promise<AccessReview> {
    if (!UUID_RE.test(reviewId)) throw new NotFoundException("no such review");
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT * FROM access_reviews WHERE customer_id = $1 AND review_id = $2`,
        [customerId, reviewId],
      ),
    );
    if (!r.rows[0]) throw new NotFoundException("no such review");
    return toReview(r.rows[0], true);
  }
}
