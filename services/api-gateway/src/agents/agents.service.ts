import {
  BadRequestException,
  ForbiddenException,
  Inject,
  Injectable,
  NotFoundException,
} from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { withTenant, UUID_RE } from '../approvals/tenant-db';
import { generateApiKey, hashApiKey } from '../auth/api-key.util';
import { configFromEnv, TravelRuleConfig, valueInZar } from '../travel-rule/travel-rule';

export interface Agent {
  agentId: string;
  customerId: string;
  name: string;
  keyId: string;
  allowedTokens: string[];
  allowedRecipients: string[] | null;
  perTransferLimitZar: number;
  dailyLimitZar: number;
  status: 'active' | 'revoked';
  expiresAt: string;
  createdBy: string;
  createdAt: string;
  revokedAt: string | null;
}

export interface AgentRecord {
  transfers: number;
  refused: number;
  released: number;
  spent24hZar: number;
  remaining24hZar: number;
  refusalsByReason: Record<string, number>;
}

function toAgent(row: Record<string, any>): Agent {
  return {
    agentId: row.agent_id,
    customerId: row.customer_id,
    name: row.name,
    keyId: row.key_id,
    allowedTokens: row.allowed_tokens,
    allowedRecipients: row.allowed_recipients,
    perTransferLimitZar: Number(row.per_transfer_limit_zar),
    dailyLimitZar: Number(row.daily_limit_zar),
    status: row.status,
    expiresAt: new Date(row.expires_at).toISOString(),
    createdBy: row.created_by,
    createdAt: new Date(row.created_at).toISOString(),
    revokedAt: row.revoked_at ? new Date(row.revoked_at).toISOString() : null,
  };
}

const ADDRESS = /^0x[0-9a-fA-F]{40}$/;

// Agents: credentials that can pay, and only pay, within limits.
//
// The limits are enforced in reserve(), in one transaction holding the
// agent's row lock: check the terms, sum the last 24 hours, and write a
// reservation, all before anything is signed. Two requests from the same
// agent at the same moment are therefore serialised -- the second sees
// the first's reservation -- so an agent cannot overspend by asking
// twice at once. A reservation whose signing fails is released.
@Injectable()
export class AgentsService {
  private readonly cfg: TravelRuleConfig = configFromEnv();
  private adminPool: Pool | null = null;

  constructor(@Inject(PG_POOL) private readonly pool: Pool) {}

  private admin(): Pool {
    if (!this.adminPool) {
      this.adminPool = new Pool({
        connectionString: process.env.DATABASE_ADMIN_URL ?? 'postgresql://app_admin:dev-only@localhost:5432/openfireblocks',
      });
    }
    return this.adminPool;
  }

  async onModuleDestroy() {
    await this.adminPool?.end().catch(() => undefined);
  }

  // ------------------------------------------------------------ admin side

  async create(input: {
    customerId: string;
    createdBy: string;
    name: string;
    keyId: string;
    allowedTokens: string[];
    allowedRecipients?: string[] | null;
    perTransferLimitZar: number;
    dailyLimitZar: number;
    expiresInDays: number;
  }): Promise<{ agent: Agent; apiKey: string }> {
    if (!input.name?.trim()) throw new BadRequestException('name is required');
    if (!UUID_RE.test(input.keyId)) throw new BadRequestException('keyId must be a key id');
    if (!Array.isArray(input.allowedTokens) || input.allowedTokens.length === 0) {
      throw new BadRequestException('allowedTokens must name at least one token');
    }
    const recipients = input.allowedRecipients ?? null;
    if (recipients && (!Array.isArray(recipients) || recipients.some((r) => !ADDRESS.test(r)))) {
      throw new BadRequestException('allowedRecipients must be 20-byte hex addresses');
    }
    if (!(input.perTransferLimitZar > 0) || !(input.dailyLimitZar > 0) || input.perTransferLimitZar > input.dailyLimitZar) {
      throw new BadRequestException('limits must be positive, and the per-transfer limit no more than the daily one');
    }
    if (!Number.isInteger(input.expiresInDays) || input.expiresInDays < 1 || input.expiresInDays > 365) {
      // A credential that can move money should not live forever.
      throw new BadRequestException('expiresInDays must be 1 to 365');
    }
    const key = await withTenant(this.pool, input.customerId, (c) =>
      c.query(`SELECT key_id FROM key_pairs WHERE key_id = $1 AND customer_id = $2 AND status = 'active'`, [
        input.keyId,
        input.customerId,
      ]),
    );
    if (!key.rows[0]) throw new BadRequestException('keyId is not an active key of this organisation');

    // Prefixed so an agent key is recognisable at a glance, in a log or a
    // leaked config, as a spending credential.
    const apiKey = generateApiKey().replace(/^ofb_/, 'ofb_agent_');
    const r = await withTenant(this.pool, input.customerId, (c) =>
      c.query(
        `INSERT INTO agents (customer_id, name, key_id, api_key_hash, allowed_tokens, allowed_recipients,
           per_transfer_limit_zar, daily_limit_zar, expires_at, created_by)
         VALUES ($1, $2, $3, decode($4, 'hex'), $5, $6, $7, $8, NOW() + make_interval(days => $9), $10)
         RETURNING *`,
        [
          input.customerId,
          input.name.trim(),
          input.keyId,
          hashApiKey(apiKey),
          input.allowedTokens.map((t) => t.toUpperCase()),
          recipients ? recipients.map((r) => r.toLowerCase()) : null,
          input.perTransferLimitZar,
          input.dailyLimitZar,
          input.expiresInDays,
          input.createdBy,
        ],
      ),
    );
    return { agent: toAgent(r.rows[0]), apiKey };
  }

