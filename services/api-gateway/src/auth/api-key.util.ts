import { createHash, randomBytes, timingSafeEqual } from 'crypto';

// API keys are stored only as SHA-256 hashes; the plaintext is shown to the
// tenant exactly once at creation. A leaked database therefore does not expose
// usable credentials.

const KEY_PREFIX = 'ofb_';

export function generateApiKey(): string {
  return KEY_PREFIX + randomBytes(24).toString('hex');
}

export function hashApiKey(apiKey: string): string {
  return createHash('sha256').update(apiKey).digest('hex');
}

// Constant-time comparison of two hex-encoded hashes.
export function hashesEqual(a: string, b: string): boolean {
  const ba = Buffer.from(a, 'hex');
  const bb = Buffer.from(b, 'hex');
  if (ba.length !== bb.length || ba.length === 0) return false;
  return timingSafeEqual(ba, bb);
}
