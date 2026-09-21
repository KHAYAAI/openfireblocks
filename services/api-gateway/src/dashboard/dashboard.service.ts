import { Inject, Injectable, Logger } from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/database.module';
import { PostgresService } from '../database/postgres.service';
import { KeysService } from '../keys/keys.service';
import { Customer } from '../customers/customer.service';
import type { ComplianceData, KeyDetailData, OverviewData, WebhookRow } from './pages';

// Assembling what each dashboard page needs.
//
// Kept out of the controller because most of these are several queries
// that have to degrade independently. A dashboard whose balance panel
// takes the whole page down when one RPC endpoint is slow is a dashboard
// people stop opening -- and the page still has something useful to say
// about keys and history while the chain is unreachable.
@Injectable()
export class DashboardService {
  private readonly logger = new Logger(DashboardService.name);

  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    private readonly postgres: PostgresService,
    private readonly keys: KeysService,
  ) {}

  async overview(customer: Customer): Promise<OverviewData> {
    const [keys, transactions, signatures] = await Promise.all([
      this.postgres.listKeys(customer.customer_id),
      this.postgres.listTransactions(customer.customer_id, 10),
      this.signaturesInLastDays(customer.customer_id, 30),
    ]);

    const byStatus = (status: string) =>
      keys.filter((k: { status: string }) => k.status === status).length;

    return {
      keyCount: keys.length,
      activeKeys: byStatus('active'),
      pendingKeys: byStatus('pending_dkg'),
      failedKeys: byStatus('failed'),
      signaturesThisMonth: signatures,
      recentTransactions: transactions as never,
      // Surfaced on the first page somebody opens, deliberately. It is the
      // gap between "the threshold is 2 of 3" and "the threshold protects
      // you against a host compromise", and it should not be discoverable
      // only by reading a runbook.
      isolation: process.env.PARTY_ISOLATION_LEVEL ?? null,
    };
  }

  private async signaturesInLastDays(customerId: string, days: number): Promise<number> {
    try {
      const res = await this.pool.query<{ count: string }>(
        `SELECT count(*)::text AS count FROM signing_requests
          WHERE customer_id = $1::uuid
            AND status = 'completed'
            AND created_at > NOW() - ($2 || ' days')::interval`,
        [customerId, String(days)],
      );
      return Number(res.rows[0]?.count ?? 0);
    } catch (err) {
      this.logger.warn(`counting signatures for ${customerId} failed: ${(err as Error).message}`);
      return 0;
    }
  }

  async keyDetail(
    customer: Customer,
    keyId: string,
    chainId?: number,
  ): Promise<KeyDetailData | null> {
    const key = await this.postgres.getKey(keyId, customer.customer_id);
    if (!key) {
      return null;
    }

    const [signings, ceremonies] = await Promise.all([
      this.postgres.getSigningRequestsForKey(keyId, customer.customer_id, 25),
      this.postgres.getCeremoniesForKey(keyId, customer.customer_id),
    ]);

    // Addresses and balances are separate calls that fail separately. A
    // key with a derivable address and an unreachable node should show
    // the address.
    let addresses: Record<string, string> | null = null;
    try {
      const derived = await this.keys.getDepositAddresses(customer, keyId);
      addresses = (derived as { addresses: Record<string, string> }).addresses;
    } catch (err) {
      this.logger.debug(`addresses for ${keyId}: ${(err as Error).message}`);
    }

    let balances: KeyDetailData['balances'] = null;
    let balanceError: string | null = null;
    const chain = chainId ?? this.defaultChainFor(key.blockchain);
    if (chain) {
      try {
        const read = await this.keys.getBalances(customer, keyId, chain);
        balances = {
          chainId: chain,
          native: read.native.balance,
          tokens: read.tokens.map((t) => ({
            symbol: t.symbol,
            balance: t.balance,
            peg_currency: t.peg_currency,
            error: (t as { error?: string }).error,
          })),
        };
      } catch (err) {
        balanceError = (err as Error).message;
      }
    }

    return {
      key: key as never,
      addresses,
      balances,
      balanceError,
      signings: signings as never,
      ceremonies: ceremonies as never,
    };
  }

  // Which chain to read balances on when the page did not say.
  //
  // Configurable rather than guessed: a deployment on Polygon should not
  // have its dashboard quietly reading mainnet and reporting zero, which
  // is indistinguishable from the customer's money not being there.
  private defaultChainFor(blockchain: string): number | null {
    if (blockchain === 'bitcoin' || blockchain === 'solana' || blockchain === 'cosmos') {
      return null;
    }
    const configured = Number(process.env.DASHBOARD_DEFAULT_CHAIN_ID);
    return Number.isInteger(configured) && configured > 0 ? configured : null;
  }

  async compliance(customer: Customer, day?: string, chain?: string): Promise<ComplianceData> {
    const targetDay = day && /^\d{4}-\d{2}-\d{2}$/.test(day)
      ? day
      : new Date().toISOString().slice(0, 10);
    const targetChain = chain?.trim() || 'ethereum';

    const url = process.env.COMPLIANCE_SERVICE_URL;
    const base: ComplianceData = {
      day: targetDay,
      chain: targetChain,
      currencies: [],
      undecoded_count: 0,
      unvalued_assets: [],
      error: null,
    };
    if (!url) {
      return {
        ...base,
        error:
          'COMPLIANCE_SERVICE_URL is not configured, so threshold reporting cannot be read from here.',
      };
    }

    try {
      const res = await fetch(`${url}/v1/regulatory/thresholds/evaluate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          customer_id: customer.customer_id,
          chain: targetChain,
          day: `${targetDay}T00:00:00Z`,
        }),
        signal: AbortSignal.timeout(15_000),
      });
      if (!res.ok) {
        return { ...base, error: `The compliance service returned HTTP ${res.status}.` };
      }
      const body = (await res.json()) as Partial<ComplianceData>;
      return {
        ...base,
        currencies: body.currencies ?? [],
        undecoded_count: body.undecoded_count ?? 0,
        unvalued_assets: body.unvalued_assets ?? [],
      };
    } catch (err) {
      return { ...base, error: `The compliance service is unreachable: ${(err as Error).message}` };
    }
  }

  async webhooks(customer: Customer): Promise<{ hooks: WebhookRow[]; error: string | null }> {
    const url = process.env.WEBHOOKS_URL;
    if (!url) {
      return {
        hooks: [],
        error: 'WEBHOOKS_URL is not configured, so no events are being delivered at all.',
      };
    }
    try {
      const res = await fetch(`${url}/webhooks`, {
        headers: { 'X-Customer-ID': customer.customer_id },
        signal: AbortSignal.timeout(10_000),
      });
      if (!res.ok) {
        return { hooks: [], error: `The webhooks service returned HTTP ${res.status}.` };
      }
      const body = (await res.json()) as { webhooks?: WebhookRow[] };
      return { hooks: body.webhooks ?? [], error: null };
    } catch (err) {
      return { hooks: [], error: `The webhooks service is unreachable: ${(err as Error).message}` };
    }
  }
}
