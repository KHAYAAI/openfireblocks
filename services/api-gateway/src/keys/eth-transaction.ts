import { Signature, Transaction, getAddress } from 'ethers';

// Building and assembling an Ethereum transaction, so that the digest which
// gets threshold-signed is one THIS service computed from fields it also
// handed to the policy engine.
//
// This is the difference between `POST /keys/:keyId/sign` and
// `POST /keys/:keyId/transactions`. The former signs a digest the caller
// supplies: a digest is opaque, so the declared to/value/chainId that policy
// evaluated cannot be checked against what the digest actually commits to,
// and a caller who lies gets a policy decision about a transaction they are
// not signing. Here there is no caller-supplied digest at all -- the fields
// policy sees are the fields that get hashed, and nothing else can be
// signed.
//
// Deliberately free of I/O so it can be tested exhaustively without a
// database, a policy service, or a signing ceremony.

export interface UnsignedTxRequest {
  to: string;
  value: string; // wei, base-10
  data?: string; // 0x-prefixed calldata, or absent for a plain transfer
  gasLimit: number;
  nonce: number;
  chainId: number;
  // Legacy fee, or the EIP-1559 pair. Exactly one form must be supplied;
  // buildUnsignedTransaction rejects both and neither.
  gasPrice?: string;
  maxFeePerGas?: string;
  maxPriorityFeePerGas?: string;
}

export interface BuiltTx {
  // The digest to sign: keccak256 of the serialized unsigned transaction,
  // hex without the 0x prefix (the shape the signing ceremony takes).
  signingHash: string;
  // Carried to assembleSignedTransaction so the two cannot disagree about
  // what was hashed.
  serializedUnsigned: string;
}

export class TransactionBuildError extends Error {}

// Builds the unsigned transaction and returns the digest to sign.
export function buildUnsignedTransaction(req: UnsignedTxRequest): BuiltTx {
  const legacy = req.gasPrice !== undefined;
  const dynamic =
    req.maxFeePerGas !== undefined || req.maxPriorityFeePerGas !== undefined;

  if (legacy && dynamic) {
    throw new TransactionBuildError(
      'supply either gasPrice (legacy) or maxFeePerGas + maxPriorityFeePerGas (EIP-1559), not both',
    );
  }
  if (!legacy && !dynamic) {
    throw new TransactionBuildError(
      'supply either gasPrice (legacy) or maxFeePerGas + maxPriorityFeePerGas (EIP-1559)',
    );
  }
  if (dynamic && (req.maxFeePerGas === undefined || req.maxPriorityFeePerGas === undefined)) {
    throw new TransactionBuildError(
      'maxFeePerGas and maxPriorityFeePerGas must be supplied together',
    );
  }

  let tx: Transaction;
  try {
    tx = Transaction.from(
      legacy
        ? {
            type: 0,
            to: req.to,
            value: BigInt(req.value),
            data: req.data ?? '0x',
            gasLimit: req.gasLimit,
            gasPrice: BigInt(req.gasPrice as string),
            nonce: req.nonce,
            chainId: req.chainId,
          }
        : {
            type: 2,
            to: req.to,
            value: BigInt(req.value),
            data: req.data ?? '0x',
            gasLimit: req.gasLimit,
            maxFeePerGas: BigInt(req.maxFeePerGas as string),
            maxPriorityFeePerGas: BigInt(req.maxPriorityFeePerGas as string),
            nonce: req.nonce,
            chainId: req.chainId,
          },
    );
  } catch (err) {
    throw new TransactionBuildError(
      `could not build transaction: ${(err as Error).message}`,
    );
  }

  return {
    // unsignedHash is keccak256(serialized unsigned tx) -- for a legacy
    // transaction that is the EIP-155 form including chainId, which is why
    // chainId is required rather than optional: without it the same
    // transaction would be replayable on every EVM chain.
    signingHash: tx.unsignedHash.slice(2),
    serializedUnsigned: tx.unsignedSerialized,
  };
}

export interface SignedTx {
  raw: string; // 0x-prefixed signed transaction, ready to broadcast
  hash: string; // its transaction hash
  from: string; // the address recovered from the signature
}

// Attaches a threshold signature to the transaction that produced `built`
// and returns the broadcastable bytes.
//
// Refuses if the recovered signer is not `expectedFrom`. That check is the
// point: a ceremony can complete and return a well-formed signature that
// belongs to a different key -- wrong ceremony id, stale shares, a
// misrouted committee -- and without this the service would hand back a
// perfectly valid transaction spending from an address the customer does
// not control, or one that simply cannot be mined.
export function assembleSignedTransaction(
  built: BuiltTx,
  signatureHex: string,
  expectedFrom: string,
): SignedTx {
  const sig = signatureHex.startsWith('0x') ? signatureHex.slice(2) : signatureHex;
  if (!/^[0-9a-fA-F]{130}$/.test(sig)) {
    throw new TransactionBuildError(
      `signature must be 65 bytes ([R||S||V]), got ${sig.length / 2} bytes`,
    );
  }

  const r = '0x' + sig.slice(0, 64);
  const s = '0x' + sig.slice(64, 128);
  const recovery = parseInt(sig.slice(128, 130), 16);
  if (recovery !== 0 && recovery !== 1) {
    throw new TransactionBuildError(
      `signature recovery byte must be 0 or 1, got ${recovery}`,
    );
  }

  const tx = Transaction.from(built.serializedUnsigned);
  try {
    tx.signature = Signature.from({ r, s, yParity: recovery as 0 | 1 });
  } catch (err) {
    throw new TransactionBuildError(
      `signature is not valid for this transaction: ${(err as Error).message}`,
    );
  }

  const from = tx.from;
  if (!from) {
    throw new TransactionBuildError('could not recover a signer from the signature');
  }
  if (getAddress(from) !== getAddress(expectedFrom)) {
    throw new TransactionBuildError(
      `signature recovered to ${getAddress(from)}, but this key's address is ` +
        `${getAddress(expectedFrom)} -- refusing to return a transaction signed by the wrong key`,
    );
  }

  return { raw: tx.serialized, hash: tx.hash as string, from: getAddress(from) };
}
