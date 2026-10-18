import { BadRequestException, Injectable, OnModuleDestroy } from '@nestjs/common';
import { Pool } from 'pg';

// Travel Rule records must be kept five years (FIC Act) and no longer than needed
// (POPIA s14, GDPR art. 5(1)(e)). The database refuses every delete except one made
// by purge_travel_rule() inside a purge, and never for a record younger than five
// years (migration 037). This service is the operator's handle on that function.
export const RETENTION_FLOOR_DAYS = 1826; // five years, including a leap day

export interface PurgeResult {
  table: string;
  rows: number;
  cutoff: string;
}

@Injectable()
export class RetentionService implements OnModuleDestroy {
  // app_admin: BYPASSRLS, so one purge covers every organisation (or one, if asked).
  private readonly admin = new Pool({
    connectionString: process.env.DATABASE_ADMIN_URL ?? 'postgresql://app_admin:dev-only@localhost:5432/openfireblocks',
    max: 2,
  });

  defaultRetainDays(): number {
    const v = Number(process.env.TRAVEL_RULE_RETENTION_DAYS ?? RETENTION_FLOOR_DAYS);
    return Number.isFinite(v) && v >= RETENTION_FLOOR_DAYS ? Math.floor(v) : RETENTION_FLOOR_DAYS;
  }

  async purgeTravelRule(opts: { retainDays?: number; customerId?: string; dryRun?: boolean; reason: string }): Promise<{ dryRun: boolean; retainDays: number; results: PurgeResult[] }> {
    const retainDays = opts.retainDays ?? this.defaultRetainDays();
    if (!Number.isInteger(retainDays) || retainDays < RETENTION_FLOOR_DAYS) {
      throw new BadRequestException(`retainDays must be a whole number of at least ${RETENTION_FLOOR_DAYS} (five years, the FIC Act minimum)`);
    }
    const reason = (opts.reason ?? '').trim();
    if (reason.length < 3) throw new BadRequestException('say why this purge is being run (reason)');
    const dryRun = opts.dryRun !== false;
    const c = await this.admin.connect();
    try {
      await c.query('BEGIN');
      const r = await c.query(
        `SELECT table_name, rows_deleted, cutoff FROM purge_travel_rule(make_interval(days => $1), $2, $3, $4)`,
        [retainDays, reason, opts.customerId ?? null, dryRun],
      );
      await c.query('COMMIT');
      return {
        dryRun,
        retainDays,
        results: r.rows.map((x) => ({ table: x.table_name, rows: Number(x.rows_deleted), cutoff: new Date(x.cutoff).toISOString() })),
      };
    } catch (e) {
      await c.query('ROLLBACK').catch(() => undefined);
      if ((e as { code?: string }).code === 'OFB08') throw new BadRequestException((e as Error).message);
      throw e;
    } finally {
      c.release();
    }
  }

  async history(limit = 50) {
    const r = await this.admin.query(
      `SELECT purge_id, table_name, customer_id, cutoff, rows_deleted, dry_run, run_by, reason, created_at
         FROM retention_purges ORDER BY created_at DESC LIMIT $1`,
      [Math.min(Math.max(1, limit), 500)],
    );
    return r.rows.map((x) => ({
      purgeId: x.purge_id, table: x.table_name, customerId: x.customer_id, cutoff: new Date(x.cutoff).toISOString(),
      rows: Number(x.rows_deleted), dryRun: x.dry_run, runBy: x.run_by, reason: x.reason, at: new Date(x.created_at).toISOString(),
    }));
  }

  async onModuleDestroy() {
    await this.admin.end().catch(() => undefined);
  }
}
