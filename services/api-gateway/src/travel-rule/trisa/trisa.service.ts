import { BadRequestException, ConflictException, Inject, Injectable, Logger, NotFoundException, OnModuleDestroy, OnModuleInit, Optional, UnprocessableEntityException } from '@nestjs/common';
import * as grpc from '@grpc/grpc-js';
import { createPublicKey, X509Certificate } from 'crypto';
import { Pool } from 'pg';
import { PG_POOL } from '../../database/pg-pool.token';
import { UUID_RE, withTenant } from '../../approvals/tenant-db';
import { AlertsService } from '../../controls/alerts.service';
import type { Ivms101Payload } from '../travel-rule';
import { EnvelopeError, openEnvelope, publicKeySignature, sealEnvelope } from './trisa-crypto';
import { decodePayload, encodePayload, toIdentityPayload, toTransaction, TransactionFacts } from './trisa-identity';
import { inboundHandlers } from './trisa-inbound';
import { protocol } from './trisa-protocol';
import { TrisaConfig, trisaConfigFromEnv } from './trisa.config';

export interface CounterpartyInput { name: string; lei?: string; endpoint: string; commonName?: string; sealingPublicKeyPem?: string }
export interface TrisaOutcome { status: 'transmitted' | 'failed'; reference?: string; error?: string }

const CALL_TIMEOUT_MS = 15000;

