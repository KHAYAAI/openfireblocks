import {
  BadRequestException, ConflictException, ForbiddenException, Inject, Injectable, Logger, NotFoundException,
  OnModuleDestroy, OnModuleInit, UnprocessableEntityException,
} from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { UUID_RE, withTenant } from '../approvals/tenant-db';
import { CustomerService } from '../customers/customer.service';
import { KeysService } from '../keys/keys.service';
import { ControlsService } from '../controls/controls.service';
import { AlertsService } from '../controls/alerts.service';
import { kindOfBlockchain, parseTransfer } from '../transfers/parse-transfer';
import { TransfersService } from '../transfers/transfers.service';
import { planSweep } from './sweep-plan';

export interface SweepRuleInput {
  name: string;
  keyId: string;
  chainId?: number;
  destination: string;
  minAmount: string;
  reserve?: string;
  intervalSeconds?: number;
  travelRule?: Record<string, unknown>;
}

export type RunTrigger = 'manual' | 'schedule';

const BASE_UNITS = /^[0-9]{1,78}$/;

// Standing rules that move what has accumulated on a deposit key to a treasury
// address.
//
// A sweep is deliberately not a privileged path. Each run is submitted through
// TransfersService like a person's transfer, so the freeze, the whitelist, the
// spending policy and the approval quorum all apply: a large sweep waits for
// approvers like any large transfer, and a frozen organisation sweeps nothing.
// What a sweep adds is only the arithmetic (how much, when) and the schedule.
//
// What it does not do: tokens (native asset only), Bitcoin (the gateway has no
// balance read for it), or choose a destination -- the rule fixes that, the
// database refuses to change it, and it must already be allowed by the
// whitelist when the rule is made.
@Injectable()
export class SweepsService implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(SweepsService.name);
  private timer?: NodeJS.Timeout;

  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    private readonly customers: CustomerService,
    private readonly keys: KeysService,
    private readonly transfers: TransfersService,
    private readonly controls: ControlsService,
    private readonly alerts: AlertsService,
  ) {}

  // Scheduled sweeping is opt-in: SWEEPS_SCHEDULER_SECONDS=0 (the default) leaves
  // every rule to be run by hand through the API.
  onModuleInit() {
    const every = Number(process.env.SWEEPS_SCHEDULER_SECONDS ?? 0);
    if (every > 0) {
      this.timer = setInterval(() => void this.tick().catch((e) => this.logger.error(`sweep tick failed: ${e.message}`)), every * 1000);
      this.timer.unref?.();
    }
  }
  onModuleDestroy() { if (this.timer) clearInterval(this.timer); }

  // ---- rules --------------------------------------------------------------

  async create(customerId: string, userId: string, input: SweepRuleInput) {
    const customer = await this.customers.getByCustomerId(customerId);
    const key = await this.keys.getKey(input.keyId, customerId);
    if (!key) throw new NotFoundException(`no key ${input.keyId}`);
    if (key.status !== 'active') throw new ConflictException(`key ${input.keyId} is ${key.status}, not active`);
    if (!['ethereum', 'polygon', 'solana', 'cosmos'].includes(key.blockchain)) {
      throw new BadRequestException(`sweeps are not supported for ${key.blockchain} keys`);
    }
    const kind = kindOfBlockchain(key.blockchain);
    if (kind === 'evm' && !input.chainId) throw new BadRequestException('chainId is required for an EVM key');
    if (!BASE_UNITS.test(input.minAmount) || BigInt(input.minAmount) <= 0n) throw new BadRequestException('minAmount must be a positive integer in base units, as a string');
    const reserve = input.reserve ?? '0';
    if (!BASE_UNITS.test(reserve)) throw new BadRequestException('reserve must be a non-negative integer in base units, as a string');
    const name = input.name?.trim();
    if (!name) throw new BadRequestException('name is required');

    // The destination must be one the transfer path would accept for this
    // chain, and must not be the key's own address (a sweep to itself moves
    // nothing and spends a fee, every interval).
    await parseTransfer(kind, { destination: input.destination, amount: '1', ...(kind === 'evm' ? { chainId: input.chainId } : {}) });
    if (key.address && key.address.toLowerCase() === input.destination.toLowerCase()) {
      throw new BadRequestException('the destination is the key\'s own address');
    }
    // A rule created now but refused at every run would look like it was
    // working. Refuse it up front when the whitelist would.
    await this.controls.assertDestinationAllowed(customerId, key.blockchain, input.destination);

    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `INSERT INTO sweep_rules (customer_id, name, key_id, blockchain, chain_id, destination, min_amount, reserve, interval_seconds, travel_rule, created_by)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING *`,
        [customerId, name, input.keyId, key.blockchain, kind === 'evm' ? input.chainId : null, input.destination,
         input.minAmount, reserve, input.intervalSeconds ?? 3600, input.travelRule ? JSON.stringify(input.travelRule) : null, userId],
      ),
    );
    void this.alerts.notify({ severity: 'info', organisation: customer.name, title: 'A sweep rule was created', detail: `${name}: key ${input.keyId} → ${input.destination.slice(0, 10)}…, threshold ${input.minAmount}` });
    return ruleView(r.rows[0]);
  }

  async list(customerId: string) {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT * FROM sweep_rules WHERE customer_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC`, [customerId]),
    );
    return r.rows.map(ruleView);
  }

  async setEnabled(customerId: string, ruleId: string, enabled: boolean) {
    this.requireUuid(ruleId);
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`UPDATE sweep_rules SET enabled = $3 WHERE rule_id = $1 AND customer_id = $2 AND deleted_at IS NULL RETURNING *`, [ruleId, customerId, enabled]),
    );
    if (!r.rows[0]) throw new NotFoundException('sweep rule not found');
    return ruleView(r.rows[0]);
  }

  async remove(customerId: string, ruleId: string) {
    this.requireUuid(ruleId);
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`UPDATE sweep_rules SET deleted_at = now(), enabled = false WHERE rule_id = $1 AND customer_id = $2 AND deleted_at IS NULL RETURNING rule_id`, [ruleId, customerId]),
    );
    if (!r.rows[0]) throw new NotFoundException('sweep rule not found');
  }

  async runs(customerId: string, ruleId?: string) {
    if (ruleId) this.requireUuid(ruleId);
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT * FROM sweep_runs WHERE customer_id = $1 AND ($2::uuid IS NULL OR rule_id = $2) ORDER BY created_at DESC LIMIT 200`, [customerId, ruleId ?? null]),
    );
    return r.rows.map(runView);
  }

  // ---- running ------------------------------------------------------------

  // One run of one rule. Never throws for a refusal or a failed transfer: the
  // outcome is recorded as the run's status, because a scheduled sweep that
  // throws into the void is a sweep nobody notices stopped.
  async run(customerId: string, ruleId: string, trigger: RunTrigger, initiatorLabel: string, initiatorUserId: string | null) {
    this.requireUuid(ruleId);
    const found = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT * FROM sweep_rules WHERE rule_id = $1 AND customer_id = $2 AND deleted_at IS NULL`, [ruleId, customerId]),
    );
    const rule = found.rows[0];
    if (!rule) throw new NotFoundException('sweep rule not found');
    if (!rule.enabled) throw new UnprocessableEntityException('this sweep rule is switched off');

    const customer = await this.customers.getByCustomerId(customerId);
    const record = async (status: string, fields: Partial<Record<'balance' | 'amount' | 'reason' | 'approval_id' | 'result' | 'error', unknown>>) => {
      const r = await withTenant(this.pool, customerId, (c) =>
        c.query(
          `INSERT INTO sweep_runs (rule_id, customer_id, trigger, status, balance, amount, reason, approval_id, result, error)
           VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING *`,
          [ruleId, customerId, trigger, status, fields.balance ?? null, fields.amount ?? null, fields.reason ?? null,
           fields.approval_id ?? null, fields.result ? JSON.stringify(fields.result) : null, fields.error ?? null],
        ),
      );
      return runView(r.rows[0]);
    };

    let balance: bigint;
    try {
      balance = await this.nativeBalance(customer, rule);
    } catch (err) {
      const message = text(err);
      this.logger.warn(`sweep ${ruleId}: balance read failed: ${message}`);
      return record('failed', { error: `could not read the balance: ${message}` });
    }

    const plan = planSweep(balance, BigInt(rule.min_amount), BigInt(rule.reserve));
    if (!plan.sweep) return record('skipped', { balance: balance.toString(), reason: plan.reason });

    // Every run is a new transfer with its own idempotency key. A retry of
    // this same run (it has no row yet, so there is none) cannot be mistaken
    // for a new one, and two runs are two distinct transfers by design.
    const kind = kindOfBlockchain(rule.blockchain);
    const body: Record<string, unknown> = {
      destination: rule.destination,
      amount: plan.amount.toString(),
      ...(kind === 'evm' ? { chainId: rule.chain_id } : {}),
      ...(rule.travel_rule ? { travelRule: rule.travel_rule } : {}),
      ...(kind === 'cosmos' ? { memo: 'sweep' } : {}),
    };
    try {
      const dto = await parseTransfer(kind, body);
      const out = await this.transfers.submit(customer, rule.key_id, kind, dto, { userId: initiatorUserId, label: initiatorLabel });
      if (out.status === 'pending_approval') {
        return record('pending_approval', { balance: balance.toString(), amount: plan.amount.toString(), reason: plan.reason, approval_id: out.approvalId });
      }
      return record('completed', { balance: balance.toString(), amount: plan.amount.toString(), reason: plan.reason, result: out.result });
    } catch (err) {
      const message = text(err);
      // A freeze, the whitelist and a policy block are decisions, not
      // faults: recorded as refused, and not alarmed about.
      const refused = err instanceof ForbiddenException || err instanceof UnprocessableEntityException;
      if (!refused) {
        void this.alerts.notify({ severity: 'critical', organisation: customer.name, title: 'A sweep failed', detail: `${rule.name}: ${message.slice(0, 300)}` });
      }
      return record(refused ? 'refused' : 'failed', { balance: balance.toString(), amount: plan.amount.toString(), reason: plan.reason, error: message });
    }
  }

  // Runs every enabled rule that is due, across organisations. A rule is
  // claimed with a conditional update before it runs, so with several gateway
  // replicas each due rule is run by exactly one of them.
  async tick(): Promise<number> {
    // The scheduler reads across organisations, which row-level security
    // forbids the application role; it uses the admin connection exactly as
    // the other cross-tenant jobs do.
    const admin = new Pool({ connectionString: process.env.DATABASE_ADMIN_URL, max: 1 });
    let ran = 0;
    try {
      const due = await admin.query(
        `UPDATE sweep_rules SET last_claimed_at = now()
          WHERE rule_id IN (
            SELECT rule_id FROM sweep_rules
             WHERE enabled AND deleted_at IS NULL
               AND (last_claimed_at IS NULL OR last_claimed_at <= now() - make_interval(secs => interval_seconds))
             ORDER BY last_claimed_at NULLS FIRST LIMIT 50 FOR UPDATE SKIP LOCKED)
          RETURNING rule_id, customer_id`,
      );
      for (const row of due.rows) {
        try {
          await this.run(row.customer_id, row.rule_id, 'schedule', 'sweep scheduler', null);
          ran++;
        } catch (err) {
          this.logger.error(`scheduled sweep ${row.rule_id}: ${text(err)}`);
        }
      }
    } finally {
      await admin.end().catch(() => undefined);
    }
    return ran;
  }

  // ---- internals ----------------------------------------------------------

  // The native asset's balance in base units.
  private async nativeBalance(customer: Awaited<ReturnType<CustomerService['getByCustomerId']>>, rule: Record<string, any>): Promise<bigint> {
    const b: any = await this.keys.getBalancesForKey(customer, rule.key_id, rule.chain_id ?? undefined);
    if (rule.blockchain === 'solana' || rule.blockchain === 'cosmos') {
      const amount = b.balances?.[0]?.amount;
      if (amount === undefined || amount === null) throw new Error('the balance read returned no amount');
      return BigInt(amount);
    }
    if (b.native?.error) throw new Error(b.native.error);
    if (b.native?.balance_wei === null || b.native?.balance_wei === undefined) throw new Error('the balance read returned no amount');
    return BigInt(b.native.balance_wei);
  }

  private requireUuid(id: string) {
    if (!UUID_RE.test(id)) throw new NotFoundException('not found');
  }
}

function ruleView(r: Record<string, any>) {
  return {
    ruleId: r.rule_id, name: r.name, keyId: r.key_id, blockchain: r.blockchain, chainId: r.chain_id,
    destination: r.destination, minAmount: String(r.min_amount), reserve: String(r.reserve),
    intervalSeconds: r.interval_seconds, enabled: r.enabled, createdAt: new Date(r.created_at).toISOString(),
    lastClaimedAt: r.last_claimed_at ? new Date(r.last_claimed_at).toISOString() : null,
  };
}
function runView(r: Record<string, any>) {
  return {
    runId: r.run_id, ruleId: r.rule_id, trigger: r.trigger, status: r.status,
    balance: r.balance === null ? null : String(r.balance), amount: r.amount === null ? null : String(r.amount),
    reason: r.reason, approvalId: r.approval_id, result: r.result, error: r.error, createdAt: new Date(r.created_at).toISOString(),
  };
}
function text(err: unknown): string {
  const e = err as { getResponse?: () => unknown; message?: string };
  const r = e?.getResponse?.();
  if (r && typeof r === 'object') {
    const m = (r as { message?: unknown }).message;
    if (typeof m === 'string') return m;
    if (Array.isArray(m)) return m.join('; ');
  }
  return e?.message ?? String(err);
}
