import { createHmac, randomBytes, timingSafeEqual } from 'crypto';

// Signed session cookies for the dashboard.
//
// The dashboard exists because a compliance officer will not use curl, and
// the people who need it most are exactly the people who should not be
// handling a long-lived API key. So the key is exchanged once, at sign-in,
// for a cookie that carries only a customer id and an expiry, signed with
// a server-side secret.
//
// Deliberately not a JWT. A JWT would mean a library, an algorithm field
// an attacker can set to "none", and a set of confused-deputy problems
// that exist entirely because the format is general. This needs to carry
// one identifier and an expiry between this process and a browser it
// issued the cookie to, and an HMAC does that in thirty lines that can be
// read in full.
//
// What it is not: an authorisation system. The cookie says which customer
// is signed in; every route still goes through the same tenant scoping as
// the API, so a forged customer id would buy nothing that a stolen API key
// would not.

const COOKIE_NAME = 'ofb_session';

// Eight hours. Long enough for a working day, short enough that a laptop
// left open is not a standing grant.
const SESSION_TTL_MS = 8 * 60 * 60 * 1000;

// The signing secret.
//
// Generated at boot when unset, which means sessions do not survive a
// restart. That is the right default for a secret with no configured
// value: the alternative is a hard-coded fallback, which is the same as no
// signature at all the moment anyone reads the source. A real deployment
// sets DASHBOARD_SESSION_SECRET and gets sessions that survive a rolling
// update.
let secret: Buffer | null = null;

function sessionSecret(): Buffer {
  if (secret) {
    return secret;
  }
  const configured = process.env.DASHBOARD_SESSION_SECRET;
  if (configured && configured.length >= 32) {
    secret = Buffer.from(configured, 'utf8');
  } else {
    secret = randomBytes(32);
  }
  return secret;
}

export interface Session {
  customerId: string;
  expiresAt: number;
}

function sign(payload: string): string {
  return createHmac('sha256', sessionSecret()).update(payload).digest('base64url');
}

// issue returns the Set-Cookie value for a signed-in customer.
export function issueSession(customerId: string): { cookie: string; expiresAt: number } {
  const expiresAt = Date.now() + SESSION_TTL_MS;
  const payload = `${customerId}.${expiresAt}`;
  const value = `${payload}.${sign(payload)}`;

  // HttpOnly: the cookie is never read by page script, so script injected
  // into a page cannot exfiltrate it.
  // SameSite=Strict: the dashboard performs state-changing actions, and a
  // cross-site form post must not carry the session.
  // Secure unless explicitly running without TLS locally.
  const secureFlag = process.env.DASHBOARD_INSECURE_COOKIES === '1' ? '' : ' Secure;';
  const cookie =
    `${COOKIE_NAME}=${value}; Path=/dashboard; HttpOnly;${secureFlag} ` +
    `SameSite=Strict; Max-Age=${Math.floor(SESSION_TTL_MS / 1000)}`;
  return { cookie, expiresAt };
}

export function clearSessionCookie(): string {
  return `${COOKIE_NAME}=; Path=/dashboard; HttpOnly; SameSite=Strict; Max-Age=0`;
}

// verify reads a session out of a Cookie header, or returns null.
//
// Every failure path returns null rather than throwing or distinguishing
// itself: a caller learning *why* a cookie was rejected learns something
// about the secret.
export function readSession(cookieHeader: string | undefined): Session | null {
  if (!cookieHeader) {
    return null;
  }
  const raw = cookieHeader
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith(`${COOKIE_NAME}=`));
  if (!raw) {
    return null;
  }

  const value = raw.slice(COOKIE_NAME.length + 1);
  const lastDot = value.lastIndexOf('.');
  if (lastDot < 0) {
    return null;
  }
  const payload = value.slice(0, lastDot);
  const provided = value.slice(lastDot + 1);

  // Constant time. A byte-by-byte comparison that returns early leaks how
  // much of a forged signature was correct, which is enough to construct
  // one a byte at a time.
  const expected = Buffer.from(sign(payload), 'utf8');
  const got = Buffer.from(provided, 'utf8');
  if (expected.length !== got.length || !timingSafeEqual(expected, got)) {
    return null;
  }

  const [customerId, expiry] = payload.split('.');
  const expiresAt = Number(expiry);
  if (!customerId || !Number.isFinite(expiresAt) || expiresAt < Date.now()) {
    return null;
  }
  return { customerId, expiresAt };
}
