// What this platform needs from another custodian, and the one adapter that speaks
// to a custodian over HTTP. Vendor-specific APIs (a bank custodian, an exchange) are
// put behind this contract by a thin connector the customer or integrator runs; the
// contract is documented in docs/deployment/MULTI-CUSTODIAN.md.
//
// Everything a connector returns is untrusted input: it is validated to the shape
// below and anything else is an error, because a malformed balance that is believed is
// worse than one that is not shown.

export interface ExternalAccount { id: string; name: string; blockchain: string; address?: string }
export interface ExternalBalance { asset: string; amount: string; decimals: number }
export type ExternalStatus = 'pending' | 'completed' | 'failed';
export interface ExternalTransfer { id: string; status: ExternalStatus; txHash?: string; error?: string }
export interface TransferRequest { accountId: string; destination: string; asset: string; amount: string; memo?: string; idempotencyKey: string }

export interface CustodianAdapter {
  listAccounts(): Promise<ExternalAccount[]>;
  getBalances(accountId: string): Promise<ExternalBalance[]>;
  initiateTransfer(req: TransferRequest): Promise<ExternalTransfer>;
  getTransfer(id: string): Promise<ExternalTransfer>;
}

export class CustodianError extends Error {}

const MAX_BYTES = 1_000_000;
const TIMEOUT_MS = 10_000;
const ID = /^[A-Za-z0-9._:-]{1,200}$/;
const ASSET = /^[A-Za-z0-9._-]{1,32}$/;
const BASE_UNITS = /^[0-9]{1,78}$/;

export function assertSafeBaseUrl(raw: string, allowInsecure = process.env.CUSTODY_ALLOW_INSECURE_HTTP === '1'): URL {
  let u: URL;
  try { u = new URL(raw); } catch { throw new CustodianError('the connector URL is not a valid URL'); }
  const local = ['localhost', '127.0.0.1', '[::1]'].includes(u.hostname);
  if (u.protocol !== 'https:' && !(u.protocol === 'http:' && (local || allowInsecure))) {
    throw new CustodianError('the connector URL must be https (plain http only for localhost)');
  }
  if (u.username || u.password) throw new CustodianError('credentials do not belong in the connector URL');
  return u;
}

export class RestCustodianAdapter implements CustodianAdapter {
  private readonly base: string;
  constructor(baseUrl: string, private readonly token: string, private readonly fetchImpl: typeof fetch = fetch) {
    this.base = assertSafeBaseUrl(baseUrl).toString().replace(/\/$/, '');
  }

  private async call(method: string, path: string, body?: unknown, idempotencyKey?: string): Promise<any> {
    let res: Response;
    try {
      res = await this.fetchImpl(`${this.base}${path}`, {
        method,
        headers: { authorization: `Bearer ${this.token}`, accept: 'application/json', ...(body ? { 'content-type': 'application/json' } : {}), ...(idempotencyKey ? { 'idempotency-key': idempotencyKey } : {}) },
        body: body ? JSON.stringify(body) : undefined,
        signal: AbortSignal.timeout(TIMEOUT_MS),
        redirect: 'error', // a connector that redirects is a connector that is not the one that was approved
      });
    } catch (err) {
      throw new CustodianError(`could not reach the custodian: ${(err as Error).message}`);
    }
    const text = await res.text();
    if (text.length > MAX_BYTES) throw new CustodianError('the custodian\'s response was unreasonably large');
    if (!res.ok) throw new CustodianError(`the custodian answered ${res.status}: ${text.slice(0, 300)}`);
    try { return JSON.parse(text); } catch { throw new CustodianError('the custodian did not answer with JSON'); }
  }

  async listAccounts(): Promise<ExternalAccount[]> {
    const out = await this.call('GET', '/accounts');
    if (!Array.isArray(out?.accounts)) throw new CustodianError('malformed accounts response');
    return out.accounts.map((a: any) => {
      if (!ID.test(String(a?.id)) || typeof a?.name !== 'string' || !/^[a-z0-9_-]{2,30}$/.test(String(a?.blockchain))) throw new CustodianError('malformed account in the custodian\'s response');
      return { id: String(a.id), name: a.name.slice(0, 200), blockchain: a.blockchain, ...(typeof a.address === 'string' ? { address: a.address.slice(0, 128) } : {}) };
    });
  }

  async getBalances(accountId: string): Promise<ExternalBalance[]> {
    if (!ID.test(accountId)) throw new CustodianError('invalid account id');
    const out = await this.call('GET', `/accounts/${encodeURIComponent(accountId)}/balances`);
    if (!Array.isArray(out?.balances)) throw new CustodianError('malformed balances response');
    return out.balances.map((b: any) => {
      if (!ASSET.test(String(b?.asset)) || !BASE_UNITS.test(String(b?.amount)) || !Number.isInteger(b?.decimals) || b.decimals < 0 || b.decimals > 36) {
        throw new CustodianError('malformed balance in the custodian\'s response');
      }
      return { asset: b.asset, amount: String(b.amount), decimals: b.decimals };
    });
  }

  async initiateTransfer(req: TransferRequest): Promise<ExternalTransfer> {
    const out = await this.call('POST', '/transfers', { accountId: req.accountId, destination: req.destination, asset: req.asset, amount: req.amount, ...(req.memo ? { memo: req.memo } : {}) }, req.idempotencyKey);
    return this.transfer(out);
  }

  async getTransfer(id: string): Promise<ExternalTransfer> {
    if (!ID.test(id)) throw new CustodianError('invalid transfer id');
    return this.transfer(await this.call('GET', `/transfers/${encodeURIComponent(id)}`));
  }

  private transfer(o: any): ExternalTransfer {
    if (!ID.test(String(o?.id)) || !['pending', 'completed', 'failed'].includes(o?.status)) throw new CustodianError('malformed transfer in the custodian\'s response');
    return { id: String(o.id), status: o.status, ...(typeof o.txHash === 'string' ? { txHash: o.txHash.slice(0, 200) } : {}), ...(typeof o.error === 'string' ? { error: o.error.slice(0, 500) } : {}) };
  }
}
