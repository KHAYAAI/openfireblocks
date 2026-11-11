import { authenticator } from 'otplib';
import { createHash, randomBytes } from 'crypto';

// TOTP (RFC 6238) secret generation, code verification, and QR-enrollment URI,
// backed by otplib rather than a hand-rolled implementation.
export function generateMfaSecret(): string {
  return authenticator.generateSecret();
}

// One 30-second step of clock drift either way, as RFC 6238 section 5.2
// recommends. With no window (otplib's default), a code generated in the last
// moment of its step is refused a moment later, once the server is in the next
// step: users typing a code near a boundary were turned away, and the
// approvals live spec failed intermittently with 401 for the same reason. A
// clone, so the global authenticator other code may use keeps its options.
//
// Replay: a code is accepted for up to 90 seconds instead of 30, and nothing
// records codes already used, so a code seen over someone's shoulder can be
// replayed within that window, as it could within 30 seconds before. Every
// caller pairs the code with something the attacker must also hold: the
// password-checked MFA challenge at login, or the user's own bearer session
// for step-up (approvals, access reviews, MFA changes). Refusing reuse
// (recording the last accepted step per user) would also refuse an approver
// deciding two transfers inside one step with the same code, which the
// approval flow does; that trade-off is left for a separate change.
const totp = authenticator.clone({ window: 1 });

export function verifyTotpCode(secret: string, code: string): boolean {
  if (!/^\d{6}$/.test(code)) return false;
  try {
    return totp.check(code, secret);
  } catch {
    return false;
  }
}

export function mfaEnrollmentUri(email: string, secret: string): string {
  return authenticator.keyuri(email, 'OpenFireblocks', secret);
}

// After password verification for an MFA-enabled account, the server issues a
// short-lived opaque challenge token instead of a JWT. The client must present
// this token plus a valid TOTP code to complete login. Only the SHA-256 hash
// is persisted (mirrors api-key.util.ts) so a leaked mfa_challenges row alone
// cannot be replayed.
const CHALLENGE_TTL_MINUTES = 10;

export function generateChallengeToken(): string {
  return randomBytes(32).toString('hex');
}

export function hashChallengeToken(token: string): Buffer {
  return createHash('sha256').update(token).digest();
}

export function challengeExpiry(): Date {
  return new Date(Date.now() + CHALLENGE_TTL_MINUTES * 60_000);
}
