import { createCipheriv, createDecipheriv, createHash, createHmac, constants, KeyObject, privateDecrypt, publicEncrypt, randomBytes, timingSafeEqual } from 'crypto';

// The cryptography of a TRISA SecureEnvelope, as the reference implementation
// (github.com/trisacrypto/trisa, pkg/trisa/crypto) defines it:
//
//   payload      AES-256-GCM, a fresh random key per envelope, 12-byte random nonce
//                appended to the ciphertext: [ciphertext || tag || nonce]
//   hmac         HMAC-SHA256 over the *encrypted* payload, with hmac_secret
//   sealing      the encryption key and the hmac secret are each encrypted to the
//                recipient's RSA public key with RSA-OAEP, SHA-512
//   key id       public_key_signature = "SHA256:" + unpadded base64 of the SHA-256 of
//                the recipient key's PKIX (SubjectPublicKeyInfo) DER
//
// The test suite checks this against the reference implementation itself, not just
// against this file.

export const ENCRYPTION_ALGORITHM = 'AES256-GCM';
export const HMAC_ALGORITHM = 'HMAC-SHA256';
const NONCE = 12;
const TAG = 16;

export interface WireEnvelope {
  id: string;
  payload: Buffer;
  encryptionKey: Buffer;
  encryptionAlgorithm: string;
  hmac: Buffer;
  hmacSecret: Buffer;
  hmacAlgorithm: string;
  sealed: boolean;
  publicKeySignature: string;
  timestamp: string;
  transferState: string;
}

export class EnvelopeError extends Error {
  constructor(readonly code: 'invalid-key' | 'invalid-signature' | 'unsupported' | 'malformed', message: string) { super(message); }
}

// Identifies a public key to the party who must find its private half.
export function publicKeySignature(pub: KeyObject): string {
  const der = pub.export({ type: 'spki', format: 'der' });
  return 'SHA256:' + createHash('sha256').update(der).digest('base64').replace(/=+$/, '');
}

export function sealEnvelope(args: { id: string; plaintext: Buffer; recipient: KeyObject; transferState: string; now?: Date }): WireEnvelope {
  if (args.recipient.asymmetricKeyType !== 'rsa') throw new EnvelopeError('invalid-key', 'a TRISA sealing key must be an RSA key');
  const key = randomBytes(32);
  const hmacSecret = randomBytes(32);
  const nonce = randomBytes(NONCE);
  const cipher = createCipheriv('aes-256-gcm', key, nonce);
  const body = Buffer.concat([cipher.update(args.plaintext), cipher.final()]);
  const payload = Buffer.concat([body, cipher.getAuthTag(), nonce]);
  const oaep = { padding: constants.RSA_PKCS1_OAEP_PADDING, oaepHash: 'sha512' } as const;
  return {
    id: args.id,
    payload,
    encryptionKey: publicEncrypt({ key: args.recipient, ...oaep }, key),
    encryptionAlgorithm: ENCRYPTION_ALGORITHM,
    hmac: createHmac('sha256', hmacSecret).update(payload).digest(),
    hmacSecret: publicEncrypt({ key: args.recipient, ...oaep }, hmacSecret),
    hmacAlgorithm: HMAC_ALGORITHM,
    sealed: true,
    publicKeySignature: publicKeySignature(args.recipient),
    timestamp: (args.now ?? new Date()).toISOString(),
    transferState: args.transferState,
  };
}

// Opens a sealed envelope with our private key. The HMAC is checked before anything is
// decrypted, and the algorithms must be exactly the ones this node implements: a
// counterparty cannot talk this node into a weaker one.
export function openEnvelope(env: Pick<WireEnvelope, 'payload' | 'encryptionKey' | 'encryptionAlgorithm' | 'hmac' | 'hmacSecret' | 'hmacAlgorithm' | 'sealed' | 'publicKeySignature'>, priv: KeyObject, mine: KeyObject): Buffer {
  if (!env.sealed) throw new EnvelopeError('unsupported', 'unsealed envelopes are not accepted: the encryption key would be in the clear');
  if (env.encryptionAlgorithm !== ENCRYPTION_ALGORITHM) throw new EnvelopeError('unsupported', `unsupported encryption algorithm ${JSON.stringify(env.encryptionAlgorithm)}`);
  if (env.hmacAlgorithm !== HMAC_ALGORITHM) throw new EnvelopeError('unsupported', `unsupported hmac algorithm ${JSON.stringify(env.hmacAlgorithm)}`);
  // Sealed to some other key: say so, so the sender can fetch ours again.
  if (env.publicKeySignature && env.publicKeySignature !== publicKeySignature(mine)) {
    throw new EnvelopeError('invalid-key', 'the envelope is sealed to a different public key than this node holds');
  }
  const oaep = { padding: constants.RSA_PKCS1_OAEP_PADDING, oaepHash: 'sha512' } as const;
  let key: Buffer; let secret: Buffer;
  try {
    key = privateDecrypt({ key: priv, ...oaep }, env.encryptionKey);
    secret = privateDecrypt({ key: priv, ...oaep }, env.hmacSecret);
  } catch {
    throw new EnvelopeError('invalid-key', 'could not unseal the envelope with this node\'s key');
  }
  if (key.length !== 32) throw new EnvelopeError('malformed', 'the encryption key is not 256 bits');
  const expected = createHmac('sha256', secret).update(env.payload).digest();
  if (expected.length !== env.hmac.length || !timingSafeEqual(expected, env.hmac)) {
    throw new EnvelopeError('invalid-signature', 'the envelope\'s HMAC does not match its payload');
  }
  if (env.payload.length < NONCE + TAG) throw new EnvelopeError('malformed', 'the payload is too short');
  const nonce = env.payload.subarray(env.payload.length - NONCE);
  const sealedBody = env.payload.subarray(0, env.payload.length - NONCE);
  const tag = sealedBody.subarray(sealedBody.length - TAG);
  const data = sealedBody.subarray(0, sealedBody.length - TAG);
  try {
    const d = createDecipheriv('aes-256-gcm', key, nonce);
    d.setAuthTag(tag);
    return Buffer.concat([d.update(data), d.final()]);
  } catch {
    throw new EnvelopeError('invalid-signature', 'the payload failed authentication');
  }
}
