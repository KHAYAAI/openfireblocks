import { authenticator } from 'otplib';
import { createHash, randomBytes } from 'crypto';

// TOTP (RFC 6238) secret generation, code verification, and QR-enrollment URI,
// backed by otplib rather than a hand-rolled implementation.
export function generateMfaSecret(): string {
  return authenticator.generateSecret();
}

export function verifyTotpCode(secret: string, code: string): boolean {
  if (!/^\d{6}$/.test(code)) return false;
  try {
    return authenticator.check(code, secret);
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
