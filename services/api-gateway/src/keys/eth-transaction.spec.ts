import { Transaction, Wallet, getAddress, keccak256, toUtf8Bytes } from 'ethers';
import {
  TransactionBuildError,
  assembleSignedTransaction,
  buildUnsignedTransaction,
} from './eth-transaction';

// A throwaway key, used only to produce real signatures for these tests.
// Threshold signatures and single-key signatures are indistinguishable at
// this layer by construction -- that is the whole point of threshold ECDSA
// -- so signing here with a Wallet exercises exactly the code path a real
// ceremony's output takes.
const wallet = new Wallet(
  '0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d',
);

const base = {
  to: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8',
  value: '1000000000000000000',
  gasLimit: 21000,
  nonce: 7,
  chainId: 11155111,
};

// Signs `hash` the way a threshold ceremony does: 65 bytes [R||S||V] with V
// as a 0/1 recovery byte, not 27/28.
function thresholdStyleSignature(hash: string): string {
  const sig = wallet.signingKey.sign('0x' + hash);
  return (
    sig.r.slice(2) + sig.s.slice(2) + (sig.yParity === 0 ? '00' : '01')
  );
}

describe('buildUnsignedTransaction', () => {
  it('produces the same signing hash ethers itself would', () => {
    const built = buildUnsignedTransaction({ ...base, gasPrice: '20000000000' });

    const reference = Transaction.from({
      type: 0,
      to: base.to,
      value: BigInt(base.value),
      data: '0x',
      gasLimit: base.gasLimit,
      gasPrice: 20000000000n,
      nonce: base.nonce,
      chainId: base.chainId,
    });

    expect('0x' + built.signingHash).toBe(reference.unsignedHash);
  });

  // The digest must commit to the chain id, or the same signed transaction
  // replays on every other EVM chain.
  it('changes the signing hash when only the chain id changes', () => {
    const a = buildUnsignedTransaction({ ...base, gasPrice: '20000000000' });
    const b = buildUnsignedTransaction({
      ...base,
      chainId: 1,
      gasPrice: '20000000000',
    });
    expect(a.signingHash).not.toBe(b.signingHash);
  });

  // Every field policy evaluates has to be one the digest commits to,
  // otherwise the guarantee this module exists for does not hold.
  it.each([
    ['to', { to: '0x1111111111111111111111111111111111111111' }],
    ['value', { value: '2000000000000000000' }],
    ['nonce', { nonce: 8 }],
    ['gasLimit', { gasLimit: 30000 }],
    ['gasPrice', { gasPrice: '30000000000' }],
    ['data', { data: '0xdeadbeef' }],
  ])('changes the signing hash when %s changes', (_label, override) => {
    const a = buildUnsignedTransaction({ ...base, gasPrice: '20000000000' });
    const b = buildUnsignedTransaction({
      ...base,
      gasPrice: '20000000000',
      ...override,
    });
    expect(a.signingHash).not.toBe(b.signingHash);
  });

  it('builds EIP-1559 transactions', () => {
    const built = buildUnsignedTransaction({
      ...base,
      maxFeePerGas: '30000000000',
      maxPriorityFeePerGas: '1000000000',
    });
    // Type-2 transactions are typed-envelope encoded, so the serialization
    // starts with the 0x02 type byte.
    expect(built.serializedUnsigned.startsWith('0x02')).toBe(true);
  });

  it.each([
    ['both fee forms', { gasPrice: '1', maxFeePerGas: '2', maxPriorityFeePerGas: '1' }],
    ['neither fee form', {}],
    ['half the 1559 pair', { maxFeePerGas: '2' }],
  ])('rejects %s', (_label, override) => {
    expect(() => buildUnsignedTransaction({ ...base, ...override })).toThrow(
      TransactionBuildError,
    );
  });

  it('rejects a malformed recipient rather than building something unsignable', () => {
    expect(() =>
      buildUnsignedTransaction({ ...base, to: 'not-an-address', gasPrice: '1' }),
    ).toThrow(TransactionBuildError);
  });
});

