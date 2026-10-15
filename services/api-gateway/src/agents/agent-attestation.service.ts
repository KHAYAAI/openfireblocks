import { BadRequestException, Injectable, ServiceUnavailableException } from '@nestjs/common';
import { createPrivateKey, createPublicKey, sign, type KeyObject } from 'node:crypto';
import { readFileSync } from 'node:fs';

// A signed statement about one agent's record, for the operator to hand to a third party (the FORGE credit bureau) as evidence.
//
// The third party cannot ask us for this record: our admin route sits behind a user login and says nothing about which wallet the agent
// pays from. So we sign a short statement that names the key's address, and the operator carries it across. The receiver checks the
// signature against our published attestation key and checks that the address is the wallet it already knows, so a record cannot be
// borrowed for someone else's wallet.
//
// Only counts leave: how many payments went through, how many were abandoned, how many were refused and why (by reason). No amounts, no
// recipients, no purposes. The signed text is carried as a string, so there is no canonical form for the two sides to disagree about.

export interface AttestationRecord {
  transfers: number;
  released: number;
  refused: number;
  refusalsByReason: Record<string, number>;
}

export interface AttestationInput {
  customerId: string;
  agentId: string;
  agentStatus: 'active' | 'revoked';
  keyAddress: string;
  record: AttestationRecord;
  /** The party this is for (for the bureau: its agent id or DID). Optional; binds the statement to one audience. */
  subject?: string;
  validityDays?: number;
  now?: Date;
}

export const DEFAULT_VALIDITY_DAYS = 7;
// Short on purpose: a statement is a snapshot, and a long-lived one would let an old, better record outlive a worse present.
export const MAX_VALIDITY_DAYS = 14;

@Injectable()
export class AgentAttestationService {
  private loaded: { key: KeyObject; publicKeyB64: string } | null = null;

  private load(): { key: KeyObject; publicKeyB64: string } {
    if (this.loaded) return this.loaded;
    const path = process.env['AGENT_ATTESTATION_KEY_FILE'];
    if (!path) throw new ServiceUnavailableException('agent attestations are not enabled on this deployment (AGENT_ATTESTATION_KEY_FILE is not set)');
    let key: KeyObject;
    try {
      key = createPrivateKey(readFileSync(path));
    } catch {
      // The message of a key-parsing error can echo key material; say only that it could not be read.
      throw new ServiceUnavailableException('the agent attestation signing key could not be read');
    }
    if (key.asymmetricKeyType !== 'ed25519') throw new ServiceUnavailableException('the agent attestation signing key must be an ed25519 key');
    const spki = createPublicKey(key).export({ type: 'spki', format: 'der' });
    this.loaded = { key, publicKeyB64: Buffer.from(spki.subarray(spki.length - 32)).toString('base64') };
    return this.loaded;
  }

  /** The raw 32-byte public key, base64: what the receiver configures as CUSTODY_ATTESTATION_PUBLIC_KEYS. */
  publicKey(): string {
    return this.load().publicKeyB64;
  }

  issue(input: AttestationInput): { payload: string; signature: string; publicKey: string } {
    const days = input.validityDays ?? DEFAULT_VALIDITY_DAYS;
    if (!Number.isInteger(days) || days < 1 || days > MAX_VALIDITY_DAYS) {
      throw new BadRequestException(`validityDays must be a whole number from 1 to ${MAX_VALIDITY_DAYS}`);
    }
    const { key, publicKeyB64 } = this.load();
    const now = input.now ?? new Date();
    const r = input.record;
    const payload = JSON.stringify({
      version: 1,
      issuer: 'forge-custody',
      issuedAt: now.toISOString(),
      expiresAt: new Date(now.getTime() + days * 86_400_000).toISOString(),
      custodyCustomerId: input.customerId,
      agentId: input.agentId,
      keyAddress: input.keyAddress,
      agentStatus: input.agentStatus,
      record: { transfers: r.transfers, released: r.released, refused: r.refused, refusalsByReason: { ...r.refusalsByReason } },
      ...(input.subject ? { subject: input.subject } : {}),
    });
    const signature = sign(null, Buffer.from(payload, 'utf8'), key).toString('base64');
    // publicKey is informational. A receiver must verify against the key it was given out of band, never this one.
    return { payload, signature, publicKey: publicKeyB64 };
  }
}