// Direct Travel Rule exchange with other providers over TRISA: mutual-TLS gRPC, and a
// SecureEnvelope only the recipient can open. Sits beside the provider-gateway
// transport; a transfer whose beneficiary provider is a trusted TRISA counterparty goes
// this way, anything else falls back to the provider URL or waits for export.
//
// What it is, plainly: the TRISA wire protocol (checked against TRISA's reference
// implementation, see trisa-interop.spec.ts), mTLS on both sides, and a counterparty
// list this organisation maintains. What it is not: membership of the TRISA network.
// That needs certificates issued by TRISA's certificate authority, which a provider
// obtains by registering with the TRISA directory; this module takes the certificates
// it is given (TRISA_CERT_FILE and friends) and does not discover counterparties
// through the directory, so each is added by hand and trusted by a second person.
@Injectable()
export class TrisaService implements OnModuleInit, OnModuleDestroy {
  private readonly logger = new Logger(TrisaService.name);
  private cfg: TrisaConfig | null | undefined;
  private server?: grpc.Server;
  private adminPool?: Pool;
  boundPort?: number;

  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    @Optional() private readonly alerts?: AlertsService,
  ) {}

  // Supplies the node identity directly instead of from the environment: for tests,
  // and for hosts that keep their keys somewhere other than files.
  useConfig(cfg: TrisaConfig | null) { this.cfg = cfg; }

  config(): TrisaConfig | null {
    if (this.cfg === undefined) {
      try { this.cfg = trisaConfigFromEnv(); } catch (err) { this.logger.error(`TRISA is misconfigured: ${(err as Error).message}`); this.cfg = null; }
      if (this.cfg?.sharedSealingKey) this.logger.warn('TRISA_SEALING_KEY_FILE is not set: the TLS identity key also seals envelopes. TRISA recommends a separate sealing key.');
    }
    return this.cfg;
  }
  configured(): boolean { return this.config() !== null; }

  private admin(): Pool {
    if (!this.adminPool) this.adminPool = new Pool({ connectionString: process.env.DATABASE_ADMIN_URL, max: 3 });
    return this.adminPool;
  }

  async onModuleInit() {
    const cfg = this.config();
    if (cfg?.listen) await this.listen(cfg.listen);
  }
  async onModuleDestroy() {
    await new Promise<void>((r) => (this.server ? this.server.tryShutdown(() => r()) : r()));
    await this.adminPool?.end().catch(() => undefined);
  }

  // ---- the receiving server ------------------------------------------------

  async listen(address: string): Promise<number> {
    const cfg = this.config();
    if (!cfg) throw new Error('TRISA is not configured');
    if (!cfg.ca) throw new Error('TRISA_CA_FILE is required to receive: without it there is no way to tell a TRISA member from anyone else');
    const p = protocol();
    const h = inboundHandlers({
      admin: () => this.admin(), cfg, log: (m) => this.logger.warn(m),
      alert: (title, detail) => void this.alerts?.notify({ severity: 'info', title, detail }),
    });
    const server = new grpc.Server();
    server.addService(p.network, { Transfer: h.transfer, KeyExchange: h.keyExchange, TransferStream: h.unimplemented('TransferStream'), ConfirmAddress: h.unimplemented('ConfirmAddress') } as any);
    server.addService(p.health, { Status: h.status } as any);
    // The third argument makes a client certificate mandatory: a TLS handshake without
    // one fails before any of our code runs.
    const creds = grpc.ServerCredentials.createSsl(cfg.ca, [{ private_key: cfg.key, cert_chain: cfg.cert }], true);
    const port = await new Promise<number>((resolve, reject) => server.bindAsync(address, creds, (err, bound) => (err ? reject(err) : resolve(bound))));
    this.server = server; this.boundPort = port;
    this.logger.log(`TRISA node listening on ${address.replace(/:\d+$/, '')}:${port} (mutual TLS)`);
    return port;
  }

  // ---- counterparties ------------------------------------------------------

  async addCounterparty(customerId: string, userId: string, input: CounterpartyInput) {
    if (!input.name?.trim()) throw new BadRequestException('name is required');
    if (!/^[A-Za-z0-9.-]+:\d{1,5}$/.test(input.endpoint ?? '')) throw new BadRequestException('endpoint must be host:port');
    if (input.lei && !/^[A-Z0-9]{20}$/.test(input.lei)) throw new BadRequestException('lei must be 20 upper-case letters and digits');
    let pem: string | null = null; let sig: string | null = null;
    if (input.sealingPublicKeyPem) {
      const k = this.parseSealingKey(input.sealingPublicKeyPem);
      pem = k.pem; sig = k.signature;
    }
    try {
      const r = await withTenant(this.pool, customerId, (c) => c.query(
        `INSERT INTO trisa_counterparties (customer_id, name, lei, endpoint, common_name, sealing_public_key_pem, sealing_key_signature, added_by)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING *`,
        [customerId, input.name.trim(), input.lei ?? null, input.endpoint, input.commonName ?? null, pem, sig, userId]));
      return cpView(r.rows[0]);
    } catch (err) {
      if ((err as { code?: string }).code === '23505') throw new ConflictException('a counterparty with this endpoint already exists');
      throw err;
    }
  }

  async listCounterparties(customerId: string) {
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM trisa_counterparties WHERE customer_id = $1 ORDER BY added_at DESC`, [customerId]));
    return r.rows.map(cpView);
  }

  // Asks the counterparty for its sealing key. The key is recorded but not trusted: a
  // person must compare its signature with one the counterparty has given them
  // out-of-band, and a second person must then trust it.
  async fetchKey(customerId: string, counterpartyId: string) {
    const cp = await this.counterparty(customerId, counterpartyId);
    if (cp.status === 'trusted') throw new ConflictException('a trusted counterparty\'s key cannot be replaced; revoke it and add a new one');
    const cfg = this.requireConfig();
    const client = this.client(cfg, cp.endpoint);
    try {
      const res: any = await new Promise((resolve, reject) =>
        client.KeyExchange({ version: 1, data: cfg.sealingPublic.export({ type: 'spki', format: 'der' }), publicKeyAlgorithm: 'RSA' }, { deadline: Date.now() + CALL_TIMEOUT_MS }, (e: Error | null, r: unknown) => (e ? reject(e) : resolve(r))));
      if (!res?.data?.length) throw new Error('the counterparty returned no key');
      const k = this.parseSealingKey(createPublicKey({ key: Buffer.from(res.data), format: 'der', type: 'spki' }).export({ type: 'spki', format: 'pem' }).toString());
      const r = await withTenant(this.pool, customerId, (c) => c.query(
        `UPDATE trisa_counterparties SET sealing_public_key_pem = $3, sealing_key_signature = $4 WHERE counterparty_id = $1 AND customer_id = $2 RETURNING *`,
        [counterpartyId, customerId, k.pem, k.signature]));
      return cpView(r.rows[0]);
    } catch (err) {
      if (err instanceof BadRequestException) throw err;
      throw new UnprocessableEntityException(`key exchange with ${cp.endpoint} failed: ${(err as Error).message}`);
    } finally { client.close(); }
  }

  async trust(customerId: string, counterpartyId: string, userId: string, expectedSignature: string) {
    const cp = await this.counterparty(customerId, counterpartyId);
    if (!cp.sealing_key_signature) throw new UnprocessableEntityException('the counterparty has no sealing key yet; fetch it or supply it first');
    // Whoever trusts must state the key they were shown. A trust decision made on a
    // key the person did not look at is no decision.
    if (expectedSignature !== cp.sealing_key_signature) {
      throw new UnprocessableEntityException(`the key signature given does not match the one on record (${cp.sealing_key_signature}); check it against what the counterparty gave you out-of-band`);
    }
    try {
      const r = await withTenant(this.pool, customerId, (c) => c.query(
        `UPDATE trisa_counterparties SET status = 'trusted', trusted_by = $3, trusted_at = now() WHERE counterparty_id = $1 AND customer_id = $2 AND status = 'pending' RETURNING *`,
        [counterpartyId, customerId, userId]));
      if (!r.rows[0]) throw new ConflictException(`the counterparty is ${cp.status}, not pending`);
      return cpView(r.rows[0]);
    } catch (err) {
      if ((err as { code?: string }).code === 'OFB07') throw new UnprocessableEntityException((err as Error).message);
      throw err;
    }
  }

  async revoke(customerId: string, counterpartyId: string) {
    await this.counterparty(customerId, counterpartyId);
    const r = await withTenant(this.pool, customerId, (c) => c.query(
      `UPDATE trisa_counterparties SET status = 'revoked' WHERE counterparty_id = $1 AND customer_id = $2 RETURNING *`, [counterpartyId, customerId]));
    return cpView(r.rows[0]);
  }

  async listInbound(customerId: string) {
    const r = await withTenant(this.pool, customerId, (c) => c.query(
      `SELECT inbound_id, envelope_id, peer_common_name, originating_vasp, originator_account, beneficiary_account, identity, transaction, status, reject_reason, received_at
         FROM trisa_inbound WHERE customer_id = $1 ORDER BY received_at DESC LIMIT 200`, [customerId]));
    return r.rows.map((x) => ({ inboundId: x.inbound_id, envelopeId: x.envelope_id, peer: x.peer_common_name, originatingVasp: x.originating_vasp,
      originatorAccount: x.originator_account, beneficiaryAccount: x.beneficiary_account, identity: x.identity, transaction: x.transaction,
      status: x.status, rejectReason: x.reject_reason, receivedAt: new Date(x.received_at).toISOString() }));
  }

  // ---- sending ---------------------------------------------------------------

  // The trusted counterparty for a beneficiary provider, or null: by LEI when the
  // record carries one, else by exact (case-insensitive) name.
  async findCounterparty(customerId: string, vasp: { name?: string; lei?: string } | undefined) {
    if (!vasp?.name && !vasp?.lei) return null;
    const r = await withTenant(this.pool, customerId, (c) => c.query(
      `SELECT * FROM trisa_counterparties WHERE customer_id = $1 AND status = 'trusted'
          AND (($2::text IS NOT NULL AND lei = $2) OR ($3::text IS NOT NULL AND lower(name) = lower($3)))
        ORDER BY (lei = $2) DESC NULLS LAST LIMIT 1`, [customerId, vasp.lei ?? null, vasp.name ?? null]));
    return r.rows[0] ?? null;
  }

  // Sends one record to a trusted counterparty. The envelope id is the record's id: a
  // retry after a timeout is the same transfer to the counterparty, not a second one.
  async send(counterparty: Record<string, any>, recordId: string, ivms: Ivms101Payload, facts: TransactionFacts): Promise<TrisaOutcome> {
    const cfg = this.requireConfig();
    if (counterparty.status !== 'trusted') return { status: 'failed', error: 'the counterparty is not trusted' };
    const p = protocol();
    let client: any;
    try {
      const recipient = createPublicKey(counterparty.sealing_public_key_pem);
      if (publicKeySignature(recipient) !== counterparty.sealing_key_signature) throw new Error('the stored sealing key does not match its recorded signature');
      const plaintext = encodePayload(p, toIdentityPayload(ivms), toTransaction(facts));
      const env = sealEnvelope({ id: recordId, plaintext, recipient, transferState: 'STARTED' });
      client = this.client(cfg, counterparty.endpoint);
      const res: any = await new Promise((resolve, reject) =>
        client.Transfer({ ...env, error: null }, { deadline: Date.now() + CALL_TIMEOUT_MS }, (e: Error | null, r: unknown) => (e ? reject(e) : resolve(r))));

      if (res.error && (res.error.code || res.error.message)) {
        // proto-loader hands back enum names, not numbers.
        const name = typeof res.error.code === 'string' ? res.error.code : errorName(Number(res.error.code));
        return { status: 'failed', error: `rejected by ${counterparty.name} (${name}): ${res.error.message || 'no reason given'}${res.error.retry ? ' [retry advised]' : ''}` };
      }
      if (res.id !== recordId) return { status: 'failed', error: `${counterparty.name} answered about a different transfer (${res.id})` };
      if (res.transferState === 'REJECTED') return { status: 'failed', error: `${counterparty.name} rejected the transfer` };
      try {
        const reply = decodePayload(p, openEnvelope(
          { payload: Buffer.from(res.payload), encryptionKey: Buffer.from(res.encryptionKey), encryptionAlgorithm: res.encryptionAlgorithm, hmac: Buffer.from(res.hmac), hmacSecret: Buffer.from(res.hmacSecret), hmacAlgorithm: res.hmacAlgorithm, sealed: res.sealed, publicKeySignature: res.publicKeySignature },
          cfg.sealingPrivate, cfg.sealingPublic));
        if (!reply.receivedAt) return { status: 'failed', error: `${counterparty.name} acknowledged without a received-at time` };
      } catch (err) {
        return { status: 'failed', error: `${counterparty.name} answered but its acknowledgement could not be read (${(err as Error).message}); resending is safe, the envelope id is the record id` };
      }
      return { status: 'transmitted', reference: `trisa:${recordId}` };
    } catch (err) {
      return { status: 'failed', error: grpcText(err) };
    } finally { client?.close(); }
  }

  // ---- internals -------------------------------------------------------------

  private requireConfig(): TrisaConfig {
    const cfg = this.config();
    if (!cfg) throw new UnprocessableEntityException('TRISA is not configured (TRISA_CERT_FILE, TRISA_KEY_FILE)');
    return cfg;
  }

  private client(cfg: TrisaConfig, endpoint: string): any {
    const p = protocol();
    const creds = grpc.credentials.createSsl(cfg.ca, cfg.key, cfg.cert);
    return new p.NetworkClient(endpoint, creds);
  }

  private async counterparty(customerId: string, id: string) {
    if (!UUID_RE.test(id)) throw new NotFoundException('counterparty not found');
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM trisa_counterparties WHERE counterparty_id = $1 AND customer_id = $2`, [id, customerId]));
    if (!r.rows[0]) throw new NotFoundException('counterparty not found');
    return r.rows[0];
  }

  private parseSealingKey(pem: string): { pem: string; signature: string } {
    try {
      // A certificate or a bare public key are both fine; what matters is an RSA key.
      const key = pem.includes('CERTIFICATE') ? new X509Certificate(pem).publicKey : createPublicKey(pem);
      if (key.asymmetricKeyType !== 'rsa') throw new Error('not an RSA key');
      return { pem: key.export({ type: 'spki', format: 'pem' }).toString(), signature: publicKeySignature(key) };
    } catch (err) {
      throw new BadRequestException(`the sealing key is not a usable RSA public key: ${(err as Error).message}`);
    }
  }
}

