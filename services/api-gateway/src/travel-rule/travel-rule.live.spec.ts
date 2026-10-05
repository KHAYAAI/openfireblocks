import { UnprocessableEntityException } from '@nestjs/common';
import { Wallet } from 'ethers';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { KeysService } from '../keys/keys.service';
import { PostgresService } from '../database/postgres.service';
import { PolicyService } from '../policies/policy.service';
import { KeysTemporalService } from '../keys/keys-temporal.service';
import { TokenRegistryService, Token } from '../tokens/token-registry.service';
import { EvmRpcService } from '../tokens/evm-rpc.service';
import { Customer } from '../customers/customer.service';
import { TravelRuleService } from './travel-rule.service';
import { TravelRuleInput } from './travel-rule';

// The Travel Rule through the real signing routes: KeysService.sendToken
// and signTransaction, with a real TravelRuleService writing to a real
// Postgres (migration 024's immutability rules are triggers, so a mock
// would test nothing). The signing ceremony itself is simulated with a
// local key, as in token-transfer.spec.ts.
//
//   eval "$(infrastructure/local/postgres-local.sh start)"
//   REQUIRE_LIVE_DB=1 npx jest src/travel-rule

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN =
  process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const PROVIDER = 'https://travel-rule-provider.test/transfers';

let reachable = false;
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 024 is reachable');
  console.warn('skipping Travel Rule live tests -- no database with migration 024');
  return true;
}

const wallet = new Wallet('0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d');
const RECIPIENT = '0x70997970C51812dc3A010C7d01b50e0d17dc79C8';
const ZARP: Token = {
  tokenId: 'tok-zarp',
  chainId: 1,
  contractAddress: '0x1234567890123456789012345678901234567890',
  symbol: 'ZARP',
  name: 'ZARP Stablecoin',
  decimals: 18,
  pegCurrency: 'ZAR',
  issuer: 'ZARP',
  status: 'verified',
  verifiedSymbol: 'ZARP',
  verifiedDecimals: 18,
  verifiedAt: new Date(),
  verificationError: null,
  notes: null,
};

const INFO: TravelRuleInput = {
  originator: {
    legalPerson: {
      name: 'Khaya Demo Bank Treasury',
      nationalIdentification: { nationalIdentifier: '5493001KJTIIGC8Y1R12', nationalIdentifierType: 'LEIX' },
    },
  },
  beneficiary: { legalPerson: { name: 'Acme Suppliers (Pty) Ltd' } },
  beneficiaryVasp: { name: 'Example Exchange', lei: '549300EXAMPLE0000000' },
};

