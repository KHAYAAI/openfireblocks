import { findingsFor, ReviewMember, ReviewSnapshot } from './findings';

const NOW = Date.parse('2026-10-11T12:00:00Z');
const daysAgo = (n: number) => new Date(NOW - n * 86_400_000).toISOString();
const inDays = (n: number) => new Date(NOW + n * 86_400_000).toISOString();

const member = (over: Partial<ReviewMember>): ReviewMember => ({
  userId: 'u', email: 'x@example.test', fullName: 'X', role: 'viewer', status: 'active',
  authProvider: 'password', mfaEnabled: true, lastLoginAt: daysAgo(1), memberSince: daysAgo(200), ...over,
});

// A well-run organisation: two admins, an auditor, everyone on MFA and recently active,
// a recent review, a fresh API key with an expiry, one tightly scoped agent.
function clean(): ReviewSnapshot {
  return {
    takenAt: new Date(NOW).toISOString(),
    organisation: { customerId: 'c', name: 'Org' },
    members: [
      member({ email: 'a1@example.test', role: 'admin' }),
      member({ email: 'a2@example.test', role: 'admin', authProvider: 'workos_sso', mfaEnabled: false }),
      member({ email: 'au@example.test', role: 'auditor' }),
    ],
    agents: [{
      agentId: 'g', name: 'payroll-bot', status: 'active', allowedTokens: ['tZAR'], allowedRecipients: 3,
      perTransferLimitZar: 5000, dailyLimitZar: 20000, expiresAt: inDays(30), createdBy: 'a1@example.test', createdAt: daysAgo(10),
    }],
    apiKey: { createdAt: daysAgo(400), rotatedAt: daysAgo(10), expiresAt: inDays(80) },
    previousReview: { reviewId: 'r', at: daysAgo(30), by: 'au@example.test', outcome: 'no_changes' },
  };
}
const codes = (s: ReviewSnapshot) => findingsFor(s, NOW).map((f) => f.code);

describe('access review findings', () => {
  it('finds nothing in a well-run organisation (SSO users are not flagged for MFA)', () => {
    expect(findingsFor(clean(), NOW)).toEqual([]);
  });

  it('notes a first review and an overdue one', () => {
    const s = clean(); s.previousReview = null;
    expect(codes(s)).toEqual(['first_review']);
    s.previousReview = { reviewId: 'r', at: daysAgo(120), by: 'x', outcome: 'no_changes' };
    expect(codes(s)).toEqual(['review_overdue']);
  });

  it('flags a disabled account that still holds a role, and missing MFA by privilege', () => {
    const s = clean();
    s.members.push(member({ email: 'gone@example.test', role: 'operator', status: 'disabled' }));
    s.members.push(member({ email: 'appr@example.test', role: 'approver', mfaEnabled: false }));
    s.members.push(member({ email: 'view@example.test', role: 'viewer', mfaEnabled: false }));
    const f = findingsFor(s, NOW);
    expect(f.find((x) => x.code === 'inactive_account_has_role')).toMatchObject({ severity: 'high', subject: 'gone@example.test' });
    expect(f.find((x) => x.code === 'no_mfa' && x.subject === 'appr@example.test')!.severity).toBe('high');
    expect(f.find((x) => x.code === 'no_mfa' && x.subject === 'view@example.test')!.severity).toBe('medium');
    // Most severe first.
    expect(f[0].severity).toBe('high');
  });

  it('flags people who never signed in or have not for a quarter', () => {
    const s = clean();
    s.members.push(member({ email: 'new@example.test', lastLoginAt: null }));
    s.members.push(member({ email: 'old@example.test', lastLoginAt: daysAgo(91) }));
    expect(codes(s).sort()).toEqual(['never_signed_in', 'stale_sign_in']);
  });

  it('flags one admin, too many admins and no auditor', () => {
    const s = clean(); s.members = [member({ role: 'admin' })];
    expect(codes(s).sort()).toEqual(['no_auditor', 'single_admin']);
    s.members = [1, 2, 3, 4].map((i) => member({ email: `a${i}@x.test`, role: 'admin' })).concat(member({ role: 'auditor' }));
    expect(codes(s)).toEqual(['many_admins']);
  });

  it('flags agents that pay anyone, live too long, or expired without being revoked; ignores revoked ones', () => {
    const s = clean();
    const base = s.agents[0];
    s.agents = [
      { ...base, name: 'open', allowedRecipients: null },
      { ...base, name: 'forever', createdAt: daysAgo(1), expiresAt: inDays(200) },
      { ...base, name: 'lapsed', expiresAt: daysAgo(1), allowedRecipients: null },
      { ...base, name: 'revoked', status: 'revoked', allowedRecipients: null, expiresAt: inDays(500) },
    ];
    const f = findingsFor(s, NOW).map((x) => `${x.code}:${x.subject}`).sort();
    expect(f).toEqual(['agent_any_recipient:open', 'agent_expired_not_revoked:lapsed', 'agent_long_lived:forever']);
  });

  it('flags an API key without expiry or not rotated for a quarter', () => {
    const s = clean(); s.apiKey = { createdAt: daysAgo(100), rotatedAt: null, expiresAt: null };
    expect(codes(s).sort()).toEqual(['api_key_no_expiry', 'api_key_old']);
  });

  it('is deterministic for a given snapshot (the stored findings can be recomputed)', () => {
    const s = clean(); s.previousReview = null;
    expect(findingsFor(s)).toEqual(findingsFor(JSON.parse(JSON.stringify(s))));
  });
});