describe('assembleSignedTransaction', () => {
  it('returns broadcastable bytes whose sender is the key address', () => {
    const built = buildUnsignedTransaction({ ...base, gasPrice: '20000000000' });
    const signed = assembleSignedTransaction(
      built,
      thresholdStyleSignature(built.signingHash),
      wallet.address,
    );

    expect(signed.from).toBe(getAddress(wallet.address));
    expect(signed.raw.startsWith('0x')).toBe(true);
    // Re-parsing the raw bytes must yield the same transaction, which is
    // what a node will do on receipt.
    const parsed = Transaction.from(signed.raw);
    expect(parsed.to).toBe(getAddress(base.to));
    expect(parsed.value).toBe(BigInt(base.value));
    expect(parsed.nonce).toBe(base.nonce);
    expect(parsed.chainId).toBe(BigInt(base.chainId));
    expect(parsed.from).toBe(getAddress(wallet.address));
  });

  // The check this module exists for. A ceremony can complete and return a
  // well-formed signature belonging to a different key -- wrong ceremony id,
  // stale shares, a misrouted committee. Returning that would hand the
  // customer a valid transaction spending from an address they do not
  // control.
  it('refuses a signature that recovers to a different address', () => {
    const built = buildUnsignedTransaction({ ...base, gasPrice: '20000000000' });
    const otherKey = Wallet.createRandom();

    expect(() =>
      assembleSignedTransaction(
        built,
        thresholdStyleSignature(built.signingHash),
        otherKey.address,
      ),
    ).toThrow(/refusing to return a transaction signed by the wrong key/);
  });

  // A signature over a different digest recovers to some unrelated address,
  // so the same guard catches it.
  it('refuses a signature over a different message', () => {
    const built = buildUnsignedTransaction({ ...base, gasPrice: '20000000000' });
    const wrongDigest = keccak256(toUtf8Bytes('a different transaction')).slice(2);

    expect(() =>
      assembleSignedTransaction(
        built,
        thresholdStyleSignature(wrongDigest),
        wallet.address,
      ),
    ).toThrow(TransactionBuildError);
  });

  it.each([
    ['too short', 'ff'.repeat(64)],
    ['too long', 'ff'.repeat(66)],
    ['not hex', 'z'.repeat(130)],
  ])('rejects a signature that is %s', (_label, sig) => {
    const built = buildUnsignedTransaction({ ...base, gasPrice: '20000000000' });
    expect(() => assembleSignedTransaction(built, sig, wallet.address)).toThrow(
      /signature must be 65 bytes/,
    );
  });

  it('rejects a recovery byte that is not 0 or 1', () => {
    const built = buildUnsignedTransaction({ ...base, gasPrice: '20000000000' });
    const sig = thresholdStyleSignature(built.signingHash).slice(0, 128) + '1b';
    expect(() => assembleSignedTransaction(built, sig, wallet.address)).toThrow(
      /recovery byte must be 0 or 1/,
    );
  });

  it('accepts a 0x-prefixed signature as well as a bare one', () => {
    const built = buildUnsignedTransaction({ ...base, gasPrice: '20000000000' });
    const sig = thresholdStyleSignature(built.signingHash);
    const a = assembleSignedTransaction(built, sig, wallet.address);
    const b = assembleSignedTransaction(built, '0x' + sig, wallet.address);
    expect(a.raw).toBe(b.raw);
  });

  it('round-trips an EIP-1559 transaction', () => {
    const built = buildUnsignedTransaction({
      ...base,
      maxFeePerGas: '30000000000',
      maxPriorityFeePerGas: '1000000000',
    });
    const signed = assembleSignedTransaction(
      built,
      thresholdStyleSignature(built.signingHash),
      wallet.address,
    );
    const parsed = Transaction.from(signed.raw);
    expect(parsed.type).toBe(2);
    expect(parsed.from).toBe(getAddress(wallet.address));
  });
});
