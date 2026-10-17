import { execFileSync, spawnSync } from 'child_process';
import { generateKeyPairSync, KeyObject, createPublicKey, randomUUID } from 'crypto';
import { mkdtempSync, writeFileSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import type { Ivms101Payload } from '../travel-rule';
import { EnvelopeError, openEnvelope, publicKeySignature, sealEnvelope, WireEnvelope } from './trisa-crypto';
import { decodePayload, encodePayload, toIdentityPayload, toTransaction } from './trisa-identity';
import { protocol } from './trisa-protocol';

// The wire-compatibility check. Everything else in this module could be internally
// consistent and still not speak TRISA, so this runs the gateway's output through
// TRISA's own reference implementation (github.com/trisacrypto/trisa, via
// test/trisa-interop) and the reference implementation's output through the gateway.
//
// Needs Go. Without it the tests skip -- unless REQUIRE_TRISA_INTEROP is set, as it is
// in CI, where a skip would be a green tick for nothing.

const HERE = join(__dirname, '..', '..', '..', 'test', 'trisa-interop');
let tool = '';
let dir = '';
let skipReason = '';

const IVMS: Ivms101Payload = {
  originator: {
    originatorPersons: [{ naturalPerson: { name: { primaryIdentifier: 'Ndlovu', secondaryIdentifier: 'Thandi' }, geographicAddress: { addressLine: ['12 Long Street', 'Cape Town'], country: 'ZA' }, nationalIdentification: { nationalIdentifier: '8001015009087', nationalIdentifierType: 'IDCD', countryOfIssue: 'ZA' } } }],
    accountNumber: ['0x1111111111111111111111111111111111111111'],
  },
  beneficiary: {
    beneficiaryPersons: [{ legalPerson: { name: 'Acme Trading (Pty) Ltd' } }],
    accountNumber: ['0x2222222222222222222222222222222222222222'],
  },
  originatingVASP: { name: 'Example Custody', lei: '5493001KJTIIGC8Y1R12' },
  beneficiaryVASP: { name: 'Counterparty Exchange', lei: 'HWUPKR0MPOU8FGXBT394' },
  transfer: { asset: 'ETH', amount: '1500000000000000000', decimals: 18, chainId: 1 },
};
const REF_ID = randomUUID();
const TX = { txid: '0xabc123', originator: IVMS.originator.accountNumber[0], beneficiary: IVMS.beneficiary.accountNumber[0], amount: '1500000000000000000', decimals: 18, chainId: 1, asset: 'ETH' };

function skipped(): boolean {
  if (!skipReason) return false;
  if (process.env.REQUIRE_TRISA_INTEROP) throw new Error(`REQUIRE_TRISA_INTEROP is set but the reference tool is unavailable: ${skipReason}`);
  console.warn(`skipping TRISA interop -- ${skipReason}`);
  return true;
}

function toWire(env: WireEnvelope): Buffer {
  const p = protocol();
  const T = p.root.lookupType('trisa.api.v1beta1.SecureEnvelope');
  return Buffer.from(T.encode(T.fromObject({ ...env, error: undefined })).finish());
}
function fromWire(bytes: Buffer): WireEnvelope {
  const p = protocol();
  const T = p.root.lookupType('trisa.api.v1beta1.SecureEnvelope');
  const o: any = T.toObject(T.decode(bytes), { enums: String, defaults: true, bytes: Buffer });
  return o as WireEnvelope;
}

// protojson reads JSON names (json_name in the .proto), protobufjs writes field names.
function jsonNames(type: any, obj: any): any {
  if (Array.isArray(obj)) return obj.map((o) => jsonNames(type, o));
  if (!obj || typeof obj !== 'object') return obj;
  const out: Record<string, any> = {};
  for (const [k, v] of Object.entries(obj)) {
    const f = type.fields[k]; f.resolve();
    out[f.options?.json_name ?? k] = f.resolvedType && f.resolvedType.fields ? jsonNames(f.resolvedType, v) : v;
  }
  return out;
}

describe('TRISA wire compatibility with the reference implementation', () => {
  let priv: KeyObject; let pub: KeyObject; let privPath = ''; let pubPath = '';

  beforeAll(() => {
    try {
      execFileSync('go', ['version'], { stdio: 'ignore' });
    } catch { skipReason = 'go is not installed'; return; }
    dir = mkdtempSync(join(tmpdir(), 'trisa-interop-'));
    tool = join(dir, 'interop');
    const b = spawnSync('go', ['build', '-o', tool, '.'], { cwd: HERE, encoding: 'utf8', timeout: 240000, env: { ...process.env, GOFLAGS: '-mod=mod' } });
    if (b.status !== 0) { skipReason = `the reference tool did not build: ${(b.stderr || b.stdout).slice(0, 400)}`; return; }
    const kp = generateKeyPairSync('rsa', { modulusLength: 2048 });
    priv = kp.privateKey; pub = kp.publicKey;
    privPath = join(dir, 'priv.pem'); pubPath = join(dir, 'pub.pem');
    writeFileSync(privPath, priv.export({ type: 'pkcs8', format: 'pem' }));
    writeFileSync(pubPath, pub.export({ type: 'spki', format: 'pem' }));
  }, 300000);
  afterAll(() => { if (dir) rmSync(dir, { recursive: true, force: true }); });

  it('an envelope this gateway seals is opened by TRISA\'s reference code, and its identity validates under the reference rules', () => {
    if (skipped()) return;
    const p = protocol();
    const plaintext = encodePayload(p, toIdentityPayload(IVMS), toTransaction(TX));
    const env = sealEnvelope({ id: randomUUID(), plaintext, recipient: pub, transferState: 'STARTED' });
    const r = spawnSync(tool, ['unseal', privPath], { input: toWire(env), encoding: 'utf8' });
    expect({ status: r.status, err: r.stderr }).toEqual({ status: 0, err: '' });
    const out = JSON.parse(r.stdout);
    expect(out.envelopeId).toBe(env.id);
    expect(out.transferState).toMatch(/STARTED/i);
    // The reference tool prints protojson, which uses the IVMS101 JSON names.
    const person = out.identity.originator.originatorPersons[0].naturalPerson;
    expect(person.name.nameIdentifier[0]).toMatchObject({ primaryIdentifier: 'Ndlovu', secondaryIdentifier: 'Thandi' });
    expect(person.geographicAddress[0].addressLine).toEqual(['12 Long Street', 'Cape Town']);
    expect(out.identity.originator.accountNumber).toEqual([IVMS.originator.accountNumber[0]]);
    expect(out.identity.beneficiary.beneficiaryPersons[0].legalPerson.name.nameIdentifier[0].legalPersonName).toBe('Acme Trading (Pty) Ltd');
    expect(out.identity.beneficiaryVasp.beneficiaryVasp.legalPerson.nationalIdentification.nationalIdentifier).toBe('HWUPKR0MPOU8FGXBT394');
    expect(out.transaction).toMatchObject({ txid: '0xabc123', network: 'ETH', assetType: 'ETH', amount: 1.5 });
  });

  it('an envelope TRISA\'s reference code seals is opened by this gateway', () => {
    if (skipped()) return;
    const p = protocol();
    // Reference-format identity: take what we produce, as JSON, and let Go build the envelope.
    const idObj = jsonNames(p.IdentityPayload, p.IdentityPayload.toObject(p.IdentityPayload.fromObject(toIdentityPayload(IVMS)), { enums: String, defaults: false }));
    const txObj = p.Transaction.toObject(p.Transaction.fromObject(toTransaction(TX)), { enums: String, defaults: false });
    const r: any = spawnSync(tool, ['seal', pubPath, REF_ID], { input: JSON.stringify({ identity: idObj, transaction: txObj }), encoding: null as any });
    expect({ status: r.status, err: r.stderr.toString() }).toEqual({ status: 0, err: '' });
    const env = fromWire(r.stdout);
    expect(env.id).toBe(REF_ID);
    expect(env.sealed).toBe(true);
    expect(env.publicKeySignature).toBe(publicKeySignature(pub));
    const plaintext = openEnvelope(env, priv, pub);
    const decoded = decodePayload(p, plaintext);
    expect(decoded.identity.originator.originatorPersons[0].naturalPerson.name.nameIdentifiers[0].primaryIdentifier).toBe('Ndlovu');
    expect(decoded.transaction).toMatchObject({ txid: '0xabc123', network: 'ETH' });
    expect(decoded.sentAt).not.toBe('');
  });

  it('the public key signature is the reference format: SHA256: and unpadded base64', () => {
    if (skipped()) return;
    const s = publicKeySignature(pub);
    expect(s).toMatch(/^SHA256:[A-Za-z0-9+/]{43}$/);
    expect(publicKeySignature(createPublicKey(pub.export({ type: 'spki', format: 'pem' })))).toBe(s);
  });
});

describe('opening an envelope safely', () => {
  const kp = generateKeyPairSync('rsa', { modulusLength: 2048 });
  const other = generateKeyPairSync('rsa', { modulusLength: 2048 });
  const mk = () => sealEnvelope({ id: 'e1', plaintext: Buffer.from('travel rule payload'), recipient: kp.publicKey, transferState: 'STARTED' });

  it('round-trips', () => {
    expect(openEnvelope(mk(), kp.privateKey, kp.publicKey).toString()).toBe('travel rule payload');
  });
  it('refuses a tampered payload before decrypting it', () => {
    const e = mk(); e.payload[0] ^= 1;
    expect(() => openEnvelope(e, kp.privateKey, kp.publicKey)).toThrow(/HMAC/);
  });
  it('refuses a tampered HMAC', () => {
    const e = mk(); e.hmac[0] ^= 1;
    expect(() => openEnvelope(e, kp.privateKey, kp.publicKey)).toThrow(EnvelopeError);
  });
  it('says "different key" when sealed to someone else, so the sender can fetch ours again', () => {
    const e = sealEnvelope({ id: 'e2', plaintext: Buffer.from('x'), recipient: other.publicKey, transferState: 'STARTED' });
    expect(() => openEnvelope(e, kp.privateKey, kp.publicKey)).toThrow(/different public key/);
    const stripped = { ...e, publicKeySignature: '' };
    expect(() => openEnvelope(stripped, kp.privateKey, kp.publicKey)).toThrow(/could not unseal/);
  });
  it('refuses unsealed envelopes and algorithms other than the two it implements', () => {
    expect(() => openEnvelope({ ...mk(), sealed: false }, kp.privateKey, kp.publicKey)).toThrow(/unsealed/);
    expect(() => openEnvelope({ ...mk(), encryptionAlgorithm: 'AES128-GCM' }, kp.privateKey, kp.publicKey)).toThrow(/encryption algorithm/);
    expect(() => openEnvelope({ ...mk(), hmacAlgorithm: 'HMAC-MD5' }, kp.privateKey, kp.publicKey)).toThrow(/hmac algorithm/);
  });
  it('refuses a payload shorter than a nonce and a tag', () => {
    const e = mk(); e.payload = Buffer.alloc(10);
    e.hmac = require('crypto').createHmac('sha256', Buffer.alloc(1)).update(e.payload).digest();
    expect(() => openEnvelope(e, kp.privateKey, kp.publicKey)).toThrow();
  });
  it('refuses a non-RSA sealing key', () => {
    const ec = generateKeyPairSync('ec', { namedCurve: 'P-256' });
    expect(() => sealEnvelope({ id: 'x', plaintext: Buffer.from('x'), recipient: ec.publicKey, transferState: 'STARTED' })).toThrow(/RSA/);
  });
});
