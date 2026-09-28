import { BadRequestException, Inject, Injectable, NotFoundException } from '@nestjs/common';
import { Pool } from 'pg';
import { getAddress } from 'ethers';
import { PG_POOL } from '../database/pg-pool.token';
import { withTenant, UUID_RE } from '../approvals/tenant-db';
import { EvmRpcService } from '../tokens/evm-rpc.service';
import {
  classify,
  Classification,
  compareStatement,
  LedgerEntry,
  Parsed,
  parseSigned,
  SEVERITY,
  StatementRow,
  unsignedNonces,
} from './reconcile';

export interface ChainBreak {
  classification: Classification;
  severity: string;
  requestId?: string;
  txHash?: string;
  address?: string;
  detail: string;
}

// Reconciles what the platform signed against the chain, and against a
// customer's own statement.
@Injectable()
export class ReconciliationService {
  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    private readonly rpc: EvmRpcService,
  ) {}

  private async ledger(customerId: string): Promise<Array<LedgerEntry & { txHash: string | null; asset: string; amount: string }>> {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT request_id, signed_tx, created_at, tx_hash, asset_contract, asset_symbol,
                effective_to, effective_amount, amount
           FROM signing.transactions
          WHERE customer_id = $1 AND signed_tx IS NOT NULL
          ORDER BY created_at`,
        [customerId],
      ),
    );
    return r.rows.map((row) => ({
      requestId: row.request_id,
      signedTx: row.signed_tx,
      // Stored without a zone, written in UTC.
      createdAt: new Date(new Date(row.created_at).toISOString().replace('Z', '') + 'Z'),
      txHash: row.tx_hash,
      assetContract: row.asset_contract,
      effectiveTo: row.effective_to,
      effectiveAmount: row.effective_amount,
      asset: row.asset_symbol ?? 'ETH',
      amount: row.effective_amount ?? row.amount,
    }));
  }

  async runChain(input: {
    customerId: string;
    chainId: number;
    requestedBy: string;
    sinceHours?: number;
    missingAfterMinutes?: number;
  }) {
    if (!this.rpc.configured(input.chainId)) {
      throw new BadRequestException(`no JSON-RPC endpoint is configured for chain ${input.chainId} (EVM_RPC_${input.chainId})`);
    }
    const provider = this.rpc.provider(input.chainId);
    const now = new Date();
    const since = new Date(now.getTime() - (input.sinceHours ?? 168) * 3600_000);
    const missingAfter = input.missingAfterMinutes ?? 30;

    // Every signed EVM transaction for this chain -- all of them, not just
    // the window, because nonce accounting needs the whole history.
    const all: Parsed[] = [];
    let unparseable = 0;
    for (const e of await this.ledger(input.customerId)) {
      try {
        const p = parseSigned(e);
        if (p.chainId === input.chainId) all.push(p);
      } catch {
        unparseable++; // not an EVM transaction (a Bitcoin one, say)
      }
    }

    const breaks: ChainBreak[] = [];
    const counts: Record<string, number> = {};
    for (const p of all.filter((x) => x.entry.createdAt >= since)) {
      const receipt = await provider.getTransactionReceipt(p.hash);
      const known = receipt ? true : Boolean(await provider.getTransaction(p.hash));
      const c = classify(p, receipt, known, now, missingAfter);
      counts[c.classification] = (counts[c.classification] ?? 0) + 1;
      if (SEVERITY[c.classification] !== 'ok') {
        breaks.push({ classification: c.classification, severity: SEVERITY[c.classification], requestId: p.requestId, txHash: p.hash, detail: c.detail });
      }
    }

    // Addresses to account for: every EVM key the organisation holds, and
    // every address the ledger shows signing.
    const keys = await withTenant(this.pool, input.customerId, (c) =>
      c.query(
        `SELECT address FROM key_pairs WHERE customer_id = $1 AND blockchain = 'ethereum' AND address ~* '^0x[0-9a-f]{40}$'`,
        [input.customerId],
      ),
    );
    const addresses = new Set<string>([...keys.rows.map((r) => getAddress(r.address)), ...all.map((p) => getAddress(p.from))]);
    for (const address of addresses) {
      const chainNonce = await provider.getTransactionCount(address, 'latest');
      const signed = new Set(all.filter((p) => getAddress(p.from) === address).map((p) => p.nonce));
      const unsigned = unsignedNonces(chainNonce, signed);
      if (unsigned.length) {
        counts.unsigned_outbound = (counts.unsigned_outbound ?? 0) + unsigned.length;
        breaks.push({
          classification: 'unsigned_outbound',
          severity: 'critical',
          address,
          detail:
            `${address} has used nonce${unsigned.length > 1 ? 's' : ''} ${unsigned.join(', ')} on chain, and this platform ` +
            `never signed a transaction with ${unsigned.length > 1 ? 'them' : 'it'}. Something else holds or used this key.`,
        });
      }
    }

    const summary = {
      chainId: input.chainId,
      window: { since: since.toISOString(), until: now.toISOString() },
      examined: all.filter((x) => x.entry.createdAt >= since).length,
      addressesChecked: addresses.size,
      counts,
      unparseable,
      needsAttention: breaks.some((b) => b.severity === 'warning' || b.severity === 'critical'),
      critical: breaks.filter((b) => b.severity === 'critical').length,
    };
    return this.save(input.customerId, 'chain', input.chainId, summary, breaks, input.requestedBy);
  }

  async runStatement(input: {
    customerId: string;
    requestedBy: string;
    periodStart: string;
    periodEnd: string;
    rows: StatementRow[];
  }) {
    const start = new Date(input.periodStart);
    const end = new Date(input.periodEnd);
    if (isNaN(start.getTime()) || isNaN(end.getTime()) || end <= start) {
      throw new BadRequestException('periodStart and periodEnd must be dates, end after start');
    }
    for (const r of input.rows ?? []) {
      if (!/^0x[0-9a-fA-F]{64}$/.test(r.txHash) || !/^[0-9]+$/.test(String(r.amount)) || !r.asset) {
        throw new BadRequestException('each row needs txHash (0x + 64 hex), asset, and amount in base units');
      }
    }
    const ledger = (await this.ledger(input.customerId))
      .filter((e) => e.txHash && e.createdAt >= start && e.createdAt < end)
      .map((e) => ({ txHash: e.txHash!, asset: e.asset, amount: e.amount }));
    const { matched, breaks } = compareStatement(input.rows ?? [], ledger);
    const summary = {
      period: { start: start.toISOString(), end: end.toISOString() },
      statementRows: input.rows?.length ?? 0,
      ledgerRows: ledger.length,
      matched,
      breaks: breaks.length,
      needsAttention: breaks.length > 0,
    };
    return this.save(input.customerId, 'statement', null, summary, breaks, input.requestedBy);
  }

  private async save(customerId: string, kind: string, chainId: number | null, summary: unknown, breaks: unknown[], by: string) {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `INSERT INTO reconciliation_runs (customer_id, kind, chain_id, summary, breaks, requested_by)
         VALUES ($1, $2, $3, $4, $5, $6) RETURNING run_id, started_at`,
        [customerId, kind, chainId, JSON.stringify(summary), JSON.stringify(breaks), by],
      ),
    );
    return { runId: r.rows[0].run_id, kind, summary, breaks };
  }

  async list(customerId: string) {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT run_id, kind, chain_id, started_at, summary, requested_by FROM reconciliation_runs
          WHERE customer_id = $1 ORDER BY started_at DESC LIMIT 100`,
        [customerId],
      ),
    );
    return r.rows.map((x) => ({ runId: x.run_id, kind: x.kind, chainId: x.chain_id, at: x.started_at, summary: x.summary, requestedBy: x.requested_by }));
  }

  async get(customerId: string, runId: string) {
    if (!UUID_RE.test(runId)) throw new NotFoundException('run not found');
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM reconciliation_runs WHERE run_id = $1`, [runId]));
    if (!r.rows[0]) throw new NotFoundException('run not found');
    const x = r.rows[0];
    return { runId: x.run_id, kind: x.kind, chainId: x.chain_id, at: x.started_at, summary: x.summary, breaks: x.breaks, requestedBy: x.requested_by };
  }
}
