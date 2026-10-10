import { WebhookEmitter } from '../webhooks/webhooks.service';
import { BadRequestException, ConflictException, ForbiddenException, Inject, Injectable, NotFoundException, Optional } from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { withTenant } from '../approvals/tenant-db';
import { AuditService } from '../database/audit.service';
import { AlertsService } from './alerts.service';
import { v4 as uuid } from 'uuid';

export const WHITELIST_CHAINS = ['bitcoin', 'ethereum', 'polygon', 'solana', 'cosmos'] as const;
export type WhitelistChain = (typeof WHITELIST_CHAINS)[number];

const EVM = new Set<string>(['ethereum', 'polygon']);

// Shapes only: enough to refuse an obviously wrong entry, not to prove an
// address is spendable.
const FORMAT: Record<WhitelistChain, RegExp> = {
  ethereum: /^0x[0-9a-fA-F]{40}$/,
  polygon: /^0x[0-9a-fA-F]{40}$/,
  bitcoin: /^(bc1|tb1|bcrt1)[0-9a-z]{20,90}$|^[123mn][1-9A-HJ-NP-Za-km-z]{25,40}$/,
  solana: /^[1-9A-HJ-NP-Za-km-z]{32,44}$/,
  cosmos: /^[a-z]{2,16}1[0-9a-z]{38,}$/,
};

export function normaliseAddress(chain: string, address: string): string {
  const a = address.trim();
  // EVM addresses are case-insensitive hex (checksum casing is decoration);
  // the others are case-sensitive and must match exactly.
  return EVM.has(chain) ? a.toLowerCase() : a;
}

export interface ControlsView {
  frozen: boolean;
  frozenReason: string | null;
  frozenAt: string | null;
  whitelistEnforced: boolean;
  whitelistCooldownMinutes: number;
}

export class FrozenException extends ForbiddenException {
  constructor(reason: string | null) {
    super(`this organisation is frozen: nothing can be signed until an admin lifts the freeze${reason ? ` (${reason})` : ''}`);
  }
}

