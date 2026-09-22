// The gateway's side of mpc-signer's two Bitcoin routes.
//
// Kept in its own file, free of Nest and of the database, so the shapes
// crossing the wire are stated once and can be tested against a stub server
// without a running platform.
//
// There is deliberately no Bitcoin logic here. Coin selection, BIP143
// sighashes, witness assembly, dust thresholds and fee estimation all live
// in services/mpc-signer/chains, in the language whose Bitcoin library this
// platform validates its transactions against. A second implementation in
// TypeScript would be a second set of bugs, and the expensive kind: a
// mistake in any of them produces a transaction that is perfectly valid and
// sends money to the wrong place, or to nobody.

export interface BitcoinSigningPlan {
  unsigned_tx_hex: string;
  sighashes: string[];
  prev_scripts: string[];
  witness: boolean[];
  amounts: number[];
}

export interface BitcoinCoinSelection {
  selected: Array<{ txid: string; vout: number; amount: number; confirmations: number }>;
  total_in: number;
  amount: number;
  fee: number;
  change: number;
  virtual_size: number;
  fee_rate: number;
  change_dropped_to_fee: boolean;
}

export interface BitcoinPrepareResult {
  selection: BitcoinCoinSelection;
  plan: BitcoinSigningPlan;
  balance: number;
  utxo_count: number;
  addresses: string[];
}

export interface BitcoinFinalizeResult {
  raw_tx_hex: string;
  txid: string;
  broadcast: boolean;
  broadcast_txid?: string;
}

// SignerError carries the signer's status code so the caller can map it
// onto an HTTP response rather than turning every failure into a 500.
//
// The distinction is not cosmetic. "You do not have enough Bitcoin" and
// "the node is unreachable" are both failures to send money, and a customer
// can act on the first one.
export class SignerError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly body?: Record<string, unknown>,
  ) {
    super(message);
  }
}

export class BitcoinSignerClient {
  constructor(
    private readonly baseUrl = process.env.MPC_SIGNER_URL ?? 'http://localhost:8080',
    private readonly timeoutMs = 150_000,
  ) {}

  // Long by HTTP standards, and it has to be: scanning a node's UTXO set is
  // tens of seconds on mainnet. A shorter timeout would report a working
  // node as unreachable under exactly the conditions it is needed.
  private async call<T>(path: string, body: unknown): Promise<T> {
    let response: Response;
    try {
      response = await fetch(`${this.baseUrl}${path}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(this.timeoutMs),
      });
    } catch (err) {
      throw new SignerError(503, `the Bitcoin signer is unreachable: ${(err as Error).message}`);
    }

    const text = await response.text();
    let parsed: Record<string, unknown> = {};
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch {
        throw new SignerError(502, `the Bitcoin signer returned an unreadable response: ${text.slice(0, 200)}`);
      }
    }
    if (!response.ok) {
      throw new SignerError(response.status, String(parsed.error ?? `HTTP ${response.status}`), parsed);
    }
    return parsed as T;
  }

  async addresses(pubKeyHex: string, network: string): Promise<{
    network: string;
    segwit: string;
    legacy: string;
    preferred: string;
  }> {
    const query = new URLSearchParams({ pubkey: pubKeyHex, network });
    let response: Response;
    try {
      response = await fetch(`${this.baseUrl}/bitcoin/addresses?${query}`, {
        signal: AbortSignal.timeout(10_000),
      });
    } catch (err) {
      throw new SignerError(503, `the Bitcoin signer is unreachable: ${(err as Error).message}`);
    }
    const text = await response.text();
    const parsed = text ? JSON.parse(text) : {};
    if (!response.ok) {
      throw new SignerError(response.status, String(parsed.error ?? `HTTP ${response.status}`), parsed);
    }
    return parsed;
  }

  prepare(req: {
    network: string;
    pubkey_hex: string;
    destination: string;
    amount: number;
    fee_rate?: number;
    confirmation_target?: number;
    change_address?: string;
    min_confirmations?: number;
  }): Promise<BitcoinPrepareResult> {
    return this.call<BitcoinPrepareResult>('/bitcoin/prepare', req);
  }

  finalize(req: {
    plan: BitcoinSigningPlan;
    signatures: Array<{ r: string; s: string }>;
    pubkey_hex: string;
    broadcast: boolean;
  }): Promise<BitcoinFinalizeResult> {
    return this.call<BitcoinFinalizeResult>('/bitcoin/finalize', req);
  }
}

// splitSignature turns a 65-byte ceremony signature into the (r, s) pair
// Bitcoin needs.
//
// The ceremony returns r || s || v, where v is the recovery id Ethereum
// uses to recover a signer's address from a signature. Bitcoin does not:
// the public key is supplied explicitly in the signature script or witness,
// so there is nothing to recover and the last byte is simply dropped.
export function splitSignature(signature: string): { r: string; s: string } {
  const hex = signature.startsWith('0x') ? signature.slice(2) : signature;
  if (hex.length < 128) {
    throw new Error(`the ceremony returned a ${hex.length / 2}-byte signature; expected at least 64 bytes of r and s`);
  }
  return { r: hex.slice(0, 64), s: hex.slice(64, 128) };
}
