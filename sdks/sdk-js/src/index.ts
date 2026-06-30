// OpenFireblocks JavaScript/TypeScript SDK.
//
// Minimal, dependency-free client over the api-gateway REST API. Uses the global
// `fetch` (Node 18+ / browsers).

export interface SignRequest {
  chainId: number;
  to: string;
  data?: string;
  value?: string; // wei, base-10 string
  gasLimit: number;
  gasPrice: string; // wei per gas, base-10 string
  nonce: number;
  country?: string;
}

export interface SignResult {
  requestId: string;
  signedTx: string;
  txHash: string;
  from: string;
  status: 'signed' | 'broadcasted';
  broadcasted: boolean;
}

export interface TransactionRecord {
  request_id: string;
  customer_id: string;
  chain: string;
  to_address: string;
  status: string;
  tx_hash: string | null;
  [key: string]: unknown;
}

export interface AuditEvent {
  id: number;
  event_type: string;
  status: string;
  request_id: string;
  [key: string]: unknown;
}

// Error thrown for any non-2xx response, carrying the parsed body when available.
export class OpenFireblocksError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly body?: unknown,
  ) {
    super(message);
    this.name = 'OpenFireblocksError';
  }
}

export interface ClientOptions {
  baseUrl: string;
  apiKey: string;
  // Injectable fetch for testing / custom runtimes; defaults to global fetch.
  fetch?: typeof fetch;
}

// Tenant-facing client. Authenticates with a customer API key.
export class OpenFireblocksClient {
  private readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly fetchImpl: typeof fetch;

  constructor(opts: ClientOptions) {
    this.baseUrl = opts.baseUrl.replace(/\/$/, '');
    this.apiKey = opts.apiKey;
    const f = opts.fetch ?? globalThis.fetch;
    if (!f) {
      throw new Error('no fetch implementation available; pass opts.fetch');
    }
    this.fetchImpl = f;
  }

  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
  ): Promise<T> {
    const res = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method,
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${this.apiKey}`,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });

    const text = await res.text();
    const parsed = text ? safeJson(text) : undefined;
    if (!res.ok) {
      throw new OpenFireblocksError(
        `request ${method} ${path} failed with ${res.status}`,
        res.status,
        parsed,
      );
    }
    return parsed as T;
  }

  // Sign (and optionally broadcast) a transaction.
  sign(req: SignRequest): Promise<SignResult> {
    return this.request<SignResult>('POST', '/sign', req);
  }

  listTransactions(): Promise<TransactionRecord[]> {
    return this.request<TransactionRecord[]>('GET', '/transactions');
  }

  getTransaction(requestId: string): Promise<TransactionRecord> {
    return this.request<TransactionRecord>(
      'GET',
      `/transactions/${encodeURIComponent(requestId)}`,
    );
  }

  getAuditTrail(requestId: string): Promise<AuditEvent[]> {
    return this.request<AuditEvent[]>(
      'GET',
      `/transactions/${encodeURIComponent(requestId)}/audit`,
    );
  }
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}
