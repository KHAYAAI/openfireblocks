import { getAddress } from 'ethers';

// Reading and writing ERC-20 calldata, so that the platform knows what a
// transaction moves before it agrees to sign it.
//
// The problem this solves: an ERC-20 transfer puts the recipient and the
// amount inside the calldata. The transaction's own `to` is the token
// contract and its `value` is zero. Every control in this platform -- the
// amount limit, the counterparty whitelist, the daily aggregate that
// decides whether a regulatory filing is due -- reads `to` and `value`. So
// before this file existed, a stablecoin transfer of any size, to anyone,
// passed every one of them: the whitelist saw the same contract address it
// sees for every transfer of that token, and the amount limit compared
// zero against the ceiling.
//
// Nothing here does I/O. Decoding is a pure function of the bytes, which
// is what makes it exhaustively testable, and the registry lookup that
// turns a contract address into a symbol and a decimals is the caller's
// job.

// Selectors are the first four bytes of the keccak hash of the signature.
// Written out rather than derived at runtime so that a reader can check
// them against the ABI by eye, and so a mistake is a wrong constant rather
// than a wrong hash function.
export const SELECTOR_TRANSFER = '0xa9059cbb'; // transfer(address,uint256)
export const SELECTOR_TRANSFER_FROM = '0x23b872dd'; // transferFrom(address,address,uint256)
export const SELECTOR_APPROVE = '0x095ea7b3'; // approve(address,uint256)
export const SELECTOR_BALANCE_OF = '0x70a08231'; // balanceOf(address)
export const SELECTOR_DECIMALS = '0x313ce567'; // decimals()
export const SELECTOR_SYMBOL = '0x95d89b41'; // symbol()

export type Erc20Method = 'transfer' | 'transferFrom' | 'approve';

export interface Erc20Call {
  method: Erc20Method;
  // Checksummed. For transfer and approve this is the recipient or the
  // spender; for transferFrom it is the recipient, and `from` carries the
  // account being debited.
  recipient: string;
  from?: string;
  // Base units, as a base-10 string. A string rather than a bigint because
  // it crosses a JSON boundary to the policy service, and a number would
  // have lost precision long before it got there.
  amount: string;
  // True for approve, which moves nothing now but authorises a contract to
  // move everything later. Callers that treat an approval as a transfer of
  // `amount` are being conservative in the right direction; callers that
  // treat it as moving nothing are wrong.
  isAllowance: boolean;
}

export class Erc20DecodeError extends Error {}

const HEX = /^0x([0-9a-fA-F]{2})*$/;

// Is there any calldata at all? Empty, "0x" and undefined are all a plain
// native transfer.
export function hasCalldata(data?: string): boolean {
  return data !== undefined && data !== '' && data !== '0x';
}

// Splits 0x-prefixed calldata into a selector and 32-byte words.
//
// Strict about length. ABI encoding is fixed-width for these signatures,
// so trailing bytes mean the caller encoded something this decoder does
// not model -- and a decoder that ignores bytes it does not understand is
// how a transaction gets approved on the strength of the part that was
// read.
function words(data: string, expected: number): string[] {
  const body = data.slice(10); // strip "0x" + 4-byte selector
  if (body.length !== expected * 64) {
    throw new Erc20DecodeError(
      `expected ${expected} argument word(s) after the selector, got ${body.length / 2} bytes`,
    );
  }
  const out: string[] = [];
  for (let i = 0; i < expected; i++) {
    out.push(body.slice(i * 64, (i + 1) * 64));
  }
  return out;
}

// An ABI address is right-aligned in a 32-byte word. The upper 12 bytes
// must be zero.
//
// Checked rather than masked off. A non-zero prefix means the encoder and
// this decoder disagree about the argument layout, and quietly taking the
// low 20 bytes would invent a plausible address out of a payload that
// meant something else.
function addressFromWord(word: string, what: string): string {
  const padding = word.slice(0, 24);
  if (!/^0{24}$/.test(padding)) {
    throw new Erc20DecodeError(
      `the ${what} argument has a non-zero high-order prefix (0x${padding}); ` +
        'this is not an ABI-encoded address',
    );
  }
  const addr = '0x' + word.slice(24);
  if (/^0x0{40}$/.test(addr)) {
    throw new Erc20DecodeError(
      `the ${what} is the zero address; a transfer there burns the tokens irrecoverably`,
    );
  }
  try {
    return getAddress(addr);
  } catch {
    throw new Erc20DecodeError(`the ${what} argument is not a valid address`);
  }
}

function amountFromWord(word: string): string {
  // Always in range: 32 bytes is exactly uint256, so there is nothing to
  // bounds-check. BigInt rather than Number -- token amounts routinely
  // exceed 2^53 and a float would silently round the low digits away.
  return BigInt('0x' + word).toString(10);
}

