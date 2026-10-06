import { BadRequestException, ConflictException, Inject, Injectable, NotFoundException, OnModuleInit, UnprocessableEntityException } from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { ApprovalsService } from '../approvals/approvals.service';
import { UUID_RE, withTenant } from '../approvals/tenant-db';
import { CustomerService } from '../customers/customer.service';
import { KeysService } from '../keys/keys.service';
import { ControlsService } from '../controls/controls.service';
import { CustodianTransferRequest, CustodyExecutor, CustodyExecutors } from '../transfers/custody-executor';
import { Initiator, TransfersService } from '../transfers/transfers.service';
import { CustodianAdapter, CustodianError, ExternalAccount, ExternalBalance, RestCustodianAdapter, assertSafeBaseUrl } from './custodian-adapter';

export interface TransferInput {
  custodianId?: string;
  accountId?: string;
  route?: boolean;
  destination: string;
  asset: string;
  amount: string;
  decimals?: number;
  memo?: string;
}

const ASSET = /^[A-Za-z0-9._-]{1,32}$/;

// One governance layer over every place the organisation's assets are held: this
// platform's own threshold keys and any custodian that speaks the connector contract
// (docs/deployment/MULTI-CUSTODIAN.md).
//
// What it gives: a single balance sheet; a single approval quorum, freeze and address
// whitelist for transfers out of any of them; routing rules that pick the account.
// What it does not: apply the spending policy engine to another custodian's account
// (it cannot see their rules), so every transfer from another custodian needs approval;
// or Travel Rule records, which stay with the custodian that actually sends.
@Injectable()
export class CustodyService implements CustodyExecutor, OnModuleInit {
  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    private readonly customers: CustomerService,
    private readonly keys: KeysService,
    private readonly transfers: TransfersService,
    private readonly controls: ControlsService,
    private readonly executors: CustodyExecutors,
    private readonly approvals: ApprovalsService,
  ) {}

  onModuleInit() { this.executors.register(this); }

  // ---- custodians ---------------------------------------------------------

  async addCustodian(customerId: string, userId: string, input: { name: string; baseUrl: string; tokenEnv: string }) {
    const name = input.name?.trim();
    if (!name) throw new BadRequestException('name is required');
    if (!/^CUSTODY_TOKEN_[A-Z0-9_]{1,60}$/.test(input.tokenEnv ?? '')) throw new BadRequestException('tokenEnv must be the name of an environment variable like CUSTODY_TOKEN_BANKX');
    try { assertSafeBaseUrl(input.baseUrl); } catch (e) { throw new BadRequestException((e as Error).message); }
    // Provisioned at deploy time, not typed into an API: fail now if it is not there,
    // not at the first transfer.
    if (!process.env[input.tokenEnv]) throw new UnprocessableEntityException(`${input.tokenEnv} is not set in the gateway's environment; provision the connector's token there first`);
    try {
      const r = await withTenant(this.pool, customerId, (c) => c.query(
        `INSERT INTO custodians (customer_id, name, base_url, token_env, added_by) VALUES ($1,$2,$3,$4,$5) RETURNING *`,
        [customerId, name, input.baseUrl, input.tokenEnv, userId]));
      return custodianView(r.rows[0]);
    } catch (err) {
      if ((err as { code?: string }).code === '23505') throw new ConflictException('a custodian with this name already exists');
      throw err;
    }
  }

  async listCustodians(customerId: string) {
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM custodians WHERE customer_id = $1 ORDER BY added_at`, [customerId]));
    return r.rows.map(custodianView);
  }

  async setEnabled(customerId: string, custodianId: string, enabled: boolean) {
    const c = await this.custodian(customerId, custodianId);
    const r = await withTenant(this.pool, customerId, (cl) => cl.query(`UPDATE custodians SET enabled = $3 WHERE custodian_id = $1 AND customer_id = $2 RETURNING *`, [c.custodian_id, customerId, enabled]));
    return custodianView(r.rows[0]);
  }

  // ---- the balance sheet --------------------------------------------------

  // Every account with its balances, from every source, and totals by asset. One
  // source failing is that source's error: the rest of the picture is still shown.
  async overview(customerId: string, evmChainId?: number) {
    const customer = await this.customers.getByCustomerId(customerId);
    const sources: any[] = [];

    const native = { source: 'native', name: 'This platform (threshold keys)', accounts: [] as any[], error: undefined as string | undefined };
    try {
      for (const k of await this.keys.listKeys(customerId)) {
        if (k.status !== 'active') continue;
        const acct: any = { id: k.key_id, name: k.name, blockchain: k.blockchain, address: k.address, balances: [] as ExternalBalance[] };
        try {
          const b: any = await this.keys.getBalancesForKey(customer, k.key_id, ['ethereum', 'polygon'].includes(k.blockchain) ? evmChainId : undefined);
          if (b.balances) acct.balances = b.balances.map((x: any) => ({ asset: x.asset, amount: String(x.amount), decimals: x.decimals }));
          else if (b.native?.balance_wei != null) acct.balances = [{ asset: 'ETH', amount: String(b.native.balance_wei), decimals: 18 }];
        } catch (e) { acct.error = (e as Error).message; }
        native.accounts.push(acct);
      }
    } catch (e) { native.error = (e as Error).message; }
    sources.push(native);

    await Promise.all((await this.listCustodians(customerId)).filter((c) => c.enabled).map(async (c) => {
      const s: any = { source: 'custodian', custodianId: c.custodianId, name: c.name, accounts: [] as any[] };
      try {
        const adapter = this.adapterFor(await this.custodian(customerId, c.custodianId));
        for (const a of await adapter.listAccounts()) {
          const acct: any = { ...a, balances: [] };
          try { acct.balances = await adapter.getBalances(a.id); } catch (e) { acct.error = (e as Error).message; }
          s.accounts.push(acct);
        }
      } catch (e) { s.error = (e as Error).message; }
      sources.push(s);
    }));

    // Totals by asset. Two sources reporting the same asset with different decimals are
    // not added together: that would be a silently wrong total.
    const totals = new Map<string, { asset: string; decimals: number; amount: bigint; conflict?: boolean }>();
    for (const s of sources) for (const a of s.accounts) for (const b of a.balances ?? []) {
      const t = totals.get(b.asset);
      if (!t) totals.set(b.asset, { asset: b.asset, decimals: b.decimals, amount: BigInt(b.amount) });
      else if (t.decimals !== b.decimals) t.conflict = true;
      else t.amount += BigInt(b.amount);
    }
    return {
      sources,
      totals: [...totals.values()].map((t) => (t.conflict ? { asset: t.asset, decimals: t.decimals, amount: null, error: 'sources disagree on this asset\'s decimals; not added together' } : { asset: t.asset, decimals: t.decimals, amount: t.amount.toString() })),
    };
  }

  // ---- routing ------------------------------------------------------------

  async addRoute(customerId: string, userId: string, input: { asset: string; maxAmount?: string; custodianId: string; accountId: string; priority?: number }) {
    if (!ASSET.test(input.asset ?? '')) throw new BadRequestException('asset is invalid');
    if (input.maxAmount !== undefined && !/^[1-9][0-9]{0,77}$/.test(input.maxAmount)) throw new BadRequestException('maxAmount must be a positive integer in base units, as a string');
    const c = await this.custodian(customerId, input.custodianId);
    if (!c.enabled) throw new ConflictException('that custodian is switched off');
    const accounts = await this.adapterFor(c).listAccounts();
    if (!accounts.some((a) => a.id === input.accountId)) throw new BadRequestException('the custodian has no such account');
    const r = await withTenant(this.pool, customerId, (cl) => cl.query(
      `INSERT INTO custody_routes (customer_id, asset, max_amount, custodian_id, account_id, priority, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING *`,
      [customerId, input.asset, input.maxAmount ?? null, input.custodianId, input.accountId, input.priority ?? 100, userId]));
    return routeView(r.rows[0]);
  }

  async listRoutes(customerId: string) {
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM custody_routes WHERE customer_id = $1 AND removed_at IS NULL ORDER BY priority, created_at`, [customerId]));
    return r.rows.map(routeView);
  }

  async removeRoute(customerId: string, routeId: string) {
    if (!UUID_RE.test(routeId)) throw new NotFoundException('route not found');
    const r = await withTenant(this.pool, customerId, (c) => c.query(`UPDATE custody_routes SET removed_at = now() WHERE route_id = $1 AND customer_id = $2 AND removed_at IS NULL RETURNING route_id`, [routeId, customerId]));
    if (!r.rows[0]) throw new NotFoundException('route not found');
  }

  // The account a transfer of this asset and size should come from.
  async resolveRoute(customerId: string, asset: string, amount: string) {
    const routes = await this.listRoutes(customerId);
    const hit = routes.find((r) => r.asset.toLowerCase() === asset.toLowerCase() && (r.maxAmount === null || BigInt(amount) <= BigInt(r.maxAmount)));
    if (!hit) throw new UnprocessableEntityException(`no routing rule covers ${amount} of ${asset}; name a custodian and account, or add a rule`);
    return hit;
  }

  // ---- transfers ----------------------------------------------------------

  async submitTransfer(customerId: string, initiator: Initiator, input: TransferInput) {
    if (!ASSET.test(input.asset ?? '')) throw new BadRequestException('asset is invalid');
    if (!/^[1-9][0-9]{0,77}$/.test(input.amount ?? '')) throw new BadRequestException('amount must be a positive integer in base units, as a string');
    if (!input.destination || input.destination.length > 128) throw new BadRequestException('destination is required');
    let custodianId = input.custodianId; let accountId = input.accountId;
    if (input.route) {
      const r = await this.resolveRoute(customerId, input.asset, input.amount);
      custodianId = r.custodianId; accountId = r.accountId;
    }
    if (!custodianId || !accountId) throw new BadRequestException('give custodianId and accountId, or route: true');
    const custodian = await this.custodian(customerId, custodianId);
    if (!custodian.enabled) throw new ConflictException('that custodian is switched off');

    // The chain comes from the custodian's own listing, never from the caller: the
    // whitelist is checked against the chain the account is really on.
    const account = (await this.adapterFor(custodian).listAccounts()).find((a) => a.id === accountId);
    if (!account) throw new BadRequestException('the custodian has no such account');
    let decimals = input.decimals;
    if (decimals === undefined) {
      const bal = (await this.adapterFor(custodian).getBalances(accountId)).find((b) => b.asset === input.asset);
      if (!bal) throw new BadRequestException(`the account holds no ${input.asset}; give decimals explicitly`);
      decimals = bal.decimals;
    }
    if (!Number.isInteger(decimals) || decimals < 0 || decimals > 36) throw new BadRequestException('decimals is invalid');

    // Refused now, before anyone is asked, and again when it runs.
    await this.controls.assertCanSign(customerId);
    await this.controls.assertDestinationAllowed(customerId, account.blockchain, input.destination);

    const req: CustodianTransferRequest = { accountId, destination: input.destination, asset: input.asset, amount: input.amount, decimals, blockchain: account.blockchain, ...(input.memo ? { memo: input.memo.slice(0, 200) } : {}) };
    return this.transfers.submit(await this.customers.getByCustomerId(customerId), custodianId, 'custodian', req, initiator);
  }

  // The current state of an approved transfer at the custodian, refreshed from it.
  async transferStatus(customerId: string, approvalId: string) {
    const pending = await this.approvals.pendingTransfer(customerId, approvalId);
    if (!pending || pending.kind !== 'custodian') throw new NotFoundException('no such custodian transfer');
    const external = (pending.result as { externalTransferId?: string } | null)?.externalTransferId;
    let live: unknown = null;
    if (external) {
      try { live = await this.adapterFor(await this.custodian(customerId, pending.keyId)).getTransfer(external); } catch (e) { live = { error: (e as Error).message }; }
    }
    return { approvalId, status: pending.status, result: pending.result, error: pending.error, live };
  }

  // ---- the executor the transfer path calls once a transfer is approved ----

  async describe(customerId: string, custodianId: string, accountId: string) {
    const c = await this.custodian(customerId, custodianId);
    const a = (await this.adapterFor(c).listAccounts()).find((x) => x.id === accountId);
    return { custodian: c.name, account: a?.name ?? accountId };
  }

  async execute(customerId: string, custodianId: string, req: CustodianTransferRequest, idempotencyKey: string): Promise<Record<string, unknown>> {
    // A freeze or a whitelist change after approval still stops it.
    await this.controls.assertCanSign(customerId);
    await this.controls.assertDestinationAllowed(customerId, req.blockchain, req.destination);
    const c = await this.custodian(customerId, custodianId);
    if (!c.enabled) throw new ConflictException('that custodian is switched off');
    const t = await this.adapterFor(c).initiateTransfer({ accountId: req.accountId, destination: req.destination, asset: req.asset, amount: req.amount, memo: req.memo, idempotencyKey });
    if (t.status === 'failed') throw new CustodianError(`the custodian refused the transfer: ${t.error ?? 'no reason given'}`);
    return { custodian: c.name, accountId: req.accountId, asset: req.asset, amount: req.amount, destination: req.destination, externalTransferId: t.id, externalStatus: t.status, txHash: t.txHash ?? null };
  }

  // ---- internals ----------------------------------------------------------

  private async custodian(customerId: string, id: string) {
    if (!UUID_RE.test(id)) throw new NotFoundException('custodian not found');
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM custodians WHERE custodian_id = $1 AND customer_id = $2`, [id, customerId]));
    if (!r.rows[0]) throw new NotFoundException('custodian not found');
    return r.rows[0];
  }

  private adapterFor(c: Record<string, any>): CustodianAdapter {
    const token = process.env[c.token_env];
    if (!token) throw new CustodianError(`${c.token_env} is not set, so ${c.name} cannot be reached`);
    return new RestCustodianAdapter(c.base_url, token);
  }
}

function custodianView(r: Record<string, any>) {
  return { custodianId: r.custodian_id, name: r.name, type: r.type, baseUrl: r.base_url, enabled: r.enabled, addedAt: new Date(r.added_at).toISOString() };
}
function routeView(r: Record<string, any>) {
  return { routeId: r.route_id, asset: r.asset, maxAmount: r.max_amount === null ? null : String(r.max_amount), custodianId: r.custodian_id, accountId: r.account_id, priority: r.priority };
}
