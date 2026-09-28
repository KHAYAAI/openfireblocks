import { ForbiddenException } from '@nestjs/common';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { AgentsService } from './agents.service';
import { AgentController } from './agents.controller';
import { KeysService } from '../keys/keys.service';
import { CustomerService } from '../customers/customer.service';
import { TokenRegistryService, Token } from '../tokens/token-registry.service';

// Agents with budgets, against a real Postgres (migration 025's rules are
// triggers and a row lock; a mock would test neither). The signing route
// itself is replaced by a recorder -- it has its own tests -- so these are
// about what an agent is allowed to ask for.
//
//   eval "$(infrastructure/local/postgres-local.sh start)"
//   REQUIRE_LIVE_DB=1 npx jest src/agents

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';

let reachable = false;
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 025 is reachable');
  console.warn('skipping agents live tests -- no database with migration 025');
  return true;
}

const SUPPLIER = '0x70997970C51812dc3A010C7d01b50e0d17dc79C8';
const OTHER = '0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC';
const base: Omit<Token, 'symbol' | 'pegCurrency' | 'decimals'> = {
  tokenId: 't', chainId: 1, contractAddress: '0x1234567890123456789012345678901234567890', name: 'x', issuer: 'x',
  status: 'verified', verifiedSymbol: null as any, verifiedDecimals: null as any, verifiedAt: new Date(), verificationError: null, notes: null,
};
const TOKENS: Record<string, Token> = {
  ZARP: { ...base, symbol: 'ZARP', decimals: 18, pegCurrency: 'ZAR' },
  USDC: { ...base, symbol: 'USDC', decimals: 6, pegCurrency: 'USD' },
  WEIRD: { ...base, symbol: 'WEIRD', decimals: 18, pegCurrency: null },
};

