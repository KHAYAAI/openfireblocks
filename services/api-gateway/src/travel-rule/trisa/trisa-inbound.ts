import * as grpc from '@grpc/grpc-js';
import { createHash, createPublicKey, KeyObject, X509Certificate } from 'crypto';
import { Pool } from 'pg';
import { EnvelopeError, openEnvelope, sealEnvelope } from './trisa-crypto';
import { decodePayload, encodePayload } from './trisa-identity';
import { ErrorCode, protocol } from './trisa-protocol';
import { TrisaConfig } from './trisa.config';

// The receiving side: another provider tells us about a transfer to one of our
// customers' addresses. Everything about the sender is untrusted input -- the identity
// payload is whatever they put in it -- and is stored as evidence, never acted on.

type Deps = { admin: () => Pool; cfg: TrisaConfig; log: (m: string) => void; alert: (title: string, detail: string) => void };

const reject = (code: number, message: string, id = '') => ({ id, error: { code, message, retry: code === ErrorCode.UNAVAILABLE }, transferState: 'REJECTED', timestamp: new Date().toISOString() });

export function inboundHandlers(d: Deps) {
  const p = protocol();

  async function transfer(call: grpc.ServerUnaryCall<any, any>, cb: grpc.sendUnaryData<any>) {
    const req = call.request;
    const peer = call.getAuthContext()?.sslPeerCertificate;
    if (!peer?.raw) return cb(null, reject(ErrorCode.REJECTED, 'a client certificate is required', req.id));
    const fingerprint = createHash('sha256').update(peer.raw).digest('hex');
    const peerName: string = (peer.subject as any)?.CN ?? '';
    let peerRsa: KeyObject | undefined;
    try {
      const k = new X509Certificate(peer.raw).publicKey;
      if (k.asymmetricKeyType === 'rsa') peerRsa = k;
    } catch { /* no usable key in the certificate */ }

    // An error envelope from the peer (a rejection of something we sent) needs no payload.
    if (req.error && (req.error.code || req.error.message)) {
      return cb(null, reject(ErrorCode.REJECTED, 'this node does not accept unsolicited error envelopes', req.id));
    }
    if (!req.id) return cb(null, reject(ErrorCode.REJECTED, 'the envelope has no id'));

    let decoded;
    try {
      const plaintext = openEnvelope(
        { payload: Buffer.from(req.payload), encryptionKey: Buffer.from(req.encryptionKey), encryptionAlgorithm: req.encryptionAlgorithm, hmac: Buffer.from(req.hmac), hmacSecret: Buffer.from(req.hmacSecret), hmacAlgorithm: req.hmacAlgorithm, sealed: req.sealed, publicKeySignature: req.publicKeySignature },
        d.cfg.sealingPrivate, d.cfg.sealingPublic,
      );
      decoded = decodePayload(p, plaintext);
    } catch (err) {
      const e = err as EnvelopeError;
      const code = e.code === 'invalid-key' ? ErrorCode.INVALID_KEY : e.code === 'invalid-signature' ? ErrorCode.INVALID_SIGNATURE : e.code === 'malformed' ? ErrorCode.ENVELOPE_DECODE_FAIL : ErrorCode.BAD_REQUEST;
      d.log(`rejected an envelope from ${peerName || fingerprint.slice(0, 12)}: ${e.message}`);
      // INVALID_KEY is retryable: the sender may simply be holding an old copy of our key.
      return cb(null, { ...reject(code, e.message, req.id), error: { code, message: e.message, retry: e.code === 'invalid-key' } });
    }

    const beneficiary: string = decoded.identity.beneficiary?.accountNumbers?.[0] ?? decoded.transaction.beneficiary ?? '';
    const originator: string = decoded.identity.originator?.accountNumbers?.[0] ?? decoded.transaction.originator ?? '';
    const originVasp = decoded.identity.originatingVasp?.originatingVasp?.legalPerson?.name?.nameIdentifiers?.[0]?.legalPersonName ?? '';

    // Whose address is it? Only an organisation that holds the beneficiary address
    // has any business receiving this.
    const owners = beneficiary
      ? await d.admin().query(`SELECT DISTINCT customer_id FROM key_pairs WHERE address = $1 OR (address LIKE '0x%' AND lower(address) = lower($1)) LIMIT 2`, [beneficiary])
      : { rows: [] as any[] };
    const stored = async (status: 'accepted' | 'rejected', customerId: string | null, reason?: string) => {
      try {
        await d.admin().query(
          `INSERT INTO trisa_inbound (customer_id, envelope_id, peer_common_name, peer_fingerprint, originating_vasp, originator_account, beneficiary_account, identity, transaction, status, reject_reason)
           VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (envelope_id, peer_fingerprint) DO NOTHING`,
          [customerId, req.id, peerName, fingerprint, originVasp, originator, beneficiary, JSON.stringify(decoded.identity), JSON.stringify(decoded.transaction), status, reason ?? null],
        );
      } catch (err) { d.log(`could not store inbound ${req.id}: ${(err as Error).message}`); throw err; }
    };

    if (owners.rows.length !== 1) {
      const why = owners.rows.length === 0 ? 'this provider does not control the beneficiary address' : 'the beneficiary address is ambiguous';
      await stored('rejected', null, why).catch(() => undefined);
      return cb(null, reject(ErrorCode.UNKNOWN_WALLET_ADDRESS, why, req.id));
    }
    const customerId: string = owners.rows[0].customer_id;

    // The reply is sealed to the sender: their registered sealing key if we have one,
    // else the key of the certificate they just authenticated with.
    let to: KeyObject | undefined = peerRsa;
    try {
      const k = await d.admin().query(
        `SELECT sealing_public_key_pem FROM trisa_counterparties WHERE customer_id = $1 AND status <> 'revoked' AND common_name = $2 AND sealing_public_key_pem IS NOT NULL LIMIT 1`,
        [customerId, peerName],
      );
      if (k.rows[0]) to = createPublicKey(k.rows[0].sealing_public_key_pem);
    } catch { /* keep the certificate key */ }
    if (!to || to.asymmetricKeyType !== 'rsa') {
      await stored('rejected', customerId, 'no RSA key to seal the reply to').catch(() => undefined);
      return cb(null, reject(ErrorCode.REJECTED, 'could not find an RSA key to seal the reply to', req.id));
    }

    try { await stored('accepted', customerId); } catch { return cb(null, reject(ErrorCode.INTERNAL_ERROR, 'could not record the transfer; please retry', req.id)); }
    d.alert('Travel Rule information received', `${originVasp || 'a provider'} sent information for a transfer to ${beneficiary.slice(0, 12)}…`);

    const now = new Date();
    const identity = decoded.identity; const tx = decoded.transaction;
    const reply = sealEnvelope({
      id: req.id,
      plaintext: encodePayload(p, identity, tx, decoded.sentAt ? new Date(decoded.sentAt) : now, now),
      recipient: to, transferState: 'ACCEPTED', now,
    });
    cb(null, reply);
  }

  return {
    transfer,
    keyExchange(_call: grpc.ServerUnaryCall<any, any>, cb: grpc.sendUnaryData<any>) {
      const der = d.cfg.sealingPublic.export({ type: 'spki', format: 'der' });
      cb(null, { version: 1, data: der, publicKeyAlgorithm: 'RSA', signatureAlgorithm: '', signature: Buffer.alloc(0), notBefore: '', notAfter: '', revoked: false });
    },
    status(_call: grpc.ServerUnaryCall<any, any>, cb: grpc.sendUnaryData<any>) {
      cb(null, { status: 'HEALTHY', notBefore: '', notAfter: '' });
    },
    unimplemented(name: string) {
      return (_c: any, cb: grpc.sendUnaryData<any>) => cb({ code: grpc.status.UNIMPLEMENTED, message: `${name} is not implemented by this node` } as any);
    },
  };
}

