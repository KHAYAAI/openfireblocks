// Reconciliation as pure functions: given what the platform signed and
// what the chain returned, what is the state of each transfer, and what
// disagrees. No I/O here, so every classification is a unit test.

import { Transaction, getAddress, id as keccakId } from 'ethers';

export const TRANSFER_TOPIC = keccakId('Transfer(address,address,uint256)');

export type Classification =
  | 'confirmed' // mined, succeeded, and moved what the ledger says
  | 'mismatch' // mined and succeeded, but did not move what the ledger says
  | 'failed' // mined and reverted: the ledger says signed, nothing moved
  | 'pending' // known to the node, not yet mined
  | 'recent' // unknown to the node, but signed too recently to worry
  | 'missing' // unknown to the node, long after signing: never broadcast or dropped
  | 'unsigned_outbound'; // the address spent a nonce the platform never signed

export type Severity = 'ok' | 'info' | 'warning' | 'critical';

export const SEVERITY: Record<Classification, Severity> = {
  confirmed: 'ok',
  recent: 'info',
  pending: 'info',
  missing: 'warning',
  failed: 'warning',
  mismatch: 'critical',
  unsigned_outbound: 'critical',
};

export interface LedgerEntry {
  requestId: string;
  signedTx: string;
  createdAt: Date;
  // What the ledger says moved: for a token, the decoded recipient and
  // amount; for a native transfer, the transaction's own to and value.
  assetContract: string | null;
  effectiveTo: string | null;
  effectiveAmount: string | null;
}

export interface Parsed {
  requestId: string;
  hash: string;
  chainId: number;
  from: string;
  nonce: number;
  to: string | null;
  value: bigint;
  entry: LedgerEntry;
}

// The transaction exactly as signed. The ledger's own columns are not
// trusted for this: the signed bytes are what the chain will see.
export function parseSigned(entry: LedgerEntry): Parsed {
  const tx = Transaction.from(entry.signedTx);
  if (!tx.hash || !tx.from) throw new Error(`ledger entry ${entry.requestId} is not a signed transaction`);
  return {
    requestId: entry.requestId,
    hash: tx.hash,
    chainId: Number(tx.chainId),
    from: tx.from,
    nonce: tx.nonce,
    to: tx.to,
    value: tx.value,
    entry,
  };
}

export interface ReceiptLike {
  status: number | null;
  blockNumber: number;
  logs: ReadonlyArray<{ address: string; topics: ReadonlyArray<string>; data: string }>;
}

function topicAddress(topic: string): string {
  return getAddress('0x' + topic.slice(-40));
}

// Did this receipt move exactly what the ledger says? For a token: a
// Transfer event from the token contract, from the signing address, to
// the recorded recipient, for the recorded amount. For a native transfer:
// success is enough, since value is part of the signed bytes.
export function movedWhatLedgerSays(p: Parsed, receipt: ReceiptLike): { ok: boolean; detail: string } {
  const e = p.entry;
  if (!e.assetContract) return { ok: true, detail: `native transfer of ${p.value} wei` };
  const want = {
    contract: getAddress(e.assetContract),
    from: getAddress(p.from),
    to: e.effectiveTo ? getAddress(e.effectiveTo) : null,
    amount: e.effectiveAmount !== null ? BigInt(e.effectiveAmount) : null,
  };
  const transfers = receipt.logs
    .filter((l) => l.topics[0]?.toLowerCase() === TRANSFER_TOPIC && l.topics.length === 3)
    .map((l) => ({
      contract: getAddress(l.address),
      from: topicAddress(l.topics[1]),
      to: topicAddress(l.topics[2]),
      amount: BigInt(l.data),
    }));
  const match = transfers.find(
    (t) => t.contract === want.contract && t.from === want.from && t.to === want.to && t.amount === want.amount,
  );
  if (match) return { ok: true, detail: `Transfer of ${match.amount} to ${match.to}` };
  const seen = transfers.map((t) => `${t.amount} ${t.from}->${t.to} (${t.contract})`).join('; ') || 'no Transfer events';
  return {
    ok: false,
    detail: `ledger says ${want.amount} to ${want.to} from ${want.from} on ${want.contract}; chain shows ${seen}`,
  };
}

export function classify(
  p: Parsed,
  receipt: ReceiptLike | null,
  knownToNode: boolean,
  now: Date,
  missingAfterMinutes: number,
): { classification: Classification; detail: string } {
  if (receipt) {
    if (receipt.status === 0) return { classification: 'failed', detail: `reverted in block ${receipt.blockNumber}` };
    const moved = movedWhatLedgerSays(p, receipt);
    return moved.ok
      ? { classification: 'confirmed', detail: `block ${receipt.blockNumber}: ${moved.detail}` }
      : { classification: 'mismatch', detail: `block ${receipt.blockNumber}: ${moved.detail}` };
  }
  if (knownToNode) return { classification: 'pending', detail: 'in the mempool, not yet mined' };
  const ageMin = (now.getTime() - p.entry.createdAt.getTime()) / 60000;
  return ageMin < missingAfterMinutes
    ? { classification: 'recent', detail: `signed ${Math.round(ageMin)} min ago; not yet seen by the node` }
    : { classification: 'missing', detail: `signed ${Math.round(ageMin)} min ago and unknown to the node: never broadcast, or dropped` };
}

// Nonces an address has used on chain that the platform never signed.
// Every nonce below the chain's count was used by a mined transaction; if
// the platform has no signed transaction with that nonce from that
// address, somebody else signed with the key.
export function unsignedNonces(chainNonce: number, signedNonces: ReadonlySet<number>): number[] {
  const out: number[] = [];
  for (let n = 0; n < chainNonce; n++) if (!signedNonces.has(n)) out.push(n);
  return out;
}

// ------------------------------------------------------------ statements

export interface StatementRow {
  txHash: string;
  asset: string;
  amount: string; // base units
}

export interface StatementBreak {
  classification: 'missing_in_platform' | 'missing_in_statement' | 'amount_mismatch' | 'asset_mismatch';
  txHash: string;
  detail: string;
}

// A customer's own books against the platform's ledger, by transaction
// hash.
export function compareStatement(
  rows: StatementRow[],
  ledger: Array<{ txHash: string; asset: string; amount: string }>,
): { matched: number; breaks: StatementBreak[] } {
  const byHash = new Map(ledger.map((l) => [l.txHash.toLowerCase(), l]));
  const seen = new Set<string>();
  const breaks: StatementBreak[] = [];
  let matched = 0;
  for (const r of rows) {
    const h = r.txHash.toLowerCase();
    seen.add(h);
    const l = byHash.get(h);
    if (!l) {
      breaks.push({ classification: 'missing_in_platform', txHash: r.txHash, detail: `statement shows ${r.amount} ${r.asset}; the platform never signed it` });
    } else if (l.asset.toUpperCase() !== r.asset.toUpperCase()) {
      breaks.push({ classification: 'asset_mismatch', txHash: r.txHash, detail: `statement ${r.asset}, platform ${l.asset}` });
    } else if (BigInt(l.amount) !== BigInt(r.amount)) {
      breaks.push({ classification: 'amount_mismatch', txHash: r.txHash, detail: `statement ${r.amount}, platform ${l.amount} (${l.asset})` });
    } else {
      matched++;
    }
  }
  for (const l of ledger) {
    if (!seen.has(l.txHash.toLowerCase())) {
      breaks.push({ classification: 'missing_in_statement', txHash: l.txHash, detail: `platform signed ${l.amount} ${l.asset}; not in the statement` });
    }
  }
  return { matched, breaks };
}
