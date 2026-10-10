import { createPrivateKey, createPublicKey, KeyObject } from 'crypto';
import { readFileSync } from 'fs';

// A TRISA node's identity: the certificate it presents on mutual TLS, the CA whose
// certificates it accepts from others, and the RSA key counterparties seal envelopes to.
// TRISA recommends the sealing key be distinct from the identity key, so they are set
// separately; with only the identity key given the node works but says so.
export interface TrisaConfig {
  cert: Buffer;
  key: Buffer;
  ca?: Buffer;
  sealingPrivate: KeyObject;
  sealingPublic: KeyObject;
  sharedSealingKey: boolean;
  listen?: string;
}

export function trisaConfigFromEnv(env: NodeJS.ProcessEnv = process.env): TrisaConfig | null {
  if (!env.TRISA_CERT_FILE && !env.TRISA_KEY_FILE) return null;
  if (!env.TRISA_CERT_FILE || !env.TRISA_KEY_FILE) {
    throw new Error('TRISA needs both TRISA_CERT_FILE and TRISA_KEY_FILE (the mutual-TLS identity), or neither');
  }
  const cert = readFileSync(env.TRISA_CERT_FILE);
  const key = readFileSync(env.TRISA_KEY_FILE);
  const sealingFile = env.TRISA_SEALING_KEY_FILE;
  const sealingPrivate = createPrivateKey(sealingFile ? readFileSync(sealingFile) : key);
  if (sealingPrivate.asymmetricKeyType !== 'rsa') {
    throw new Error('the TRISA sealing key must be an RSA key (set TRISA_SEALING_KEY_FILE to one if the TLS identity is not RSA)');
  }
  return {
    cert, key,
    ca: env.TRISA_CA_FILE ? readFileSync(env.TRISA_CA_FILE) : undefined,
    sealingPrivate,
    sealingPublic: createPublicKey(sealingPrivate),
    sharedSealingKey: !sealingFile,
    listen: env.TRISA_LISTEN || undefined,
  };
}
