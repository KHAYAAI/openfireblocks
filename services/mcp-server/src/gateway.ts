// A small client for the agent's own routes on the FORGE CUSTODY gateway.
//
// It holds one credential: an agent key (ofb_agent_...), which an organisation admin
// created with a token list, an optional recipient list, a per-transfer and a daily limit,
// and an expiry. The key cannot create keys, approve anything, change its own limits or
// reach any customer route. Everything this server can do, the gateway already allows
// this key to do; nothing here widens it.

export class GatewayError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly body: unknown,
  ) {
    super(message);
  }
}

export interface GatewayClient {
  get(path: string): Promise<any>;
  post(path: string, body: unknown): Promise<any>;
}

function messageOf(body: unknown, status: number): string {
  if (body && typeof body === 'object') {
    const m = (body as { message?: unknown }).message;
    if (typeof m === 'string') return m;
    if (Array.isArray(m)) return m.join('; ');
    const e = (body as { error?: unknown }).error;
    if (typeof e === 'string') return e;
  }
  if (typeof body === 'string' && body) return body.slice(0, 500);
  return `the gateway answered ${status}`;
}

export function gatewayClient(baseUrl: string, agentKey: string, timeoutMs = 120_000): GatewayClient {
  const base = baseUrl.replace(/\/+$/, '');
  async function call(method: 'GET' | 'POST', path: string, body?: unknown) {
    const res = await fetch(`${base}${path}`, {
      method,
      headers: {
        authorization: `Bearer ${agentKey}`,
        accept: 'application/json',
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      // A payment runs a threshold signing ceremony; give it time, but not forever.
      signal: AbortSignal.timeout(timeoutMs),
    });
    const text = await res.text();
    let parsed: unknown = text;
    try {
      parsed = text ? JSON.parse(text) : null;
    } catch {
      /* not JSON; keep the text */
    }
    if (!res.ok) throw new GatewayError(res.status, messageOf(parsed, res.status), parsed);
    return parsed;
  }
  return {
    get: (path) => call('GET', path),
    post: (path, body) => call('POST', path, body),
  };
}