describe('Travel Rule on outbound transfers (live Postgres)', () => {
  let admin: Pool;
  let tenant: Pool;
  let customer: Customer;
  let travelRule: TravelRuleService;
  let providerCalls: Array<{ ivms101: any; txHash: string }>;
  let providerStatus = 200;
  let ceremonies = 0;

  beforeAll(async () => {
    admin = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
    try {
      const r = await admin.query(`SELECT to_regclass('travel_rule_records') IS NOT NULL AS ok`);
      reachable = r.rows[0].ok;
    } catch {
      reachable = false;
    }
    if (!reachable) return;
    tenant = new Pool({ connectionString: TENANT_DSN });
    const id = randomUUID();
    await admin.query(
      `INSERT INTO customers (customer_id, name, email, api_key_hash, status, tier)
       VALUES ($1, 'Khaya Demo Bank', $2, decode(md5(random()::text), 'hex'), 'active', 'enterprise')`,
      [id, `tr-${id}@example.test`],
    );
    customer = {
      customer_id: id,
      name: 'Khaya Demo Bank',
      email: 'tr@example.test',
      status: 'active',
      tier: 'enterprise',
      policies: {},
      raw_digest_signing_enabled: false,
      arbitrary_contract_calls_enabled: false,
    };
    travelRule = new TravelRuleService(tenant);
  });

  afterAll(async () => {
    await tenant?.end();
    await admin?.end();
  });

  beforeEach(() => {
    providerCalls = [];
    providerStatus = 200;
    ceremonies = 0;
    delete process.env.TRAVEL_RULE_PROVIDER_URL;
    // Party health probes answer ok; the provider records what it gets.
    global.fetch = jest.fn(async (url: string, init?: RequestInit) => {
      if (String(url) === PROVIDER) {
        providerCalls.push(JSON.parse(String(init?.body)));
        const body = providerStatus === 200 ? JSON.stringify({ reference: `tr-${providerCalls.length}` }) : 'provider down';
        return new Response(body, { status: providerStatus });
      }
      return { ok: true } as Response;
    }) as unknown as typeof fetch;
  });

  function service() {
    const postgres = {
      getKey: jest.fn().mockResolvedValue({
        key_id: 'key-1', status: 'active', threshold: 2, total_parties: 3, blockchain: 'ethereum',
        address: wallet.address, public_key: 'ff'.repeat(33),
      }),
      getCompletedCeremonyForKey: jest.fn().mockResolvedValue({ ceremony_id: 'cer-1', threshold: 2, total_parties: 3 }),
      findSigningRequestByIdempotencyKey: jest.fn().mockResolvedValue(null),
      createSigningRequest: jest.fn().mockResolvedValue(undefined),
      completeSigningRequest: jest.fn().mockResolvedValue(undefined),
      failSigningRequest: jest.fn().mockResolvedValue(undefined),
      recordTransfer: jest.fn().mockResolvedValue(undefined),
    } as unknown as PostgresService;
    const temporal = {
      signWithThreshold: jest.fn().mockImplementation(async ({ message }: { message: string }) => {
        ceremonies++;
        const sig = wallet.signingKey.sign('0x' + message);
        return { status: 'completed', signature: sig.r.slice(2) + sig.s.slice(2) + (sig.yParity === 0 ? '00' : '01') };
      }),
    } as unknown as KeysTemporalService;
    const policy = {
      evaluate: jest.fn().mockResolvedValue({ approved: true, denials: [], requiresApproval: false, reason: 'x' }),
    } as unknown as PolicyService;
    const registry = {
      requireTransactable: jest.fn(async () => ZARP),
      byContract: jest.fn(async () => ZARP),
      bySymbol: jest.fn(async () => ZARP),
      list: jest.fn(async () => [ZARP]),
    } as unknown as TokenRegistryService;
    const rpc = { configured: () => true, call: jest.fn(), provider: jest.fn() } as unknown as EvmRpcService;
    return new KeysService(postgres, temporal, policy, undefined, registry, rpc, travelRule);
  }

  const send = (amount: string, travel?: TravelRuleInput) =>
    service().sendToken(customer, 'key-1', {
      token: 'ZARP', recipient: RECIPIENT, amount, chainId: 1, nonce: Math.floor(Math.random() * 1e6),
      gasPrice: '20000000000', idempotencyKey: randomUUID(), travelRule: travel,
    } as any);

  async function record(id: string) {
    const r = await admin.query(`SELECT * FROM travel_rule_records WHERE record_id = $1`, [id]);
    return r.rows[0];
  }

  it('below the threshold, nothing is asked and nothing recorded', async () => {
    if (skipped()) return;
    const out = await send('4999.99');
    expect(out.travel_rule).toBeUndefined();
    expect(ceremonies).toBe(1);
  });

  it('at the threshold without the information, the transfer is refused before any signing', async () => {
    if (skipped()) return;
    const err = await send('5000').catch((e) => e);
    expect(err).toBeInstanceOf(UnprocessableEntityException);
    expect(err.getResponse().missing.join()).toMatch(/travelRule is required/);
    expect(err.getResponse().reason).toMatch(/R5000/);
    expect(ceremonies).toBe(0);
  });

  it('incomplete information is refused, naming what is missing', async () => {
    if (skipped()) return;
    const err = await send('10000', { ...INFO, originator: { legalPerson: { name: 'Khaya' } } }).catch((e) => e);
    expect(err.getResponse().missing.join()).toMatch(/originator: one of/);
    expect(ceremonies).toBe(0);
  });

  it('with the information and no provider, it signs and keeps the record awaiting transmission', async () => {
    if (skipped()) return;
    const out = await send('10000', INFO);
    expect(ceremonies).toBe(1);
    expect(out.travel_rule).toMatchObject({ status: 'awaiting_transmission' });
    const row = await record(out.travel_rule!.record_id);
    expect(row.tx_hash).toBe(out.transaction_hash);
    expect(row.value_zar).toBe('10000.00');
    expect(row.valuation).toBe('zar-peg');
    // As given (EIP-55), for the receiving provider; the indexed columns
    // are lower-cased for matching.
    expect(row.ivms101.originator.accountNumber).toEqual([wallet.address]);
    expect(row.originator_address).toBe(wallet.address.toLowerCase());
    expect(row.ivms101.beneficiary.accountNumber).toEqual([RECIPIENT]);
    expect(row.ivms101.originatingVASP.name).toBe('Khaya Demo Bank');
  });

  it('with a provider configured, it transmits the IVMS101 payload with the transaction hash', async () => {
    if (skipped()) return;
    process.env.TRAVEL_RULE_PROVIDER_URL = PROVIDER;
    const out = await send('25000', INFO);
    expect(out.travel_rule).toMatchObject({ status: 'transmitted', reference: 'tr-1' });
    expect(providerCalls).toHaveLength(1);
    expect(providerCalls[0].txHash).toBe(out.transaction_hash);
    expect(providerCalls[0].ivms101.beneficiaryVASP.lei).toBe('549300EXAMPLE0000000');
    const row = await record(out.travel_rule!.record_id);
    expect(row.transmission_status).toBe('transmitted');
    expect(row.transmission_reference).toBe('tr-1');
  });

  it('a failed transmission does not undo the signature: it is recorded as failed, for a retry', async () => {
    if (skipped()) return;
    process.env.TRAVEL_RULE_PROVIDER_URL = PROVIDER;
    providerStatus = 503;
    const out = await send('25000', INFO);
    expect(out.raw_transaction).toMatch(/^0x/);
    expect(out.travel_rule).toMatchObject({ status: 'failed' });
    const row = await record(out.travel_rule!.record_id);
    expect(row.transmission_error).toMatch(/503/);

    // Sent by hand afterwards, and recorded.
    const marked = await travelRule.markTransmitted(customer.customer_id, row.record_id, 'portal ref 7781');
    expect(marked.transmissionStatus).toBe('transmitted');
  });

  it('a failed transmission can be retried through the provider, once, and the retry cannot be filed twice', async () => {
    if (skipped()) return;
    process.env.TRAVEL_RULE_PROVIDER_URL = PROVIDER;
    providerStatus = 503;
    const out = await send('26000', INFO);
    const id = out.travel_rule!.record_id;
    expect((await record(id)).transmission_status).toBe('failed');

    // Still down: stays failed, error refreshed.
    const still = await travelRule.retransmit(customer.customer_id, id);
    expect(still.transmissionStatus).toBe('failed');

    // Back up: transmitted, and the provider was given the record's id as its
    // idempotency key both times.
    providerStatus = 200;
    const done = await travelRule.retransmit(customer.customer_id, id);
    expect(done.transmissionStatus).toBe('transmitted');
    expect(providerCalls.at(-1)!.txHash).toBe((await record(id)).tx_hash);

    // Already sent: refused, and nothing further reaches the provider.
    const before = providerCalls.length;
    await expect(travelRule.retransmit(customer.customer_id, id)).rejects.toThrow(/already been transmitted/);
    expect(providerCalls).toHaveLength(before);
  });

  it('with no provider configured a retry says so instead of pretending', async () => {
    if (skipped()) return;
    delete process.env.TRAVEL_RULE_PROVIDER_URL;
    const out = await send('27000', INFO);
    await expect(travelRule.retransmit(customer.customer_id, out.travel_rule!.record_id)).rejects.toThrow(/no Travel Rule provider/);
  });

  it('an unhosted beneficiary is recorded, and nothing is transmitted', async () => {
    if (skipped()) return;
    process.env.TRAVEL_RULE_PROVIDER_URL = PROVIDER;
    const { beneficiaryVasp, ...rest } = INFO;
    void beneficiaryVasp;
    const out = await send('10000', { ...rest, beneficiaryUnhosted: true });
    expect(out.travel_rule).toMatchObject({ status: 'not_applicable_unhosted' });
    expect(providerCalls).toHaveLength(0);
  });

  it('native ETH has no price source here, so even a small transfer needs the information', async () => {
    if (skipped()) return;
    const err = await service()
      .signTransaction(customer, 'key-1', {
        to: RECIPIENT, value: '1000', gasLimit: 21000, nonce: 1, chainId: 1, gasPrice: '1', idempotencyKey: randomUUID(),
      } as any)
      .catch((e) => e);
    expect(err).toBeInstanceOf(UnprocessableEntityException);
    expect(err.getResponse().reason).toMatch(/treated as over/);
    expect(ceremonies).toBe(0);
  });

  it('what was declared cannot be edited or deleted afterwards, even by the admin role', async () => {
    if (skipped()) return;
    const out = await send('10000', INFO);
    const id = out.travel_rule!.record_id;
    for (const stmt of [
      `UPDATE travel_rule_records SET ivms101 = '{}' WHERE record_id = $1`,
      `UPDATE travel_rule_records SET beneficiary_address = '0xdead' WHERE record_id = $1`,
      `UPDATE travel_rule_records SET tx_hash = '0xother' WHERE record_id = $1`,
      `DELETE FROM travel_rule_records WHERE record_id = $1`,
    ]) {
      const e = await admin.query(stmt, [id]).catch((x) => x);
      expect(e.code).toBe('OFB04');
    }
    await travelRule.markTransmitted(customer.customer_id, id, 'sent');
    const back = await admin
      .query(`UPDATE travel_rule_records SET transmission_status = 'awaiting_transmission' WHERE record_id = $1`, [id])
      .catch((x) => x);
    expect(back.code).toBe('OFB03');
  });

  it('lists what still has to be sent', async () => {
    if (skipped()) return;
    await send('10000', INFO);
    const pending = await travelRule.list(customer.customer_id, 'awaiting_transmission');
    expect(pending.length).toBeGreaterThan(0);
    expect(pending.every((r) => r.transmissionStatus === 'awaiting_transmission')).toBe(true);
  });
});
