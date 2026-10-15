import { BadRequestException, ConflictException, ServiceUnavailableException } from '@nestjs/common';
import { createPublicKey, generateKeyPairSync, verify } from 'node:crypto';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { AgentsAdminController } from './agents.controller';
import { AgentAttestationService, DEFAULT_VALIDITY_DAYS, MAX_VALIDITY_DAYS } from './agent-attestation.service';
import { CAN_MANAGE } from '../approvals/roles';

// A signed statement of an agent's record, for the operator to hand to the FORGE credit bureau. What matters: only the right
// things leave (counts, not amounts or recipients), it is signed so it can be checked and cannot be altered, it is short-lived, it
// names the key's address so it cannot be borrowed for another wallet, and only an admin can ask for one.

const ROLES_KEY = 'ofb:tenantRoles';
const KEY_ADDRESS = '0x00000000000000000000000000000000000000aa';
const NOW = new Date('2026-10-15T12:00:00.000Z');
const RECORD = { transfers: 40, released: 2, refused: 3, refusalsByReason: { 'over daily budget': 1, 'recipient not allowed': 2 } };

let dir: string | undefined;
let signingPublic: Buffer;
const SAVED = process.env['AGENT_ATTESTATION_KEY_FILE'];

function installKey(): string {
  dir = mkdtempSync(join(tmpdir(), 'attest-'));
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const spki = publicKey.export({ type: 'spki', format: 'der' });
  signingPublic = Buffer.from(spki.subarray(spki.length - 32));
  const path = join(dir, 'key.pem');
  writeFileSync(path, privateKey.export({ type: 'pkcs8', format: 'pem' }), { mode: 0o600 });
  process.env['AGENT_ATTESTATION_KEY_FILE'] = path;
  return path;
}
afterEach(() => {
  if (dir) rmSync(dir, { recursive: true, force: true });
  dir = undefined;
  if (SAVED === undefined) delete process.env['AGENT_ATTESTATION_KEY_FILE']; else process.env['AGENT_ATTESTATION_KEY_FILE'] = SAVED;
});

const input = (over: Record<string, unknown> = {}) => ({
  customerId: 'c1', agentId: 'a1', agentStatus: 'active' as const, keyAddress: KEY_ADDRESS, record: RECORD, now: NOW, ...over,
});

describe('issuing a statement', () => {
  it('signs a short-lived statement of the counts, naming the key address, that the public key verifies', () => {
    installKey();
    const svc = new AgentAttestationService();
    const out = svc.issue(input());
    const p = JSON.parse(out.payload);
    expect(p).toEqual({
      version: 1, issuer: 'forge-custody', issuedAt: '2026-10-15T12:00:00.000Z', expiresAt: '2026-10-22T12:00:00.000Z',
      custodyCustomerId: 'c1', agentId: 'a1', keyAddress: KEY_ADDRESS, agentStatus: 'active', record: RECORD,
    });
    const spki = Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), signingPublic]);
    const pub = createPublicKey({ key: spki, format: 'der', type: 'spki' });
    expect(verify(null, Buffer.from(out.payload, 'utf8'), pub, Buffer.from(out.signature, 'base64'))).toBe(true);
    expect(out.publicKey).toBe(signingPublic.toString('base64'));
    expect(svc.publicKey()).toBe(out.publicKey);
  });

  it('cannot be altered after signing', () => {
    installKey();
    const out = new AgentAttestationService().issue(input());
    const spki = Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), signingPublic]);
    const pub = createPublicKey({ key: spki, format: 'der', type: 'spki' });
    const tampered = out.payload.replace('"transfers":40', '"transfers":400');
    expect(verify(null, Buffer.from(tampered, 'utf8'), pub, Buffer.from(out.signature, 'base64'))).toBe(false);
  });

  it('carries counts only: no amounts, recipients, purposes or limits', () => {
    installKey();
    const noisy = { ...RECORD, spent24hZar: 123.45, remaining24hZar: 876.55, recipients: ['0xabc'], purpose: 'secret invoice' };
    const text = new AgentAttestationService().issue(input({ record: noisy })).payload;
    for (const leak of ['123.45', '876.55', '0xabc', 'secret invoice', 'spent24hZar', 'remaining24hZar']) expect(text).not.toContain(leak);
  });

  it('adds the audience only when asked', () => {
    installKey();
    const svc = new AgentAttestationService();
    expect(JSON.parse(svc.issue(input()).payload).subject).toBeUndefined();
    expect(JSON.parse(svc.issue(input({ subject: 'bureau_agent_9' })).payload).subject).toBe('bureau_agent_9');
  });

  it('defaults to seven days, never exceeds fourteen, and refuses nonsense', () => {
    installKey();
    const svc = new AgentAttestationService();
    expect(DEFAULT_VALIDITY_DAYS).toBe(7);
    expect(JSON.parse(svc.issue(input({ validityDays: MAX_VALIDITY_DAYS })).payload).expiresAt).toBe('2026-10-29T12:00:00.000Z');
    for (const bad of [0, -1, 15, 1.5, Number.NaN]) expect(() => svc.issue(input({ validityDays: bad }))).toThrow(BadRequestException);
  });
});