function cpView(r: Record<string, any>) {
  return { counterpartyId: r.counterparty_id, name: r.name, lei: r.lei, endpoint: r.endpoint, commonName: r.common_name,
    sealingKeySignature: r.sealing_key_signature, status: r.status, addedAt: new Date(r.added_at).toISOString(),
    trustedAt: r.trusted_at ? new Date(r.trusted_at).toISOString() : null };
}

const ERROR_NAMES: Record<number, string> = { 1: 'UNAVAILABLE', 49: 'INTERNAL_ERROR', 50: 'REJECTED', 51: 'UNKNOWN_WALLET_ADDRESS', 52: 'UNKNOWN_IDENTITY', 53: 'UNKNOWN_ORIGINATOR', 54: 'UNKNOWN_BENEFICIARY', 60: 'UNSUPPORTED_CURRENCY', 61: 'EXCEEDED_TRADING_VOLUME', 90: 'COMPLIANCE_CHECK_FAIL', 92: 'HIGH_RISK', 105: 'INVALID_SIGNATURE', 106: 'INVALID_KEY', 107: 'ENVELOPE_DECODE_FAIL', 150: 'BAD_REQUEST' };
function errorName(code: number) { return ERROR_NAMES[code] ?? `code ${code}`; }
function grpcText(err: unknown): string {
  const e = err as { code?: number; details?: string; message?: string };
  if (e?.code === grpc.status.UNAVAILABLE) return `could not reach the counterparty: ${e.details ?? e.message}`;
  if (e?.code === grpc.status.DEADLINE_EXCEEDED) return 'the counterparty did not answer in time';
  return e?.message ?? String(err);
}
