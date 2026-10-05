import { Inject, Injectable, Logger, NotFoundException, UnprocessableEntityException, BadRequestException } from '@nestjs/common';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { withTenant, UUID_RE } from '../approvals/tenant-db';
import {
  configFromEnv,
  problems,
  Requirement,
  requirementFor,
  toIvms101,
  TransferFacts,
  TravelRuleConfig,
  TravelRuleInput,
} from './travel-rule';

export interface Assessment {
  requirement: Requirement;
  input: TravelRuleInput;
}

export interface TravelRuleRecordView {
  recordId: string;
  requestId: string;
  txHash: string | null;
  asset: string;
  amount: string;
  valueZar: string | null;
  valuation: string;
  beneficiaryAddress: string;
  beneficiaryUnhosted: boolean;
  transmissionStatus: string;
  transmissionReference: string | null;
  transmissionError: string | null;
  createdAt: string;
  ivms101?: unknown;
}

// Collects, keeps and transmits Travel Rule information for outbound
// transfers.
//
// Order matters, and is enforced by KeysService: assess before policy
// lets anything through to signing; record before the ceremony, so no
// transfer is ever signed without its record; transmit after, because the
// receiving provider needs the transaction hash to match it.
//
// Transmission goes to TRAVEL_RULE_PROVIDER_URL when one is configured --
// a Travel Rule network gateway (Notabene, Sumsub, TRP, or the customer's
// own) that accepts IVMS101 JSON. Without one, records wait as
// awaiting_transmission for the compliance team's export, and GET
// /travel-rule/records?status=awaiting_transmission lists them. That is
// not "done": the transfer is signed and the information is kept, but the
// counterparty has not received it until somebody sends it.
@Injectable()
export class TravelRuleService {
  private readonly logger = new Logger(TravelRuleService.name);
  private readonly cfg: TravelRuleConfig;

  constructor(@Inject(PG_POOL) private readonly pool: Pool) {
    this.cfg = configFromEnv();
  }

  // Throws 422 naming what is missing when the rule applies and the
  // information is not enough. Returns null when the rule does not apply.
  assess(t: TransferFacts, input: TravelRuleInput | undefined): Assessment | null {
    const requirement = requirementFor(t, this.cfg);
    if (!requirement.required) return null;
    const missing = problems(input);
    if (missing.length) {
      throw new UnprocessableEntityException({
        message: 'Travel Rule information is required for this transfer',
        reason: requirement.reason,
        missing,
      });
    }
    return { requirement, input: input! };
  }

  // Written before the signing ceremony. Returns the record id.
  async recordBeforeSigning(args: {
    customerId: string;
    customerName: string;
    requestId: string;
    chainId: number;
    facts: TransferFacts;
    originatorAddress: string;
    beneficiaryAddress: string;
    assessment: Assessment;
  }): Promise<string> {
    const { assessment: a } = args;
    const ivms = toIvms101(
      a.input,
      { ...args.facts, chainId: args.chainId, originatorAddress: args.originatorAddress, beneficiaryAddress: args.beneficiaryAddress },
      { name: args.customerName },
    );
    const unhosted = Boolean(a.input.beneficiaryUnhosted);
    const r = await withTenant(this.pool, args.customerId, (c) =>
      c.query(
        `INSERT INTO travel_rule_records
           (customer_id, request_id, chain_id, asset, amount, asset_decimals, originator_address,
            beneficiary_address, value_zar, valuation, threshold_zar, ivms101, beneficiary_unhosted,
            transmission_status)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
         ON CONFLICT (customer_id, request_id) DO UPDATE SET request_id = EXCLUDED.request_id
         RETURNING record_id`,
        [
          args.customerId,
          args.requestId,
          args.chainId,
          args.facts.asset,
          args.facts.amount,
          args.facts.decimals,
          args.originatorAddress.toLowerCase(),
          args.beneficiaryAddress.toLowerCase(),
          a.requirement.valueZar,
          a.requirement.valuation,
          this.cfg.thresholdZar,
          JSON.stringify(ivms),
          unhosted,
          unhosted ? 'not_applicable_unhosted' : 'awaiting_transmission',
        ],
      ),
    );
    return r.rows[0].record_id;
  }

