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

  // One 30-second step of drift either way (RFC 6238 section 5.2), and no more.
  describe('clock drift', () => {
    const STEP = 30_000;
    const codeAt = (secret: string, offsetSteps: number) =>
      authenticator.clone({ epoch: Date.now() + offsetSteps * STEP }).generate(secret);

    it('accepts a code from the previous and the next step', () => {
      const secret = generateMfaSecret();
      expect(verifyTotpCode(secret, codeAt(secret, -1))).toBe(true);
      expect(verifyTotpCode(secret, codeAt(secret, 1))).toBe(true);
    });

    it('refuses a code two steps away', () => {
      const secret = generateMfaSecret();
      const stale = codeAt(secret, -2);
      const early = codeAt(secret, 2);
      // A code two steps away can collide with the current one by chance
      // (one in a million); skip the assertion in that case.
      const current = [codeAt(secret, -1), codeAt(secret, 0), codeAt(secret, 1)];
      if (!current.includes(stale)) expect(verifyTotpCode(secret, stale)).toBe(false);
      if (!current.includes(early)) expect(verifyTotpCode(secret, early)).toBe(false);
    });

    it('leaves the shared authenticator without a window', () => {
      expect(authenticator.options.window ?? 0).toBe(0);
    });
  });

  it('rejects malformed codes without throwing', () => {
    const secret = generateMfaSecret();
    expect(verifyTotpCode(secret, 'abcdef')).toBe(false);
    expect(verifyTotpCode(secret, '123')).toBe(false);
    expect(verifyTotpCode(secret, '')).toBe(false);
    expect(verifyTotpCode(secret, '1234567')).toBe(false);
    expect(verifyTotpCode(secret, ' 12345')).toBe(false);
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