  async list(customerId: string): Promise<Agent[]> {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT * FROM agents WHERE customer_id = $1 ORDER BY created_at DESC`, [customerId]),
    );
    return r.rows.map(toAgent);
  }

  async get(customerId: string, agentId: string): Promise<Agent & { record: AgentRecord }> {
    if (!UUID_RE.test(agentId)) throw new NotFoundException('agent not found');
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM agents WHERE agent_id = $1`, [agentId]));
    if (!r.rows[0]) throw new NotFoundException('agent not found');
    const agent = toAgent(r.rows[0]);
    return { ...agent, record: await this.record(customerId, agent) };
  }

  async revoke(customerId: string, agentId: string, userId: string): Promise<Agent> {
    if (!UUID_RE.test(agentId)) throw new NotFoundException('agent not found');
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `UPDATE agents SET status = 'revoked', revoked_by = $2, revoked_at = NOW()
          WHERE agent_id = $1 AND status = 'active' RETURNING *`,
        [agentId, userId],
      ),
    );
    if (!r.rows[0]) return (await this.get(customerId, agentId)) as Agent;
    return toAgent(r.rows[0]);
  }

  // The agent's track record: what it did, and what it was refused.
  async record(customerId: string, agent: Agent): Promise<AgentRecord> {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT status, reason, value_zar, created_at > NOW() - interval '24 hours' AS recent
           FROM agent_spend WHERE agent_id = $1`,
        [agent.agentId],
      ),
    );
    const out: AgentRecord = { transfers: 0, refused: 0, released: 0, spent24hZar: 0, remaining24hZar: 0, refusalsByReason: {} };
    for (const row of r.rows) {
      if (row.status === 'spent') out.transfers++;
      if (row.status === 'released') out.released++;
      if (row.status === 'refused') {
        out.refused++;
        const key = String(row.reason).split(':')[0];
        out.refusalsByReason[key] = (out.refusalsByReason[key] ?? 0) + 1;
      }
      if (row.recent && (row.status === 'spent' || row.status === 'reserved')) out.spent24hZar += Number(row.value_zar);
    }
    out.spent24hZar = Math.round(out.spent24hZar * 100) / 100;
    out.remaining24hZar = Math.max(0, Math.round((agent.dailyLimitZar - out.spent24hZar) * 100) / 100);
    return out;
  }

  // ------------------------------------------------------------ agent side

  // Resolves an agent key, before any tenant is known (hence the admin
  // pool, as for customers). Revoked or expired agents do not resolve.
  async authenticate(apiKey: string): Promise<Agent | null> {
    if (!apiKey.startsWith('ofb_agent_')) return null;
    const r = await this.admin().query(
      `SELECT a.* FROM agents a JOIN customers c ON c.customer_id = a.customer_id
        WHERE a.api_key_hash = decode($1, 'hex') AND a.status = 'active' AND a.expires_at > NOW()
          AND c.status = 'active'`,
      [hashApiKey(apiKey)],
    );
    return r.rows[0] ? toAgent(r.rows[0]) : null;
  }

  // Checks the agent's terms and reserves the value against its budget.
  // Refusals are recorded too -- they are half of the track record -- and
  // thrown as 403 with the reason.
  async reserve(agent: Agent, t: {
    requestId: string;
    token: string;
    tokenDecimals: number;
    tokenPeg: string | null;
    recipient: string;
    amountBaseUnits: string;
    purpose?: string;
  }): Promise<{ spendId: string; valueZar: number; remaining24hZar: number }> {
    const valueZar = valueInZar({ asset: t.token, amount: t.amountBaseUnits, decimals: t.tokenDecimals, pegCurrency: t.tokenPeg }, this.cfg);
    const row = [agent.agentId, agent.customerId, t.requestId, t.token.toUpperCase(), t.recipient.toLowerCase(), t.amountBaseUnits, valueZar];

    // Decide and reserve under the agent's row lock. A refusal writes
    // nothing here; it is recorded below, after the lock is released.
    const outcome = await withTenant(this.pool, agent.customerId, async (c) => {
      const locked = await c.query(`SELECT * FROM agents WHERE agent_id = $1 FOR UPDATE`, [agent.agentId]);
      const current = locked.rows[0] ? toAgent(locked.rows[0]) : null;

      if (!current || current.status !== 'active' || new Date(current.expiresAt) <= new Date()) {
        return { refused: 'revoked or expired: this agent can no longer spend' };
      }
      if (!current.allowedTokens.includes(t.token.toUpperCase())) {
        return { refused: `token not allowed: ${t.token} is not one of ${current.allowedTokens.join(', ')}` };
      }
      if (current.allowedRecipients && !current.allowedRecipients.includes(t.recipient.toLowerCase())) {
        return { refused: `recipient not allowed: ${t.recipient} is not on this agent's list` };
      }
      if (valueZar === null) {
        return { refused: `unvalued: ${t.token} cannot be valued in rand, so no budget can govern it` };
      }
      if (valueZar > current.perTransferLimitZar) {
        return { refused: `over per-transfer limit: R${valueZar} exceeds R${current.perTransferLimitZar}` };
      }
      const spent = await c.query(
        `SELECT COALESCE(SUM(value_zar), 0) AS total FROM agent_spend
          WHERE agent_id = $1 AND status IN ('reserved', 'spent') AND created_at > NOW() - interval '24 hours'`,
        [agent.agentId],
      );
      const total = Number(spent.rows[0].total);
      if (total + valueZar > current.dailyLimitZar) {
        return { refused: `over daily budget: R${valueZar} with R${total} already spent in 24 hours exceeds R${current.dailyLimitZar}` };
      }
      const ins = await c.query(
        `INSERT INTO agent_spend (agent_id, customer_id, request_id, token, recipient, amount, value_zar, status, purpose)
         VALUES ($1,$2,$3,$4,$5,$6,$7,'reserved',$8) RETURNING spend_id`,
        [...row, t.purpose ?? null],
      );
      return {
        spendId: ins.rows[0].spend_id as string,
        valueZar,
        remaining24hZar: Math.round((current.dailyLimitZar - total - valueZar) * 100) / 100,
      };
    });

    if ('refused' in outcome) {
      // Refusals are half of the track record, so they are kept.
      await withTenant(this.pool, agent.customerId, (c) =>
        c.query(
          `INSERT INTO agent_spend (agent_id, customer_id, request_id, token, recipient, amount, value_zar, status, reason, purpose)
           VALUES ($1,$2,$3,$4,$5,$6,$7,'refused',$8,$9)`,
          [...row, outcome.refused, t.purpose ?? null],
        ),
      );
      throw new ForbiddenException(`agent refused: ${outcome.refused}`);
    }
    return outcome as { spendId: string; valueZar: number; remaining24hZar: number };
  }

  async settle(agent: Agent, spendId: string, outcome: { txHash: string } | { failed: string }) {
    await withTenant(this.pool, agent.customerId, (c) =>
      'txHash' in outcome
        ? c.query(`UPDATE agent_spend SET status = 'spent', tx_hash = $2, settled_at = NOW() WHERE spend_id = $1`, [spendId, outcome.txHash])
        : c.query(`UPDATE agent_spend SET status = 'released', reason = $2, settled_at = NOW() WHERE spend_id = $1`, [spendId, outcome.failed]),
    );
  }
}