  // After signing: attach the hash and, if a provider is configured and
  // there is a provider to send to, transmit. Never throws for a
  // transmission failure -- the transfer is signed, the information is
  // kept, and the failure is recorded and reported for a retry.
  async completeAfterSigning(customerId: string, recordId: string, txHash: string): Promise<{ status: string; reference?: string; error?: string }> {
    const rec = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `UPDATE travel_rule_records SET tx_hash = $2 WHERE record_id = $1
         RETURNING transmission_status, ivms101`,
        [recordId, txHash],
      ),
    );
    const row = rec.rows[0];
    if (row.transmission_status !== 'awaiting_transmission') return { status: row.transmission_status };
    return this.transmit(customerId, recordId, row.ivms101, txHash);
  }

  // Sends one record to the configured provider and records the outcome. Used
  // right after signing and again for a retry. Never throws for a provider
  // failure: the information is kept and the failure recorded.
  private async transmit(customerId: string, recordId: string, ivms101: unknown, txHash: string | null): Promise<{ status: string; reference?: string; error?: string }> {
    const url = process.env.TRAVEL_RULE_PROVIDER_URL;
    if (!url) return { status: 'awaiting_transmission' };

    try {
      const res = await fetch(url, {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          // The same record always carries the same key, so a retry after a
          // timeout cannot make the provider file it twice.
          'idempotency-key': recordId,
          ...(process.env.TRAVEL_RULE_PROVIDER_TOKEN ? { authorization: `Bearer ${process.env.TRAVEL_RULE_PROVIDER_TOKEN}` } : {}),
        },
        body: JSON.stringify({ ivms101, txHash }),
        signal: AbortSignal.timeout(10000),
      });
      const text = await res.text();
      if (!res.ok) throw new Error(`provider answered ${res.status}: ${text.slice(0, 300)}`);
      let reference = '';
      try {
        reference = String(JSON.parse(text).reference ?? '');
      } catch {
        reference = '';
      }
      await this.mark(customerId, recordId, 'transmitted', reference || null, null);
      return { status: 'transmitted', reference };
    } catch (err) {
      const message = (err as Error).message;
      this.logger.error(`Travel Rule transmission for ${recordId} failed: ${message}`);
      await this.mark(customerId, recordId, 'failed', null, message);
      return { status: 'failed', error: message };
    }
  }

  // Try again for a record that is waiting or whose last attempt failed.
  // A record already transmitted is left alone: sending it twice is how a
  // counterparty ends up with two copies.
  async retransmit(customerId: string, recordId: string): Promise<TravelRuleRecordView> {
    if (!UUID_RE.test(recordId)) throw new NotFoundException('record not found');
    if (!process.env.TRAVEL_RULE_PROVIDER_URL) {
      throw new UnprocessableEntityException('no Travel Rule provider is configured (TRAVEL_RULE_PROVIDER_URL), so there is nowhere to send it; record it as sent elsewhere instead');
    }
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT transmission_status, ivms101, tx_hash FROM travel_rule_records WHERE record_id = $1 AND customer_id = $2`, [recordId, customerId]),
    );
    const row = r.rows[0];
    if (!row) throw new NotFoundException('record not found');
    if (row.transmission_status === 'transmitted') throw new UnprocessableEntityException('this record has already been transmitted');
    await this.transmit(customerId, recordId, row.ivms101, row.tx_hash);
    return this.get(customerId, recordId);
  }

  private async mark(customerId: string, recordId: string, status: string, reference: string | null, error: string | null) {
    await withTenant(this.pool, customerId, (c) =>
      c.query(
        `UPDATE travel_rule_records
            SET transmission_status = $2::text, transmission_reference = $3, transmission_error = $4,
                transmitted_at = CASE WHEN $2::text = 'transmitted' THEN NOW() ELSE transmitted_at END
          WHERE record_id = $1`,
        [recordId, status, reference, error],
      ),
    );
  }

  // For transmission done outside the platform (a provider portal, an
  // export): the compliance team records that it was sent, and where.
  async markTransmitted(customerId: string, recordId: string, reference: string): Promise<TravelRuleRecordView> {
    if (!UUID_RE.test(recordId)) throw new NotFoundException('record not found');
    if (!reference?.trim()) throw new BadRequestException('reference is required: where, or under what id, it was sent');
    try {
      await this.mark(customerId, recordId, 'transmitted', reference.trim(), null);
    } catch (err) {
      if ((err as { code?: string }).code === 'OFB03') throw new UnprocessableEntityException((err as Error).message);
      throw err;
    }
    return this.get(customerId, recordId);
  }

  async list(customerId: string, status?: string): Promise<TravelRuleRecordView[]> {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(
        `SELECT * FROM travel_rule_records WHERE customer_id = $1 AND ($2::text IS NULL OR transmission_status = $2)
          ORDER BY created_at DESC LIMIT 200`,
        [customerId, status ?? null],
      ),
    );
    return r.rows.map((row) => view(row, false));
  }

  async get(customerId: string, recordId: string): Promise<TravelRuleRecordView> {
    if (!UUID_RE.test(recordId)) throw new NotFoundException('record not found');
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query(`SELECT * FROM travel_rule_records WHERE record_id = $1`, [recordId]),
    );
    if (!r.rows[0]) throw new NotFoundException('record not found');
    return view(r.rows[0], true);
  }
}

function view(row: Record<string, any>, full: boolean): TravelRuleRecordView {
  return {
    recordId: row.record_id,
    requestId: row.request_id,
    txHash: row.tx_hash,
    asset: row.asset,
    amount: String(row.amount),
    valueZar: row.value_zar === null ? null : String(row.value_zar),
    valuation: row.valuation,
    beneficiaryAddress: row.beneficiary_address,
    beneficiaryUnhosted: row.beneficiary_unhosted,
    transmissionStatus: row.transmission_status,
    transmissionReference: row.transmission_reference,
    transmissionError: row.transmission_error,
    createdAt: new Date(row.created_at).toISOString(),
    ...(full ? { ivms101: row.ivms101 } : {}),
  };
}
