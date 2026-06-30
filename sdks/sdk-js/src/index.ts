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

export interface PreparedTx {
  from: string;
  chainId: number;
  nonce: number;
  gasLimit: number;
  maxFeePerGas?: string;
  maxPriorityFeePerGas?: string;
  gasPrice?: string;
}

export interface SettlementStatus {
  workflowId: string;
  status: string;
  result?: unknown;
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

  // Suggest nonce/gas/fees for building a sign request (requires server RPC).
  prepare(params: {
    to: string;
    data?: string;
    value?: string;
  }): Promise<PreparedTx> {
    const q = new URLSearchParams({ to: params.to });
    if (params.data) q.set('data', params.data);
    if (params.value) q.set('value', params.value);
    return this.request<PreparedTx>('GET', `/prepare?${q.toString()}`);
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

  // --- Durable settlements (Temporal-backed) ---

  // Start a durable settlement workflow. Returns its workflow id.
  startSettlement(req: SignRequest): Promise<{ workflowId: string }> {
    return this.request<{ workflowId: string }>('POST', '/settlements', req);
  }

  // Get a settlement's status (and result once COMPLETED).
  getSettlement(workflowId: string): Promise<SettlementStatus> {
    return this.request<SettlementStatus>(
      'GET',
      `/settlements/${encodeURIComponent(workflowId)}`,
    );
  }

  // Approve or reject a high-value settlement awaiting manual approval.
  approveSettlement(
    workflowId: string,
    approved: boolean,
  ): Promise<{ workflowId: string; approved: boolean }> {
    return this.request('POST', `/settlements/${encodeURIComponent(workflowId)}/approve`, {
      approved,
    });
  }
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}