// Organisation-level safety controls. The checks live here so every signing
// path asks the same question the same way.
@Injectable()
export class ControlsService {
  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    private readonly audit: AuditService,
    private readonly alerts: AlertsService,
    @Optional() private readonly webhooks?: WebhookEmitter,
  ) {}

  async get(customerId: string): Promise<ControlsView> {
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM org_controls WHERE customer_id = $1`, [customerId]));
    const row = r.rows[0];
    return {
      frozen: row?.frozen ?? false,
      frozenReason: row?.frozen_reason ?? null,
      frozenAt: row?.frozen_at ? new Date(row.frozen_at).toISOString() : null,
      whitelistEnforced: row?.whitelist_enforced ?? false,
      whitelistCooldownMinutes: row?.whitelist_cooldown_minutes ?? 0,
    };
  }

  // Called before anything is signed. Fails closed: if the controls cannot be
  // read, nothing is signed, because "could not check whether we are frozen"
  // is not "not frozen".
  async assertCanSign(customerId: string): Promise<void> {
    let c: ControlsView;
    try {
      c = await this.get(customerId);
    } catch {
      throw new ForbiddenException('the organisation\'s safety controls could not be checked, so nothing was signed');
    }
    if (c.frozen) throw new FrozenException(c.frozenReason);
  }

  // Called with the destination of a transfer. A no-op unless the
  // organisation enforces a whitelist.
  async assertDestinationAllowed(customerId: string, chain: string, address: string): Promise<void> {
    let controls: ControlsView;
    try {
      controls = await this.get(customerId);
    } catch {
      throw new ForbiddenException('the organisation\'s safety controls could not be checked, so nothing was signed');
    }
    if (!controls.whitelistEnforced) return;
    const addr = normaliseAddress(chain, address);
    const chains = EVM.has(chain) ? ['ethereum', 'polygon'] : [chain];
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT active_from FROM address_whitelist
          WHERE customer_id = $1 AND blockchain = ANY($2) AND address = $3 AND removed_at IS NULL`,
        [customerId, chains, addr],
      ),
    );
    if (r.rows.length === 0) {
      throw new ForbiddenException('this address is not on the organisation\'s whitelist, so the transfer was refused');
    }
    const live = r.rows.some((row) => new Date(row.active_from).getTime() <= Date.now());
    if (!live) {
      throw new ForbiddenException('this address was added to the whitelist recently and is still in its cooling-off period, so the transfer was refused');
    }
  }

  async freeze(customerId: string, orgName: string, userId: string, email: string, reason: string): Promise<ControlsView> {
    const why = reason.trim();
    if (!why) throw new BadRequestException('say why the organisation is being frozen');
    await withTenant(this.pool, customerId, (c) =>
      c.query(
        `INSERT INTO org_controls (customer_id, frozen, frozen_reason, frozen_by, frozen_at)
         VALUES ($1, true, $2, $3, now())
         ON CONFLICT (customer_id) DO UPDATE
           SET frozen = true, frozen_reason = $2, frozen_by = $3, frozen_at = now(), updated_at = now()`,
        [customerId, why, userId],
      ),
    );
    await this.record(customerId, 'controls.frozen', `${email} froze the organisation: ${why}`);
    void this.webhooks?.emit(customerId, 'org.frozen', { reason: why, by: email });
    void this.alerts.notify({ severity: 'critical', organisation: orgName, title: 'Organisation FROZEN: nothing can be signed', detail: `${email}: ${why}` });
    return this.get(customerId);
  }

  async unfreeze(customerId: string, orgName: string, email: string): Promise<ControlsView> {
    await withTenant(this.pool, customerId, (c) =>
      c.query(
        `UPDATE org_controls SET frozen = false, frozen_reason = NULL, updated_at = now() WHERE customer_id = $1`,
        [customerId],
      ),
    );
    await this.record(customerId, 'controls.unfrozen', `${email} lifted the freeze`);
    void this.webhooks?.emit(customerId, 'org.unfrozen', { by: email });
    void this.alerts.notify({ severity: 'warning', organisation: orgName, title: 'Freeze lifted: signing is possible again', detail: email });
    return this.get(customerId);
  }

  async setWhitelistMode(customerId: string, orgName: string, email: string, enforced: boolean, cooldownMinutes: number): Promise<ControlsView> {
    if (!Number.isInteger(cooldownMinutes) || cooldownMinutes < 0 || cooldownMinutes > 10080) {
      throw new BadRequestException('the cooling-off period must be between 0 minutes and 7 days');
    }
    await withTenant(this.pool, customerId, (c) =>
      c.query(
        `INSERT INTO org_controls (customer_id, whitelist_enforced, whitelist_cooldown_minutes) VALUES ($1, $2, $3)
         ON CONFLICT (customer_id) DO UPDATE SET whitelist_enforced = $2, whitelist_cooldown_minutes = $3, updated_at = now()`,
        [customerId, enforced, cooldownMinutes],
      ),
    );
    await this.record(customerId, 'controls.whitelist_mode', `${email} set the whitelist to ${enforced ? 'enforced' : 'not enforced'} with a ${cooldownMinutes}-minute cooling-off period`);
    void this.alerts.notify({ severity: 'warning', organisation: orgName, title: `Address whitelist ${enforced ? 'enforced' : 'switched off'}`, detail: `${email}; cooling-off ${cooldownMinutes} min` });
    return this.get(customerId);
  }

  async listWhitelist(customerId: string) {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT w.entry_id, w.blockchain, w.address, w.label, w.added_at, w.active_from, w.removed_at, u.email AS added_by
           FROM address_whitelist w JOIN users u ON u.id = w.added_by
          WHERE w.customer_id = $1 AND w.removed_at IS NULL ORDER BY w.added_at DESC`,
        [customerId],
      ),
    );
    return r.rows.map((x) => ({
      entryId: x.entry_id, blockchain: x.blockchain, address: x.address, label: x.label,
      addedBy: x.added_by, addedAt: x.added_at, activeFrom: x.active_from,
      active: new Date(x.active_from).getTime() <= Date.now(),
    }));
  }

  async addToWhitelist(customerId: string, orgName: string, userId: string, email: string, input: { blockchain: string; address: string; label?: string }) {
    if (!(WHITELIST_CHAINS as readonly string[]).includes(input.blockchain)) throw new BadRequestException(`blockchain must be one of ${WHITELIST_CHAINS.join(', ')}`);
    const chain = input.blockchain as WhitelistChain;
    const addr = normaliseAddress(chain, input.address ?? '');
    if (!FORMAT[chain].test(addr)) throw new BadRequestException(`that does not look like a ${chain} address`);
    const controls = await this.get(customerId);
    const id = uuid();
    try {
      await withTenant(this.pool, customerId, (c) =>
        c.query(
          `INSERT INTO address_whitelist (entry_id, customer_id, blockchain, address, label, added_by, active_from)
           VALUES ($1, $2, $3, $4, $5, $6, now() + ($7 || ' minutes')::interval)`,
          [id, customerId, chain, addr, input.label?.trim() || null, userId, String(controls.whitelistCooldownMinutes)],
        ),
      );
    } catch (e) {
      if ((e as { code?: string }).code === '23505') throw new ConflictException('that address is already on the whitelist');
      throw e;
    }
    await this.record(customerId, 'controls.whitelist_added', `${email} added ${chain} address ${addr.slice(0, 8)}…${addr.slice(-4)} to the whitelist`);
    void this.alerts.notify({ severity: 'warning', organisation: orgName, title: 'Address added to the whitelist', detail: `${email} added a ${chain} address; usable after ${controls.whitelistCooldownMinutes} min` });
    return { entryId: id };
  }

  async removeFromWhitelist(customerId: string, orgName: string, userId: string, email: string, entryId: string): Promise<void> {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `UPDATE address_whitelist SET removed_at = now(), removed_by = $3
          WHERE entry_id = $1 AND customer_id = $2 AND removed_at IS NULL RETURNING blockchain`,
        [entryId, customerId, userId],
      ),
    );
    if (r.rows.length === 0) throw new NotFoundException('no such whitelist entry');
    await this.record(customerId, 'controls.whitelist_removed', `${email} removed a ${r.rows[0].blockchain} address from the whitelist`);
    void this.alerts.notify({ severity: 'info', organisation: orgName, title: 'Address removed from the whitelist', detail: email });
  }

  private record(customerId: string, type: string, message: string) {
    return this.audit.logEvent({ type, requestId: uuid(), customerId, message, status: 'ok' });
  }
}