describe('agents with budgets (live Postgres)', () => {
  let admin: Pool;
  let tenant: Pool;
  let agents: AgentsService;
  let customerId: string;
  let keyId: string;
  let userId: string;
  let signed: string[];
  let failNext = false;

  beforeAll(async () => {
    admin = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
    try {
      reachable = (await admin.query(`SELECT to_regclass('agents') IS NOT NULL AS ok`)).rows[0].ok;
    } catch {
      reachable = false;
    }
    if (!reachable) return;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN;
    tenant = new Pool({ connectionString: TENANT_DSN });
    agents = new AgentsService(tenant);
    customerId = randomUUID();
    await admin.query(
      `INSERT INTO customers (customer_id, name, email, api_key_hash, status, tier)
       VALUES ($1, 'Agentic Co', $2, decode(md5(random()::text), 'hex'), 'active', 'enterprise')`,
      [customerId, `agents-${customerId}@example.test`],
    );
    keyId = (
      await admin.query(
        `INSERT INTO key_pairs (customer_id, name, blockchain, threshold, total_parties, status, address)
         VALUES ($1, 'treasury', 'ethereum', 1, 3, 'active', '0xabc') RETURNING key_id`,
        [customerId],
      )
    ).rows[0].key_id;
    userId = (
      await admin.query(`INSERT INTO users (email, password_hash, full_name) VALUES ($1, 'x', 'Ada') RETURNING id`, [
        `ada-${customerId}@example.test`,
      ])
    ).rows[0].id;
  });

  afterAll(async () => {
    await agents?.onModuleDestroy();
    await tenant?.end();
    await admin?.end();
  });

  beforeEach(() => {
    signed = [];
    failNext = false;
  });

  function controller() {
    const keys = {
      sendToken: jest.fn(async (_c: unknown, _k: string, req: { recipient: string; amount: string }) => {
        if (failNext) {
          failNext = false;
          throw new Error('ceremony failed');
        }
        // A little latency, so concurrent requests genuinely overlap.
        await new Promise((r) => setTimeout(r, 20));
        signed.push(`${req.recipient}:${req.amount}`);
        return { transaction_hash: `0x${randomUUID().replace(/-/g, '')}` };
      }),
    } as unknown as KeysService;
    const customers = { getByCustomerId: jest.fn(async () => ({ customer_id: customerId })) } as unknown as CustomerService;
    const tokens = {
      requireTransactable: jest.fn(async (_chain: number, sym: string) => TOKENS[sym.toUpperCase()]),
    } as unknown as TokenRegistryService;
    return new AgentController(agents, keys, customers, tokens);
  }

  async function newAgent(over: Partial<Parameters<AgentsService['create']>[0]> = {}) {
    return agents.create({
      customerId,
      createdBy: userId,
      name: 'procurement bot',
      keyId,
      allowedTokens: ['ZARP', 'USDC', 'WEIRD'],
      allowedRecipients: [SUPPLIER],
      perTransferLimitZar: 5000,
      dailyLimitZar: 10000,
      expiresInDays: 30,
      ...over,
    });
  }

  const pay = (agent: any, amount: string, extra: Record<string, unknown> = {}) =>
    controller().transfer({ agent, headers: {} } as any, {
      token: 'ZARP', recipient: SUPPLIER, amount, chainId: 1, nonce: 1, gasPrice: '1', purpose: 'invoice 1043', ...extra,
    } as any);

  it('an agent key resolves only while active, and never as a customer API key', async () => {
    if (skipped()) return;
    const { agent, apiKey } = await newAgent();
    expect(apiKey).toMatch(/^ofb_agent_/);
    expect((await agents.authenticate(apiKey))?.agentId).toBe(agent.agentId);
    expect(await new CustomerService().getByApiKey(apiKey)).toBeNull();
    await agents.revoke(customerId, agent.agentId, userId);
    expect(await agents.authenticate(apiKey)).toBeNull();
  });

  it('pays within its limits, and the record shows it', async () => {
    if (skipped()) return;
    const { agent } = await newAgent();
    const out = await pay(agent, '1500');
    expect(out.agent).toMatchObject({ valueZar: 1500, remaining24hZar: 8500 });
    const got = await agents.get(customerId, agent.agentId);
    expect(got.record).toMatchObject({ transfers: 1, refused: 0, spent24hZar: 1500, remaining24hZar: 8500 });
  });

  it('refuses a token, a recipient, or an amount outside its terms -- before anything is signed', async () => {
    if (skipped()) return;
    const { agent } = await newAgent({ allowedTokens: ['ZARP'] });
    const cases: Array<[Record<string, unknown>, string, RegExp]> = [
      [{ token: 'USDC' }, '10', /token not allowed/],
      [{ recipient: OTHER }, '10', /recipient not allowed/],
      [{}, '5000.01', /over per-transfer limit/],
    ];
    for (const [extra, amount, reason] of cases) {
      const err = await pay(agent, amount, extra).catch((e) => e);
      expect(err).toBeInstanceOf(ForbiddenException);
      expect(err.message).toMatch(reason);
    }
    expect(signed).toEqual([]);
    const got = await agents.get(customerId, agent.agentId);
    expect(got.record.refused).toBe(3);
    expect(Object.keys(got.record.refusalsByReason).sort()).toEqual(
      ['over per-transfer limit', 'recipient not allowed', 'token not allowed'].sort(),
    );
  });

  it('refuses a token it cannot value, since no budget can govern it', async () => {
    if (skipped()) return;
    const { agent } = await newAgent();
    const err = await pay(agent, '1', { token: 'WEIRD' }).catch((e) => e);
    expect(err.message).toMatch(/unvalued/);
  });

  it('values a dollar stablecoin at the configured rate, or refuses it without one', async () => {
    if (skipped()) return;
    const { agent } = await newAgent();
    const err = await pay(agent, '10', { token: 'USDC' }).catch((e) => e);
    expect(err.message).toMatch(/unvalued/); // no TRAVEL_RULE_ZAR_PER_USD in this environment
  });

  it('stops at the daily budget', async () => {
    if (skipped()) return;
    const { agent } = await newAgent();
    await pay(agent, '4000');
    await pay(agent, '4000');
    const err = await pay(agent, '2500').catch((e) => e);
    expect(err.message).toMatch(/over daily budget: R2500 with R8000 already spent/);
    await pay(agent, '2000'); // exactly to the limit is allowed
    expect(signed).toHaveLength(3);
  });

  it('twelve simultaneous requests cannot overspend it: the budget is reserved under a lock', async () => {
    if (skipped()) return;
    const { agent } = await newAgent({ perTransferLimitZar: 4000, dailyLimitZar: 10000 });
    const results = await Promise.allSettled(Array.from({ length: 12 }, () => pay(agent, '4000')));
    const ok = results.filter((r) => r.status === 'fulfilled').length;
    expect(ok).toBe(2); // 2 x R4000 fits in R10000; a third would not
    expect(signed).toHaveLength(2);
    const got = await agents.get(customerId, agent.agentId);
    expect(got.record.spent24hZar).toBe(8000);
    expect(got.record.refused).toBe(10);
  });

  it('a failed signature gives the budget back', async () => {
    if (skipped()) return;
    const { agent } = await newAgent();
    failNext = true;
    await expect(pay(agent, '5000')).rejects.toThrow(/ceremony failed/);
    const got = await agents.get(customerId, agent.agentId);
    expect(got.record).toMatchObject({ transfers: 0, released: 1, spent24hZar: 0, remaining24hZar: 10000 });
  });

  it('a revoked agent cannot spend, and cannot be revived or have its terms raised', async () => {
    if (skipped()) return;
    const { agent } = await newAgent();
    await agents.revoke(customerId, agent.agentId, userId);
    await expect(pay(agent, '1')).rejects.toThrow(/revoked or expired/);
    for (const stmt of [
      `UPDATE agents SET status = 'active' WHERE agent_id = $1`,
      `UPDATE agents SET daily_limit_zar = 1000000 WHERE agent_id = $1`,
      `DELETE FROM agents WHERE agent_id = $1`,
    ]) {
      const e = await admin.query(stmt, [agent.agentId]).catch((x) => x);
      expect(['OFB03', 'OFB04']).toContain(e.code);
    }
  });

  it('what an agent did cannot be rewritten', async () => {
    if (skipped()) return;
    const { agent } = await newAgent();
    await pay(agent, '100');
    for (const stmt of [
      `UPDATE agent_spend SET value_zar = 1 WHERE agent_id = $1`,
      `UPDATE agent_spend SET status = 'released' WHERE agent_id = $1 AND status = 'spent'`,
      `DELETE FROM agent_spend WHERE agent_id = $1`,
    ]) {
      const e = await admin.query(stmt, [agent.agentId]).catch((x) => x);
      expect(['OFB03', 'OFB04']).toContain(e.code);
    }
  });

  it('an agent can only be created on an active key of its own organisation', async () => {
    if (skipped()) return;
    await expect(newAgent({ keyId: randomUUID() })).rejects.toThrow(/not an active key/);
    await expect(newAgent({ perTransferLimitZar: 20000 })).rejects.toThrow(/per-transfer limit no more than/);
    await expect(newAgent({ expiresInDays: 0 })).rejects.toThrow(/expiresInDays/);
  });
});
