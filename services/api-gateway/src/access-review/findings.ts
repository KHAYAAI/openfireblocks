// What an access reviewer should look at first. Pure: the same snapshot always gives the same
// findings, so the findings stored with a review are reproducible from its snapshot.

export interface ReviewMember {
  userId: string;
  email: string;
  fullName: string;
  role: string;
  status: string;
  authProvider: string;
  mfaEnabled: boolean;
  lastLoginAt: string | null;
  memberSince: string;
}

export interface ReviewAgent {
  agentId: string;
  name: string;
  status: string;
  allowedTokens: string[];
  allowedRecipients: number | null; // count; null means any recipient
  perTransferLimitZar: number;
  dailyLimitZar: number;
  expiresAt: string;
  createdBy: string;
  createdAt: string;
}

export interface ReviewSnapshot {
  takenAt: string;
  organisation: { customerId: string; name: string };
  members: ReviewMember[];
  agents: ReviewAgent[];
  apiKey: {
    createdAt: string;
    rotatedAt: string | null;
    expiresAt: string | null;
  };
  previousReview: {
    reviewId: string;
    at: string;
    by: string;
    outcome: string;
  } | null;
}

export interface Finding {
  code: string;
  severity: "high" | "medium" | "low" | "info";
  subject: string;
  detail: string;
}

const DAY = 24 * 60 * 60 * 1000;
export const STALE_SIGN_IN_DAYS = 90;
export const API_KEY_MAX_AGE_DAYS = 90;
export const REVIEW_INTERVAL_DAYS = 92; // quarterly, with a little slack
const LONG_AGENT_DAYS = 90;
const PRIVILEGED = new Set(["admin", "approver"]);

const days = (from: string, now: number) =>
  Math.floor((now - new Date(from).getTime()) / DAY);

export function findingsFor(
  s: ReviewSnapshot,
  now = new Date(s.takenAt).getTime(),
): Finding[] {
  const out: Finding[] = [];

  if (!s.previousReview) {
    out.push({
      code: "first_review",
      severity: "info",
      subject: "organisation",
      detail: "No access review has been recorded before.",
    });
  } else if (days(s.previousReview.at, now) > REVIEW_INTERVAL_DAYS) {
    out.push({
      code: "review_overdue",
      severity: "medium",
      subject: "organisation",
      detail: `The last review was ${days(s.previousReview.at, now)} days ago; reviews are due quarterly.`,
    });
  }

  for (const m of s.members) {
    if (m.status !== "active") {
      out.push({
        code: "inactive_account_has_role",
        severity: "high",
        subject: m.email,
        detail: `The account is ${m.status} but still holds the ${m.role} role. Remove the role.`,
      });
    }
    if (m.authProvider === "password" && !m.mfaEnabled) {
      out.push({
        code: "no_mfa",
        severity: PRIVILEGED.has(m.role) ? "high" : "medium",
        subject: m.email,
        detail: `${m.role} signs in with a password and has no second factor.${PRIVILEGED.has(m.role) ? " They cannot approve transfers until it is on." : ""}`,
      });
    }
    if (!m.lastLoginAt) {
      out.push({
        code: "never_signed_in",
        severity: "medium",
        subject: m.email,
        detail: `Holds the ${m.role} role and has never signed in. Confirm the access is still needed.`,
      });
    } else if (days(m.lastLoginAt, now) > STALE_SIGN_IN_DAYS) {
      out.push({
        code: "stale_sign_in",
        severity: "medium",
        subject: m.email,
        detail: `Last signed in ${days(m.lastLoginAt, now)} days ago while holding the ${m.role} role. Confirm the access is still needed.`,
      });
    }
  }

  const admins = s.members.filter((m) => m.role === "admin").length;
  if (admins === 1) {
    out.push({
      code: "single_admin",
      severity: "low",
      subject: "organisation",
      detail:
        "Only one admin. If that person is unavailable, nobody can lift a freeze or change members.",
    });
  } else if (admins > 3) {
    out.push({
      code: "many_admins",
      severity: "medium",
      subject: "organisation",
      detail: `${admins} admins. Admin is the most powerful role; keep it to the few who need it.`,
    });
  }
  if (!s.members.some((m) => m.role === "auditor")) {
    out.push({
      code: "no_auditor",
      severity: "info",
      subject: "organisation",
      detail:
        "No one holds the read-only auditor role. An independent reviewer usually should.",
    });
  }

  for (const a of s.agents.filter((x) => x.status === "active")) {
    if (new Date(a.expiresAt).getTime() <= now) {
      out.push({
        code: "agent_expired_not_revoked",
        severity: "low",
        subject: a.name,
        detail:
          "Expired, so it can no longer pay, but it was never revoked. Revoke it to close the record.",
      });
      continue;
    }
    if (a.allowedRecipients === null) {
      out.push({
        code: "agent_any_recipient",
        severity: "medium",
        subject: a.name,
        detail:
          "May pay any address. Agents reachable by untrusted text should have a recipient list.",
      });
    }
    if (days(a.createdAt, new Date(a.expiresAt).getTime()) > LONG_AGENT_DAYS) {
      out.push({
        code: "agent_long_lived",
        severity: "low",
        subject: a.name,
        detail: `Valid for more than ${LONG_AGENT_DAYS} days. Shorter expiry limits the damage of a leaked key.`,
      });
    }
  }

  if (!s.apiKey.expiresAt) {
    out.push({
      code: "api_key_no_expiry",
      severity: "medium",
      subject: "API key",
      detail:
        "The organisation API key never expires. Rotate it with an expiry (POST /api-key/rotate, API_KEY_TTL_DAYS).",
    });
  }
  const keyAge = days(s.apiKey.rotatedAt ?? s.apiKey.createdAt, now);
  if (keyAge > API_KEY_MAX_AGE_DAYS) {
    out.push({
      code: "api_key_old",
      severity: "medium",
      subject: "API key",
      detail: `Not rotated for ${keyAge} days.`,
    });
  }

  const rank = { high: 0, medium: 1, low: 2, info: 3 } as const;
  return out.sort((x, y) => rank[x.severity] - rank[y.severity]);
}