// Decodes calldata, or explains why it cannot.
//
// Throws rather than returning null for calldata that is present but
// unrecognised, because those are different situations with different
// correct responses: `null` means "no calldata, this is a native
// transfer", and a throw means "there is a payload here and the platform
// does not know what it does".
export function decodeErc20Call(data?: string): Erc20Call | null {
  if (!hasCalldata(data)) {
    return null;
  }
  const hex = data as string;
  if (!HEX.test(hex)) {
    throw new Erc20DecodeError('calldata is not 0x-prefixed hex of whole bytes');
  }
  if (hex.length < 10) {
    throw new Erc20DecodeError(
      `calldata is ${(hex.length - 2) / 2} bytes; a contract call is at least a 4-byte selector`,
    );
  }

  const selector = hex.slice(0, 10).toLowerCase();

  switch (selector) {
    case SELECTOR_TRANSFER: {
      const [to, value] = words(hex, 2);
      return {
        method: 'transfer',
        recipient: addressFromWord(to, 'recipient'),
        amount: amountFromWord(value),
        isAllowance: false,
      };
    }
    case SELECTOR_TRANSFER_FROM: {
      const [from, to, value] = words(hex, 3);
      return {
        method: 'transferFrom',
        from: addressFromWord(from, 'source account'),
        recipient: addressFromWord(to, 'recipient'),
        amount: amountFromWord(value),
        isAllowance: false,
      };
    }
    case SELECTOR_APPROVE: {
      const [spender, value] = words(hex, 2);
      // The spender is the counterparty for policy purposes. An approval
      // is not a payment, but it is the authority to take one, and the
      // address that ends up holding the tokens is the spender's.
      return {
        method: 'approve',
        recipient: addressFromWord(spender, 'spender'),
        amount: amountFromWord(value),
        isAllowance: true,
      };
    }
    default:
      throw new Erc20DecodeError(
        `unrecognised contract call ${selector}. The platform can only evaluate ` +
          'transfer, transferFrom and approve against policy; anything else would be ' +
          'signed without any control having read what it does',
      );
  }
}

function padWord(hex: string): string {
  return hex.padStart(64, '0');
}

// Builds transfer(address,uint256) calldata.
//
// The point of the platform encoding this rather than accepting it from
// the caller: when the customer supplies calldata, the recipient and
// amount that policy evaluated are whatever the decoder read out of bytes
// the customer chose. That is sound only because the decoder above is
// strict. When the platform encodes, there is nothing to disagree about --
// the same recipient and amount go to policy and into the signed bytes.
export function encodeTransfer(recipient: string, amount: string): string {
  let checksummed: string;
  try {
    checksummed = getAddress(recipient);
  } catch {
    throw new Erc20DecodeError(`${recipient} is not a valid Ethereum address`);
  }
  if (/^0x0{40}$/i.test(checksummed)) {
    throw new Erc20DecodeError(
      'refusing to transfer to the zero address; the tokens would be irrecoverable',
    );
  }

  let value: bigint;
  try {
    value = BigInt(amount);
  } catch {
    throw new Erc20DecodeError(`amount ${amount} is not a base-10 integer`);
  }
  if (value <= 0n) {
    throw new Erc20DecodeError('amount must be greater than zero');
  }
  if (value >= 1n << 256n) {
    throw new Erc20DecodeError('amount does not fit in uint256');
  }

  return (
    SELECTOR_TRANSFER +
    padWord(checksummed.slice(2).toLowerCase()) +
    padWord(value.toString(16))
  );
}

// balanceOf(address) calldata, for reading a token balance over eth_call.
export function encodeBalanceOf(owner: string): string {
  return SELECTOR_BALANCE_OF + padWord(getAddress(owner).slice(2).toLowerCase());
}

// Turns base units into a human decimal string, without floating point.
//
// Used for display and for the money-denominated controls. Done with
// string arithmetic because 1 USDC is 10^6 base units and 1 DAI is 10^18:
// the latter exceeds what a double represents exactly, so a balance
// formatted through a float is wrong in the low digits of every large
// holding.
export function formatUnits(baseUnits: string, decimals: number): string {
  const negative = baseUnits.startsWith('-');
  const digits = (negative ? baseUnits.slice(1) : baseUnits).padStart(decimals + 1, '0');
  const whole = digits.slice(0, digits.length - decimals);
  const frac = decimals === 0 ? '' : digits.slice(digits.length - decimals).replace(/0+$/, '');
  return `${negative ? '-' : ''}${whole}${frac ? '.' + frac : ''}`;
}

// The inverse. Rejects more precision than the token has rather than
// rounding it away: a customer who asks to send 1.0000005 USDC has
// misunderstood something, and silently sending 1.000000 is a worse answer
// than refusing.
export function parseUnits(amount: string, decimals: number): string {
  if (!/^[0-9]+(\.[0-9]+)?$/.test(amount)) {
    throw new Erc20DecodeError(
      `amount ${amount} must be a non-negative decimal number with no sign, exponent or separators`,
    );
  }
  const [whole, frac = ''] = amount.split('.');
  if (frac.length > decimals) {
    throw new Erc20DecodeError(
      `amount ${amount} has ${frac.length} decimal places but this token has ${decimals}; ` +
        'the extra precision cannot be represented and will not be rounded away silently',
    );
  }
  const base = `${whole}${frac.padEnd(decimals, '0')}`.replace(/^0+(?=\d)/, '');
  if (base === '0') {
    throw new Erc20DecodeError('amount must be greater than zero');
  }
  return base;
}
