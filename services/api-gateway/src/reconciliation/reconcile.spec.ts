import { Wallet, zeroPadValue, toBeHex } from 'ethers';
import { classify, classifyNative, compareStatement, SEVERITY, movedWhatLedgerSays, parseSigned, TRANSFER_TOPIC, unsignedNonces } from './reconcile';

const key = new Wallet('0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d');
const TOKEN = '0x1234567890123456789012345678901234567890';
const TO = '0x70997970C51812dc3A010C7d01b50e0d17dc79C8';

async function entry(over: Record<string, unknown> = {}, createdAt = new Date()) {
  const signedTx = await key.signTransaction({ to: TOKEN, value: 0, nonce: 3, gasLimit: 60000, gasPrice: 1, chainId: 1337, data: '0x' });
  return { requestId: 'r1', signedTx, createdAt, assetContract: TOKEN, effectiveTo: TO, effectiveAmount: '1000', ...over };
}

const transferLog = (from: string, to: string, amount: bigint, contract = TOKEN) => ({
  address: contract,
  topics: [TRANSFER_TOPIC, zeroPadValue(from, 32), zeroPadValue(to, 32)],
  data: zeroPadValue(toBeHex(amount), 32),
});

describe('classifying a signed transfer against the chain', () => {
  it('reads what was signed from the signed bytes', async () => {
    const p = parseSigned(await entry());
    expect(p).toMatchObject({ chainId: 1337, nonce: 3, from: key.address });
  });

  it('confirmed: mined, succeeded, and the Transfer event matches the ledger exactly', async () => {
    const p = parseSigned(await entry());
    const c = classify(p, { status: 1, blockNumber: 9, logs: [transferLog(key.address, TO, 1000n)] }, true, new Date(), 30);
    expect(c.classification).toBe('confirmed');
  });

  it('mismatch: succeeded but moved a different amount than the ledger records', async () => {
    const p = parseSigned(await entry());
    const c = classify(p, { status: 1, blockNumber: 9, logs: [transferLog(key.address, TO, 999n)] }, true, new Date(), 30);
    expect(c.classification).toBe('mismatch');
    expect(c.detail).toMatch(/ledger says 1000.*chain shows 999/);
  });

  it('mismatch: a Transfer from another contract does not count', async () => {
    const p = parseSigned(await entry());
    const c = classify(p, { status: 1, blockNumber: 9, logs: [transferLog(key.address, TO, 1000n, '0x' + '9'.repeat(40))] }, true, new Date(), 30);
    expect(c.classification).toBe('mismatch');
  });

  it('failed, pending, recent and missing', async () => {
    const p = parseSigned(await entry());
    expect(classify(p, { status: 0, blockNumber: 9, logs: [] }, true, new Date(), 30).classification).toBe('failed');
    expect(classify(p, null, true, new Date(), 30).classification).toBe('pending');
    expect(classify(p, null, false, new Date(), 30).classification).toBe('recent');
    const old = parseSigned(await entry({}, new Date(Date.now() - 3600_000)));
    expect(classify(old, null, false, new Date(), 30).classification).toBe('missing');
  });

  it('a native transfer needs only success', async () => {
    const p = parseSigned(await entry({ assetContract: null }));
    expect(movedWhatLedgerSays(p, { status: 1, blockNumber: 1, logs: [] }).ok).toBe(true);
  });
});

describe('nonces the platform never signed', () => {
  it('every used nonce without a signed transaction is reported', () => {
    expect(unsignedNonces(6, new Set([0, 1, 2, 3, 5]))).toEqual([4]);
    expect(unsignedNonces(3, new Set([0, 1, 2]))).toEqual([]);
    expect(unsignedNonces(2, new Set())).toEqual([0, 1]);
  });
});

describe('a customer statement against the ledger', () => {
  const h = (n: number) => '0x' + n.toString(16).padStart(64, '0');
  it('matches, and names every kind of disagreement', () => {
    const out = compareStatement(
      [
        { txHash: h(1), asset: 'ZARP', amount: '100' },
        { txHash: h(2), asset: 'ZARP', amount: '250' },
        { txHash: h(3), asset: 'USDC', amount: '5' },
        { txHash: h(9), asset: 'ZARP', amount: '1' },
      ],
      [
        { txHash: h(1), asset: 'ZARP', amount: '100' },
        { txHash: h(2), asset: 'ZARP', amount: '200' },
        { txHash: h(3), asset: 'ZARP', amount: '5' },
        { txHash: h(4), asset: 'ZARP', amount: '7' },
      ],
    );
    expect(out.matched).toBe(1);
    expect(out.breaks.map((b) => b.classification).sort()).toEqual(
      ['amount_mismatch', 'asset_mismatch', 'missing_in_platform', 'missing_in_statement'].sort(),
    );
  });
});

describe('Solana and Cosmos transactions against what the node reports', () => {
  const now = new Date('2026-10-05T12:00:00Z');
  const entry = (minsAgo: number) => ({ requestId: 'r1', createdAt: new Date(now.getTime() - minsAgo * 60000), txHash: 'h', to: 'DST', amount: '1000' });

  it('a transaction the node has not seen is recent, then missing', () => {
    expect(classifyNative(entry(5), { found: false }, now, 30).classification).toBe('recent');
    expect(classifyNative(entry(90), { found: false }, now, 30).classification).toBe('missing');
  });
  it('an included failure is failed, and carries the reason', () => {
    const c = classifyNative(entry(5), { found: true, failed: true, failure: 'out of gas' }, now, 30);
    expect(c.classification).toBe('failed');
    expect(c.detail).toContain('out of gas');
  });
  it('a transaction not yet confirmed is pending', () => {
    expect(classifyNative(entry(5), { found: true, pending: true }, now, 30).classification).toBe('pending');
  });
  it('one that confirmed and moved what the ledger says is confirmed', () => {
    expect(classifyNative(entry(5), { found: true, moved: { to: 'DST', amount: '1000' } }, now, 30).classification).toBe('confirmed');
  });
  it('one that confirmed and moved something else is a critical mismatch', () => {
    for (const moved of [{ to: 'OTHER', amount: '1000' }, { to: 'DST', amount: '9999' }]) {
      const c = classifyNative(entry(5), { found: true, moved }, now, 30);
      expect(c.classification).toBe('mismatch');
      expect(SEVERITY[c.classification]).toBe('critical');
    }
  });
  it('says so when the node did not report what moved, rather than claiming it was checked', () => {
    expect(classifyNative(entry(5), { found: true }, now, 30).detail).toMatch(/unchecked/);
  });
});
