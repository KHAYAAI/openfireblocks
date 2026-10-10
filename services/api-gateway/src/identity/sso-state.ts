import { createHmac, randomBytes, timingSafeEqual } from 'crypto';
import { UnauthorizedException } from '@nestjs/common';

// The OAuth/OIDC `state` parameter, shared by every SSO path.
//
// It is the CSRF defence: without it an attacker can feed a victim's browser
// their own authorization code and silently log the victim into the
// attacker's account. Signed with JWT_SECRET (already required in production)
// and time-boxed, so it cannot be forged or replayed indefinitely and no
// server-side session store is needed.

export const STATE_TTL_MS = 10 * 60 * 1000;

function secret(): string {
  const s = process.env.JWT_SECRET;
  if (!s) {
    if (process.env.NODE_ENV === 'production') throw new Error('JWT_SECRET must be set in production');
    return 'dev-only-insecure-jwt-secret-do-not-use-in-production';
  }
  return s;
}

const mac = (payload: string) => createHmac('sha256', secret()).update(payload).digest('hex');

export function createState(now = Date.now()): string {
  const payload = `${randomBytes(16).toString('hex')}.${now}`;
  return `${payload}.${mac(payload)}`;
}

export function verifyState(state: string | undefined, now = Date.now()): string {
  if (!state) throw new UnauthorizedException('missing SSO state');
  const parts = state.split('.');
  if (parts.length !== 3) throw new UnauthorizedException('malformed SSO state');
  const [nonce, issuedAtRaw, given] = parts;
  const want = Buffer.from(mac(`${nonce}.${issuedAtRaw}`), 'hex');
  const got = Buffer.from(given, 'hex');
  if (got.length !== want.length || !timingSafeEqual(got, want)) throw new UnauthorizedException('invalid SSO state');
  const issuedAt = Number(issuedAtRaw);
  if (!Number.isFinite(issuedAt) || now - issuedAt > STATE_TTL_MS) throw new UnauthorizedException('expired SSO state');
  return nonce;
}

// Values the browser never sees but both legs of the login must agree on,
// derived from the signed state instead of stored: the PKCE verifier (proves
// the code exchange comes from whoever started the login) and the OIDC nonce
// (binds the ID token to this login). Derived with a purpose label so one
// cannot be substituted for the other.
export function derive(purpose: 'pkce' | 'nonce', state: string): string {
  return createHmac('sha256', secret()).update(`${purpose}:${state}`).digest('base64url');
}
