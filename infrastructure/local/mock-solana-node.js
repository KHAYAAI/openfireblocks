// A stand-in for a Solana JSON-RPC node, for local end-to-end runs.
//
// What it does honestly: it accepts a transaction only if its signature
// verifies (Ed25519) against the fee payer named in the message, the check a
// real validator makes first. What it is not: a chain. There is no consensus,
// no state, and every balance is the same configured number. A transaction
// this node accepts has NOT been accepted by Solana.
//
//   PORT=18899 BALANCE_LAMPORTS=5000000000 node mock-solana-node.js
//   GET /__sent returns what was accepted, for assertions.
const http = require('http');
const crypto = require('crypto');

const ALPHABET = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
const b58 = (buf) => { let n = BigInt('0x' + (buf.toString('hex') || '0')); let s = ''; while (n > 0n) { s = ALPHABET[Number(n % 58n)] + s; n /= 58n; } for (const b of buf) { if (b === 0) s = '1' + s; else break; } return s; };

function shortvec(buf, off) { let n = 0, shift = 0, i = off; for (;;) { const b = buf[i++]; n |= (b & 0x7f) << shift; if (!(b & 0x80)) break; shift += 7; } return [n, i]; }

// Ed25519 public key (32 raw bytes) -> a KeyObject node can verify with.
const spki = (raw) => crypto.createPublicKey({ key: Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), raw]), format: 'der', type: 'spki' });

const sent = [];
const balance = Number(process.env.BALANCE_LAMPORTS || 5_000_000_000);

function acceptTransaction(b64) {
  const tx = Buffer.from(b64, 'base64');
  let [nsig, off] = shortvec(tx, 0);
  if (nsig < 1) throw new Error('no signatures');
  const sigs = []; for (let i = 0; i < nsig; i++) { sigs.push(tx.subarray(off, off + 64)); off += 64; }
  const message = tx.subarray(off);
  // legacy message: 3-byte header, then the account keys; the first is the fee payer
  let [nkeys, koff] = shortvec(message, 3);
  const payer = message.subarray(koff, koff + 32);
  if (!crypto.verify(null, message, spki(payer), sigs[0])) throw new Error('signature verification failed');
  const signature = b58(sigs[0]);
  // First instruction of a System Program transfer: [program, accounts..., data = u32 index(2) + u64 lamports]
  const keys = []; for (let i = 0; i < nkeys; i++) keys.push(message.subarray(koff + 32 * i, koff + 32 * i + 32));
  let io = koff + 32 * nkeys + 32; let ninst; [ninst, io] = shortvec(message, io);
  let transfer = null;
  if (ninst >= 1) {
    io += 1; let nacc; [nacc, io] = shortvec(message, io); const accIdx = [...message.subarray(io, io + nacc)]; io += nacc;
    let dlen; [dlen, io] = shortvec(message, io); const data = message.subarray(io, io + dlen);
    if (dlen === 12 && data.readUInt32LE(0) === 2) transfer = { source: b58(keys[accIdx[0]]), destination: b58(keys[accIdx[1]]), lamports: data.readBigUInt64LE(4).toString() };
  }
  sent.push({ signature, payer: b58(payer), accounts: nkeys, bytes: tx.length, transfer });
  return signature;
}

http.createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/__sent') { res.setHeader('content-type', 'application/json'); return res.end(JSON.stringify(sent)); }
  let body = ''; req.on('data', (d) => (body += d)); req.on('end', () => {
    const m = JSON.parse(body || '{}'); const out = { jsonrpc: '2.0', id: m.id ?? 1 };
    try {
      switch (m.method) {
        case 'getLatestBlockhash': out.result = { value: { blockhash: b58(crypto.createHash('sha256').update(String(Date.now())).digest()), lastValidBlockHeight: 99 } }; break;
        case 'getBalance': out.result = { value: balance }; break;
        case 'getTransaction': { const t = sent.find((x) => x.signature === m.params[0]); out.result = t && t.transfer ? { slot: 1, transaction: { message: { instructions: [{ program: 'system', parsed: { type: 'transfer', info: t.transfer } }] } } } : null; break; }
        case 'getFeeForMessage': out.result = { value: 5000 }; break;
        case 'sendTransaction': out.result = acceptTransaction(m.params[0]); break;
        case 'getSignatureStatuses': out.result = { value: m.params[0].map((s) => (sent.find((x) => x.signature === s) ? { slot: 1, confirmations: null, err: null, confirmationStatus: 'finalized' } : null)) }; break;
        default: out.error = { code: -32601, message: `mock node: ${m.method} not implemented` };
      }
    } catch (e) { out.error = { code: -32002, message: e.message }; }
    res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(out));
  });
}).listen(Number(process.env.PORT || 18899), '127.0.0.1');
