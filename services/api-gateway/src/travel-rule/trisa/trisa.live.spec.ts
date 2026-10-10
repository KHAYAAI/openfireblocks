import { execFileSync } from 'child_process';
import * as grpc from '@grpc/grpc-js';
import { createPrivateKey, createPublicKey, randomUUID } from 'crypto';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { Pool } from 'pg';
import { sealEnvelope } from './trisa-crypto';
import { encodePayload, toIdentityPayload, toTransaction } from './trisa-identity';
import { protocol } from './trisa-protocol';
import { TrisaConfig } from './trisa.config';
import { TrisaService } from './trisa.service';
import { TravelRuleService } from '../travel-rule.service';
import type { Ivms101Payload } from '../travel-rule';

// The whole TRISA exchange over real mutual-TLS gRPC and real Postgres: one node
// receives, another sends, certificates come from a throwaway CA. Both nodes are this
// gateway, so this proves the flow and the safeguards; that the wire format is TRISA's
// is proved separately against the reference implementation (trisa-interop.spec.ts).
//
//   eval "$(infrastructure/local/postgres-local.sh start)"   # migrations through 032
//   npx jest src/travel-rule/trisa/trisa.live.spec.ts

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';

let reachable = false;
beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try { reachable = (await p.query(`SELECT to_regclass('trisa_counterparties') IS NOT NULL AS ok`)).rows[0].ok; } catch { reachable = false; } finally { await p.end().catch(() => undefined); }
});
function skipped(): boolean {
  if (reachable) return false;
  if (process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 032 is reachable');
  console.warn('skipping TRISA live test -- no database with migration 032');
  return true;
}

const sh = (args: string[], cwd: string) => execFileSync('openssl', args, { cwd, stdio: 'pipe' });
function makeCa(dir: string, name: string) {
  sh(['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', `${name}.key`, '-out', `${name}.crt`, '-days', '2', '-subj', `/CN=${name}`, '-addext', 'basicConstraints=critical,CA:TRUE'], dir);
}
function makeNode(dir: string, ca: string, name: string) {
  sh(['req', '-newkey', 'rsa:2048', '-nodes', '-keyout', `${name}.key`, '-out', `${name}.csr`, '-subj', `/CN=${name}`], dir);
  writeFileSync(join(dir, `${name}.ext`), 'subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth,clientAuth\n');
  sh(['x509', '-req', '-in', `${name}.csr`, '-CA', `${ca}.crt`, '-CAkey', `${ca}.key`, '-CAcreateserial', '-out', `${name}.crt`, '-days', '2', '-extfile', `${name}.ext`], dir);
}
function cfg(dir: string, node: string, ca: string): TrisaConfig {
  const key = readFileSync(join(dir, `${node}.key`));
  const sealingPrivate = createPrivateKey(key);
  return { cert: readFileSync(join(dir, `${node}.crt`)), key, ca: readFileSync(join(dir, `${ca}.crt`)), sealingPrivate, sealingPublic: createPublicKey(sealingPrivate), sharedSealingKey: true };
}

const ivmsFor = (from: string, to: string, beneficiaryVasp: string): Ivms101Payload => ({
  originator: { originatorPersons: [{ naturalPerson: { name: { primaryIdentifier: 'Ndlovu', secondaryIdentifier: 'Thandi' }, geographicAddress: { addressLine: ['12 Long Street'], country: 'ZA' } } }], accountNumber: [from] },
  beneficiary: { beneficiaryPersons: [{ legalPerson: { name: 'Acme Trading (Pty) Ltd' } }], accountNumber: [to] },
  originatingVASP: { name: 'Sender Custody' },
  beneficiaryVASP: { name: beneficiaryVasp },
  transfer: { asset: 'ETH', amount: '2500000000000000000', decimals: 18, chainId: 1 },
});
const facts = (from: string, to: string) => ({ txid: '0xfeed', originator: from, beneficiary: to, amount: '2500000000000000000', decimals: 18, chainId: 1, asset: 'ETH' });

describe('TRISA exchange (live Postgres, real mutual-TLS gRPC)', () => {
  let dir = ''; let admin: Pool; let tenant: Pool;
  let sender: TrisaService; let receiver: TrisaService; let rogue: TrisaService;
  let orgA = ''; let orgB = ''; let alice = ''; let bob = '';
  let port = 0; let cpId = ''; let recvAddress = '';
  const mkOrg = async () => (await admin.query(`INSERT INTO customers (name, api_key_hash) VALUES ('trisa-test', decode(md5(clock_timestamp()::text || random()::text), 'hex')) RETURNING customer_id`)).rows[0].customer_id;
  const mkUser = async (n: string) => (await admin.query(`INSERT INTO users (email, password_hash, full_name) VALUES ($1, 'x', $2) RETURNING id`, [`${n}-${randomUUID()}@example.test`, n])).rows[0].id;

  beforeAll(async () => {
    if (!reachable) return;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN;
    dir = mkdtempSync(join(tmpdir(), 'trisa-live-'));
    makeCa(dir, 'trisa-ca'); makeCa(dir, 'rogue-ca');
    makeNode(dir, 'trisa-ca', 'sender'); makeNode(dir, 'trisa-ca', 'receiver'); makeNode(dir, 'rogue-ca', 'rogue');
    admin = new Pool({ connectionString: ADMIN_DSN }); tenant = new Pool({ connectionString: TENANT_DSN });
    orgA = await mkOrg(); orgB = await mkOrg(); alice = await mkUser('alice'); bob = await mkUser('bob');
    recvAddress = '0x' + randomUUID().replace(/-/g, '').padEnd(40, 'a').slice(0, 40);
    await admin.query(`INSERT INTO key_pairs (customer_id, name, blockchain, threshold, total_parties, address, status) VALUES ($1,'deposit','ethereum',1,3,$2,'active')`, [orgB, recvAddress]);

    sender = new TrisaService(tenant); sender.useConfig(cfg(dir, 'sender', 'trisa-ca'));
    receiver = new TrisaService(tenant); receiver.useConfig({ ...cfg(dir, 'receiver', 'trisa-ca') });
    port = await receiver.listen('127.0.0.1:0');
    // A node whose certificate comes from a CA nobody here trusts.
    rogue = new TrisaService(tenant); rogue.useConfig({ ...cfg(dir, 'rogue', 'rogue-ca') });
  }, 120000);
  afterAll(async () => {
    if (!reachable) return;
    await receiver.onModuleDestroy(); await sender.onModuleDestroy(); await rogue.onModuleDestroy();
    await admin.end(); await tenant.end(); rmSync(dir, { recursive: true, force: true });
  });

  it('adds a counterparty, fetches its key by key exchange, and trusts it only through a second person who states the key they checked', async () => {
    if (skipped()) return;
    const cp = await sender.addCounterparty(orgA, alice, { name: 'Receiving Exchange', endpoint: `localhost:${port}`, commonName: 'receiver' });
    cpId = cp.counterpartyId;
    expect(cp).toMatchObject({ status: 'pending', sealingKeySignature: null });
    await expect(sender.addCounterparty(orgA, alice, { name: 'dup', endpoint: `localhost:${port}` })).rejects.toThrow(/already exists/);
    await expect(sender.trust(orgA, cpId, bob, 'SHA256:x')).rejects.toThrow(/no sealing key yet/);

    const withKey = await sender.fetchKey(orgA, cpId);
    const expected = withKey.sealingKeySignature!;
    expect(expected).toMatch(/^SHA256:/);
    expect(expected).toBe((await import('./trisa-crypto')).publicKeySignature(receiver.config()!.sealingPublic));

    await expect(sender.trust(orgA, cpId, bob, 'SHA256:' + 'A'.repeat(43))).rejects.toThrow(/does not match/);
    // The person who added it cannot trust it, whatever signature they give.
    await expect(sender.trust(orgA, cpId, alice, expected)).rejects.toThrow(/someone other than the person who added/);
    expect((await sender.trust(orgA, cpId, bob, expected)).status).toBe('trusted');
    // Trusted means fixed.
    await expect(admin.query(`UPDATE trisa_counterparties SET endpoint = 'evil:1' WHERE counterparty_id = $1`, [cpId])).rejects.toMatchObject({ code: 'OFB07' });
    await expect(sender.fetchKey(orgA, cpId)).rejects.toThrow(/cannot be replaced/);
  });

  it('sends Travel Rule information to the counterparty, which stores it for the customer who owns the address and acknowledges', async () => {
    if (skipped()) return;
    const from = '0x' + '1'.repeat(40);
    const cp = await sender.findCounterparty(orgA, { name: 'receiving exchange' });
    expect(cp?.counterparty_id).toBe(cpId);
    const recordId = randomUUID();
    const out = await sender.send(cp, recordId, ivmsFor(from, recvAddress, 'Receiving Exchange'), facts(from, recvAddress));
    expect(out).toEqual({ status: 'transmitted', reference: `trisa:${recordId}` });

    const inbound = await receiver.listInbound(orgB);
    const row = inbound.find((x) => x.envelopeId === recordId)!;
    expect(row).toMatchObject({ status: 'accepted', beneficiaryAccount: recvAddress, originatorAccount: from, originatingVasp: 'Sender Custody', peer: 'sender' });
    expect(row.identity.originator.originatorPersons[0].naturalPerson.name.nameIdentifiers[0].primaryIdentifier).toBe('Ndlovu');
    expect(row.transaction).toMatchObject({ txid: '0xfeed', network: 'ETH' });
    // Nobody else sees it.
    expect((await receiver.listInbound(orgA)).find((x) => x.envelopeId === recordId)).toBeUndefined();
  });

  it('treats the same envelope sent twice as one transfer', async () => {
    if (skipped()) return;
    const from = '0x' + '2'.repeat(40);
    const cp = await sender.findCounterparty(orgA, { name: 'Receiving Exchange' });
    const recordId = randomUUID();
    const a = await sender.send(cp, recordId, ivmsFor(from, recvAddress, 'Receiving Exchange'), facts(from, recvAddress));
    const b = await sender.send(cp, recordId, ivmsFor(from, recvAddress, 'Receiving Exchange'), facts(from, recvAddress));
    expect([a.status, b.status]).toEqual(['transmitted', 'transmitted']);
    expect((await receiver.listInbound(orgB)).filter((x) => x.envelopeId === recordId)).toHaveLength(1);
  });

  it('is rejected, with TRISA\'s reason, when the beneficiary address is not one the receiver holds; the attempt is recorded, visible to no tenant', async () => {
    if (skipped()) return;
    const from = '0x' + '3'.repeat(40); const stranger = '0x' + '9'.repeat(40);
    const cp = await sender.findCounterparty(orgA, { name: 'Receiving Exchange' });
    const recordId = randomUUID();
    const out = await sender.send(cp, recordId, ivmsFor(from, stranger, 'Receiving Exchange'), facts(from, stranger));
    expect(out.status).toBe('failed');
    expect(out.error).toMatch(/UNKNOWN_WALLET_ADDRESS/);
    const kept = await admin.query(`SELECT customer_id, status FROM trisa_inbound WHERE envelope_id = $1`, [recordId]);
    expect(kept.rows[0]).toMatchObject({ customer_id: null, status: 'rejected' });
    expect((await receiver.listInbound(orgB)).find((x) => x.envelopeId === recordId)).toBeUndefined();
  });

  it('will not talk to a peer whose certificate is not from the trusted CA, in either direction', async () => {
    if (skipped()) return;
    // The rogue node dials the receiver: the handshake fails before any message.
    const from = '0x' + '4'.repeat(40);
    const fake = { counterparty_id: randomUUID(), name: 'Receiving Exchange', status: 'trusted', endpoint: `localhost:${port}`,
      sealing_public_key_pem: receiver.config()!.sealingPublic.export({ type: 'spki', format: 'pem' }).toString(),
      sealing_key_signature: (await import('./trisa-crypto')).publicKeySignature(receiver.config()!.sealingPublic) };
    const out = await rogue.send(fake, randomUUID(), ivmsFor(from, recvAddress, 'Receiving Exchange'), facts(from, recvAddress));
    expect(out.status).toBe('failed');
    expect(out.error).toMatch(/could not reach/);
  });

  it('rejects a tampered envelope, an unsealed one, and one sealed to a different key, each with TRISA\'s code', async () => {
    if (skipped()) return;
    const p = protocol();
    const from = '0x' + '5'.repeat(40);
    const plaintext = encodePayload(p, toIdentityPayload(ivmsFor(from, recvAddress, 'Receiving Exchange')), toTransaction(facts(from, recvAddress)));
    const client: any = new p.NetworkClient(`localhost:${port}`, grpc.credentials.createSsl(sender.config()!.ca, sender.config()!.key, sender.config()!.cert));
    const call = (env: any) => new Promise<any>((resolve, reject) => client.Transfer({ ...env, error: null }, (e: Error | null, r: any) => (e ? reject(e) : resolve(r))));
    try {
      const good = sealEnvelope({ id: randomUUID(), plaintext, recipient: receiver.config()!.sealingPublic, transferState: 'STARTED' });
      const tampered = { ...good, id: randomUUID(), payload: Buffer.from(good.payload) }; tampered.payload[3] ^= 1;
      expect((await call(tampered)).error.code).toBe('INVALID_SIGNATURE');
      expect((await call({ ...good, id: randomUUID(), sealed: false })).error.code).toBe('BAD_REQUEST');
      const other = sealEnvelope({ id: randomUUID(), plaintext, recipient: sender.config()!.sealingPublic, transferState: 'STARTED' });
      const r = await call(other);
      expect(r.error).toMatchObject({ code: 'INVALID_KEY', retry: true }); // worth retrying with a fresh key
      expect((await call({ ...good, id: '' })).error.code).toBe('REJECTED');
    } finally { client.close(); }
    expect((await admin.query(`SELECT 1 FROM trisa_inbound WHERE originator_account = $1`, [from])).rows).toHaveLength(0);
  });

  it('is invisible to another organisation, and received information cannot be edited or deleted', async () => {
    if (skipped()) return;
    expect(await sender.listCounterparties(orgB)).toEqual([]);
    expect((await sender.listCounterparties(orgA)).length).toBe(1);
    const id = (await admin.query(`SELECT inbound_id FROM trisa_inbound WHERE customer_id = $1 LIMIT 1`, [orgB])).rows[0].inbound_id;
    await expect(admin.query(`UPDATE trisa_inbound SET status = 'rejected' WHERE inbound_id = $1`, [id])).rejects.toMatchObject({ code: 'OFB07' });
    await expect(admin.query(`DELETE FROM trisa_inbound WHERE inbound_id = $1`, [id])).rejects.toMatchObject({ code: 'OFB07' });
    await expect(sender.revoke(orgB, cpId)).rejects.toThrow(/not found/);
  });

  it('is used by the Travel Rule service for a transfer to a trusted counterparty, and the record says so', async () => {
    if (skipped()) return;
    const tr = new TravelRuleService(tenant, sender);
    const from = '0x' + '6'.repeat(40);
    const requestId = randomUUID();
    const input = { originator: ivmsFor(from, recvAddress, 'x').originator.originatorPersons[0], beneficiary: ivmsFor(from, recvAddress, 'x').beneficiary.beneficiaryPersons[0], beneficiaryVasp: { name: 'Receiving Exchange' } };
    const assessment = tr.assess({ asset: 'NATIVE', amount: '2500000000000000000', decimals: 18 }, input)!;
    const recordId = await tr.recordBeforeSigning({ customerId: orgA, customerName: 'Sender Custody', requestId, chainId: 1, facts: { asset: 'ETH', amount: '2500000000000000000', decimals: 18 }, originatorAddress: from, beneficiaryAddress: recvAddress, assessment });
    const out = await tr.completeAfterSigning(orgA, recordId, '0xhash');
    expect(out).toMatchObject({ status: 'transmitted', reference: `trisa:${recordId}` });
    const rec = await tr.get(orgA, recordId);
    expect(rec).toMatchObject({ transmissionStatus: 'transmitted', transmissionReference: `trisa:${recordId}`, txHash: '0xhash' });
    expect((await receiver.listInbound(orgB)).find((x) => x.envelopeId === recordId)?.transaction.txid).toBe('0xhash');
  });

  it('a revoked counterparty is no longer sent anything', async () => {
    if (skipped()) return;
    await sender.revoke(orgA, cpId);
    expect(await sender.findCounterparty(orgA, { name: 'Receiving Exchange' })).toBeNull();
    await expect(admin.query(`UPDATE trisa_counterparties SET status = 'trusted' WHERE counterparty_id = $1`, [cpId])).rejects.toMatchObject({ code: 'OFB07' });
  });
});
