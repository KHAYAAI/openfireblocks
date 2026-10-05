import { SignerError } from './bitcoin-signer-client';

// The gateway's side of mpc-signer's Solana and Cosmos routes.
//
// Free of Nest and of the database, like the Bitcoin client, so the shapes
// crossing the wire are stated once and can be tested against a stub server.
// There is no chain logic here: transaction layout, address derivation,
// balance and rent checks, signature verification and relay all live in
// services/mpc-signer, in the language whose libraries the platform
// validates against. A second implementation here would be a second set of
// bugs of the expensive kind.

export interface SolanaPrepareResult {
  message_hex: string;
  from: string;
  to: string;
  amount: string;
  fee: string;
  balance: string;
  blockhash: string;
  last_valid_block_height: number;
}

export interface SolanaFinalizeResult {
  signature: string;
  raw_tx_base64: string;
  broadcast: boolean;
}

export interface SolanaStatus {
  found: boolean;
  slot?: number;
  confirmation_status?: 'processed' | 'confirmed' | 'finalized';
  err?: unknown;
}

export interface CosmosSigningPlan {
  body_bytes_hex: string;
  auth_info_bytes_hex: string;
  chain_id: string;
  account_number: number;
  digest_hex: string;
}

export interface CosmosPrepareResult {
  plan: CosmosSigningPlan;
  from: string;
  to: string;
  amount: string;
  denom: string;
  fee: string;
  fee_denom: string;
  gas_limit: number;
  balance: string;
}

export interface CosmosFinalizeResult {
  txhash: string;
  raw_tx_base64: string;
  broadcast: boolean;
}

export interface CosmosStatus {
  found: boolean;
  height?: string;
  code?: number;
  raw_log?: string;
  txhash?: string;
}

export class ChainSignerClient {
  constructor(
    private readonly baseUrl = process.env.MPC_SIGNER_URL ?? 'http://localhost:8080',
    private readonly timeoutMs = 60_000,
  ) {}

  private async request<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
    let response: Response;
    try {
      response = await fetch(`${this.baseUrl}${path}`, {
        method,
        headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(this.timeoutMs),
      });
    } catch (err) {
      throw new SignerError(503, `the chain signer is unreachable: ${(err as Error).message}`);
    }
    const text = await response.text();
    let parsed: Record<string, unknown> = {};
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch {
        throw new SignerError(502, `the chain signer returned an unreadable response: ${text.slice(0, 200)}`);
      }
    }
    if (!response.ok) {
      throw new SignerError(response.status, String(parsed.error ?? `HTTP ${response.status}`), parsed);
    }
    return parsed as T;
  }

  private get<T>(path: string, query: Record<string, string>): Promise<T> {
    return this.request<T>('GET', `${path}?${new URLSearchParams(query)}`);
  }

  // ---- Solana ----
  solanaAddress(pubkeyHex: string): Promise<{ address: string }> {
    return this.get('/solana/addresses', { pubkey: pubkeyHex });
  }
  solanaBalance(address: string): Promise<{ address: string; lamports: string; asset: string; decimals: number }> {
    return this.get('/solana/balance', { address });
  }
  solanaPrepare(req: { pubkey_hex: string; destination: string; amount: string }): Promise<SolanaPrepareResult> {
    return this.request('POST', '/solana/prepare', req);
  }
  solanaFinalize(req: { message_hex: string; signature_hex: string; broadcast: boolean }): Promise<SolanaFinalizeResult> {
    return this.request('POST', '/solana/finalize', req);
  }
  solanaStatus(signature: string): Promise<SolanaStatus> {
    return this.get('/solana/status', { signature });
  }

  // ---- Cosmos ----
  cosmosAddress(pubkeyHex: string): Promise<{ address: string; prefix: string }> {
    return this.get('/cosmos/addresses', { pubkey: pubkeyHex });
  }
  cosmosBalance(address: string, denom?: string): Promise<{ address: string; denom: string; amount: string }> {
    return this.get('/cosmos/balance', denom ? { address, denom } : { address });
  }
  cosmosPrepare(req: { pubkey_hex: string; destination: string; amount: string; denom?: string; memo?: string }): Promise<CosmosPrepareResult> {
    return this.request('POST', '/cosmos/prepare', req);
  }
  cosmosFinalize(req: { plan: CosmosSigningPlan; r: string; s: string; pubkey_hex: string; broadcast: boolean }): Promise<CosmosFinalizeResult> {
    return this.request('POST', '/cosmos/finalize', req);
  }
  cosmosStatus(txhash: string): Promise<CosmosStatus> {
    return this.get('/cosmos/status', { txhash });
  }
}