describe('when attestations are not set up', () => {
  it('is unavailable without a key file, and says how to enable it', () => {
    delete process.env['AGENT_ATTESTATION_KEY_FILE'];
    expect(() => new AgentAttestationService().issue(input())).toThrow(ServiceUnavailableException);
    expect(() => new AgentAttestationService().issue(input())).toThrow(/AGENT_ATTESTATION_KEY_FILE/);
  });

  it('refuses an unreadable key or a key of the wrong type, without echoing any of it', () => {
    dir = mkdtempSync(join(tmpdir(), 'attest-'));
    const bad = join(dir, 'bad.pem');
    writeFileSync(bad, '-----BEGIN PRIVATE KEY-----\nSECRET-LOOKING-MATERIAL\n-----END PRIVATE KEY-----\n');
    process.env['AGENT_ATTESTATION_KEY_FILE'] = bad;
    let message = '';
    try { new AgentAttestationService().issue(input()); } catch (e) { message = String((e as Error).message); }
    expect(message).toMatch(/could not be read/);
    expect(message).not.toContain('SECRET-LOOKING-MATERIAL');

    const rsa = join(dir, 'rsa.pem');
    writeFileSync(rsa, generateKeyPairSync('rsa', { modulusLength: 2048 }).privateKey.export({ type: 'pkcs8', format: 'pem' }));
    process.env['AGENT_ATTESTATION_KEY_FILE'] = rsa;
    expect(() => new AgentAttestationService().issue(input())).toThrow(/must be an ed25519 key/);
  });
});

describe('the admin route', () => {
  const AGENT = { agentId: 'a1', name: 'bot', keyId: 'k1', status: 'active', record: { ...RECORD, spent24hZar: 55, remaining24hZar: 945 } };
  function setup(opts: { address?: string | null } = {}) {
    const agents = { get: jest.fn(async () => AGENT) };
    const keys = { getKey: jest.fn(async () => ({ address: opts.address === undefined ? KEY_ADDRESS : opts.address })) };
    const audit = { logEvent: jest.fn(async () => undefined) };
    const issued: any[] = [];
    const attestations = { issue: jest.fn((i: unknown) => { issued.push(i); return { payload: '{}', signature: 'sig', publicKey: 'pk' }; }) };
    const c = new AgentsAdminController(agents as any, audit as any, keys as any, attestations as any);
    return { c, agents, keys, audit, attestations, issued };
  }
  const CLAIMS = { sub: 'u1', email: 'admin@example.org' } as any;

  it('is for admins only', () => {
    expect(Reflect.getMetadata(ROLES_KEY, AgentsAdminController.prototype.attest)).toEqual([...CAN_MANAGE]);
    expect(CAN_MANAGE).toEqual(['admin']);
  });

  it('attests the agent\'s own key address and counts, and records who asked', async () => {
    const { c, issued, audit, keys } = setup();
    await c.attest('c1', 'a1', CLAIMS, 'bureau_agent_9', '10');
    expect(keys.getKey).toHaveBeenCalledWith('k1', 'c1');
    expect(issued[0]).toMatchObject({
      customerId: 'c1', agentId: 'a1', agentStatus: 'active', keyAddress: KEY_ADDRESS, subject: 'bureau_agent_9', validityDays: 10,
      record: { transfers: 40, released: 2, refused: 3, refusalsByReason: RECORD.refusalsByReason },
    });
    expect(Object.keys(issued[0].record).sort()).toEqual(['refusalsByReason', 'refused', 'released', 'transfers']);   // no spend amounts passed on
    expect(audit.logEvent).toHaveBeenCalledWith(expect.objectContaining({ type: 'agent.attested', customerId: 'c1', message: expect.stringContaining('admin@example.org') }));
  });

  it('refuses when the key has no address yet, and a bad subject, and issues nothing', async () => {
    const a = setup({ address: null });
    await expect(a.c.attest('c1', 'a1', CLAIMS)).rejects.toThrow(ConflictException);
    expect(a.attestations.issue).not.toHaveBeenCalled();
    const b = setup();
    await expect(b.c.attest('c1', 'a1', CLAIMS, 'x'.repeat(201))).rejects.toThrow(BadRequestException);
    await expect(b.c.attest('c1', 'a1', CLAIMS, '')).rejects.toThrow(BadRequestException);
    expect(b.attestations.issue).not.toHaveBeenCalled();
    expect(b.audit.logEvent).not.toHaveBeenCalled();
  });
});
