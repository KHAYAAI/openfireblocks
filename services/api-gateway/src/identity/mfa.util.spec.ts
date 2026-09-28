import { authenticator } from 'otplib';
import {
  generateMfaSecret,
  verifyTotpCode,
  mfaEnrollmentUri,
  hashChallengeToken,
} from './mfa.util';

describe('mfa.util', () => {
  it('generates a secret a real authenticator app can enroll', () => {
    const secret = generateMfaSecret();
    expect(secret).toMatch(/^[A-Z2-7]+$/); // base32
    expect(secret.length).toBeGreaterThanOrEqual(16);
  });

  it('accepts a code generated from the matching secret', () => {
    const secret = generateMfaSecret();
    const code = authenticator.generate(secret);
    expect(verifyTotpCode(secret, code)).toBe(true);
  });

  it('rejects a code generated from a different secret', () => {
    const secret = generateMfaSecret();
    const otherCode = authenticator.generate(generateMfaSecret());
    expect(verifyTotpCode(secret, otherCode)).toBe(false);
  });

  it('rejects malformed codes without throwing', () => {
    const secret = generateMfaSecret();
    expect(verifyTotpCode(secret, 'abcdef')).toBe(false);
    expect(verifyTotpCode(secret, '123')).toBe(false);
    expect(verifyTotpCode(secret, '')).toBe(false);
  });

  it('builds a standard otpauth:// enrollment URI', () => {
    const uri = mfaEnrollmentUri('alice@example.com', 'FFBQ42ZIEQEA6AZT');
    expect(uri).toMatch(/^otpauth:\/\/totp\//);
    expect(uri).toContain('OpenFireblocks');
    expect(uri).toContain('alice%40example.com');
  });

  it('hashes challenge tokens deterministically', () => {
    const a = hashChallengeToken('same-token');
    const b = hashChallengeToken('same-token');
    const c = hashChallengeToken('different-token');
    expect(a.equals(b)).toBe(true);
    expect(a.equals(c)).toBe(false);
  });
});
