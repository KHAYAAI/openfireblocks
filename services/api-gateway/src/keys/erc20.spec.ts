import {
  decodeErc20Call,
  encodeTransfer,
  encodeBalanceOf,
  formatUnits,
  parseUnits,
  hasCalldata,
  Erc20DecodeError,
  SELECTOR_TRANSFER,
} from './erc20';

// Reading what a token transaction actually moves.
//
// Every test here is written from the failure it prevents, and the
// failures are all the same shape: the platform signs a transfer while a
// control believes it is looking at something else. That is not a
// hypothetical -- before this decoder existed, every stablecoin transfer
// this platform could produce had `value: "0"` and a `to` of the token
// contract, so the amount limit compared zero against the ceiling and the
// counterparty whitelist saw an address that is identical for every
// transfer of that token.

const RECIPIENT = '0x742d35Cc6634C0532925a3b844Bc454e4438f44e';
const OTHER = '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed';

describe('hasCalldata', () => {
  it.each([undefined, '', '0x'])('treats %p as a native transfer', (data) => {
    expect(hasCalldata(data as string | undefined)).toBe(false);
    expect(decodeErc20Call(data as string | undefined)).toBeNull();
  });
});

describe('decoding a transfer', () => {
  it('reads the recipient and amount out of the calldata', () => {
    const call = decodeErc20Call(encodeTransfer(RECIPIENT, '1000000'));

    expect(call).not.toBeNull();
    expect(call!.method).toBe('transfer');
    expect(call!.recipient).toBe(RECIPIENT);
    expect(call!.amount).toBe('1000000');
    expect(call!.isAllowance).toBe(false);
  });

  // The whole point. A transfer of fifty million rand has value 0 on the
  // transaction; the amount that matters is only in here.
  it('recovers an amount far larger than the native value field ever shows', () => {
    const fiftyMillionAt18 = '50000000000000000000000000';
    const call = decodeErc20Call(encodeTransfer(RECIPIENT, fiftyMillionAt18));

    expect(call!.amount).toBe(fiftyMillionAt18);
  });

  // Token amounts exceed what a double holds exactly. 2^53 is about
  // 9.007e15, which is nine billion USDC at six decimals -- well within
  // range for a settlement platform, and a decoder that went through
  // Number would start losing the low digits there.
  it('does not lose precision above 2^53', () => {
    const big = '123456789012345678901234567890';
    expect(decodeErc20Call(encodeTransfer(RECIPIENT, big))!.amount).toBe(big);
  });

  it('returns the recipient checksummed, so it matches a whitelist entry', () => {
    const lowercase = RECIPIENT.toLowerCase();
    const call = decodeErc20Call(encodeTransfer(lowercase, '1'));

    expect(call!.recipient).toBe(RECIPIENT);
  });
});

describe('decoding transferFrom and approve', () => {
  it('reads transferFrom as a transfer to the recipient, naming the debited account', () => {
    const data =
      '0x23b872dd' +
      OTHER.slice(2).toLowerCase().padStart(64, '0') +
      RECIPIENT.slice(2).toLowerCase().padStart(64, '0') +
      (5000n).toString(16).padStart(64, '0');

    const call = decodeErc20Call(data)!;

    expect(call.method).toBe('transferFrom');
    expect(call.from).toBe(OTHER);
    expect(call.recipient).toBe(RECIPIENT);
    expect(call.amount).toBe('5000');
  });

  // An approval moves nothing at the moment it is signed and authorises
  // the spender to move `amount` whenever they like. Policy has to see it
  // as the exposure it is, not as a zero-value call.
  it('marks approve as an allowance so policy does not read it as moving nothing', () => {
    const data =
      '0x095ea7b3' +
      RECIPIENT.slice(2).toLowerCase().padStart(64, '0') +
      (2n ** 256n - 1n).toString(16).padStart(64, '0');

    const call = decodeErc20Call(data)!;

    expect(call.method).toBe('approve');
    expect(call.isAllowance).toBe(true);
    expect(call.recipient).toBe(RECIPIENT);
    // An unlimited approval. The number matters: this is the single most
    // common way a token balance is drained, and a control that reads it
    // as zero is not a control.
    expect(call.amount).toBe((2n ** 256n - 1n).toString(10));
  });
});

describe('calldata the platform cannot account for', () => {
  // The fail-closed case, and the one that decides whether any of this is
  // worth anything. An unrecognised call must not fall through to being
  // signed with policy having read nothing.
  it('refuses a call it does not recognise rather than passing it through', () => {
    expect(() => decodeErc20Call('0xdeadbeef' + '00'.repeat(32))).toThrow(Erc20DecodeError);
  });

  it('names the selector it could not account for', () => {
    expect(() => decodeErc20Call('0xdeadbeef' + '00'.repeat(32))).toThrow(/0xdeadbeef/);
  });

  // Trailing bytes mean the encoder and this decoder disagree about the
  // layout. Reading the part that parses and ignoring the rest is how a
  // transaction gets approved on the strength of a prefix.
  it('refuses trailing bytes after a well-formed transfer', () => {
    expect(() => decodeErc20Call(encodeTransfer(RECIPIENT, '1') + '00')).toThrow(
      Erc20DecodeError,
    );
  });

  it('refuses a transfer with an argument word missing', () => {
    const truncated = encodeTransfer(RECIPIENT, '1').slice(0, -64);
    expect(() => decodeErc20Call(truncated)).toThrow(Erc20DecodeError);
  });

  // A non-zero high-order prefix on the address word is not an address.
  // Masking it off would invent a plausible recipient out of a payload
  // that meant something else entirely.
  it('refuses an address word with a non-zero high-order prefix', () => {
    const data =
      SELECTOR_TRANSFER +
      ('ff' + '00'.repeat(11) + RECIPIENT.slice(2).toLowerCase()) +
      (1n).toString(16).padStart(64, '0');

    expect(() => decodeErc20Call(data)).toThrow(/high-order prefix/);
  });

  it('refuses a transfer to the zero address', () => {
    const data = SELECTOR_TRANSFER + '0'.repeat(64) + (1n).toString(16).padStart(64, '0');
    expect(() => decodeErc20Call(data)).toThrow(/zero address/);
  });

  it('refuses calldata that is not whole bytes of hex', () => {
    expect(() => decodeErc20Call('0xabc')).toThrow(Erc20DecodeError);
  });

  it('refuses calldata too short to carry a selector', () => {
    expect(() => decodeErc20Call('0xa9059c')).toThrow(Erc20DecodeError);
  });

  // Selectors are compared lowercase; a caller that upper-cases their hex
  // is still making the same call.
  it('recognises a selector regardless of hex case', () => {
    const upper = '0xA9059CBB' + RECIPIENT.slice(2).padStart(64, '0') + '1'.padStart(64, '0');
    expect(decodeErc20Call(upper)!.method).toBe('transfer');
  });
});

describe('encoding a transfer', () => {
  it('round-trips through the decoder', () => {
    const call = decodeErc20Call(encodeTransfer(RECIPIENT, '123456'))!;
    expect(call.recipient).toBe(RECIPIENT);
    expect(call.amount).toBe('123456');
  });

  it('produces exactly a selector and two argument words', () => {
    // 4 bytes + 32 + 32 = 68 bytes = 136 hex characters, plus "0x".
    expect(encodeTransfer(RECIPIENT, '1')).toHaveLength(138);
  });

  it.each([
    ['the zero address', '0x0000000000000000000000000000000000000000'],
    ['a truncated address', '0x742d35Cc6634C0532925a3b844Bc454e4438f4'],
    ['not an address at all', 'somebody'],
  ])('refuses %s', (_name, recipient) => {
    expect(() => encodeTransfer(recipient, '1')).toThrow(Erc20DecodeError);
  });

  it.each(['0', '-1', 'abc', ''])('refuses the amount %p', (amount) => {
    expect(() => encodeTransfer(RECIPIENT, amount)).toThrow(Erc20DecodeError);
  });
});

describe('balanceOf encoding', () => {
  it('is a selector and one address word', () => {
    const data = encodeBalanceOf(RECIPIENT);
    expect(data).toHaveLength(74); // 0x + 4 bytes + 32 bytes
    expect(data.slice(0, 10)).toBe('0x70a08231');
    expect(data.slice(10)).toBe(RECIPIENT.slice(2).toLowerCase().padStart(64, '0'));
  });
});

describe('base units and decimal amounts', () => {
  // The 10^12 error. USDC is 6 decimals and DAI is 18, and reading one
  // for the other turns a thousand-dollar limit into a billion-dollar one.
  it('formats the same base units differently for USDC and DAI', () => {
    expect(formatUnits('1000000', 6)).toBe('1');
    expect(formatUnits('1000000', 18)).toBe('0.000000000001');
  });

  it('formats without floating point in the low digits', () => {
    // 2^53 base units at 18 decimals. Through a double, the trailing
    // digits of this come back wrong.
    expect(formatUnits('9007199254740993', 18)).toBe('0.009007199254740993');
  });

  it.each([
    ['1', 6, '1000000'],
    ['0.5', 6, '500000'],
    ['1.000001', 6, '1000001'],
    ['1', 18, '1000000000000000000'],
    ['1234.56', 2, '123456'],
  ])('parses %s at %i decimals to %s', (amount, decimals, expected) => {
    expect(parseUnits(amount as string, decimals as number)).toBe(expected);
  });

  it('round-trips', () => {
    expect(formatUnits(parseUnits('1234.567891', 6), 6)).toBe('1234.567891');
  });

  // Refusing rather than rounding. A customer asking to send 1.0000005
  // USDC has misunderstood the token; sending them 1.000000 silently is a
  // worse answer than telling them.
  it('refuses more precision than the token has', () => {
    expect(() => parseUnits('1.0000005', 6)).toThrow(/decimal places/);
  });

  it.each(['1e6', '1_000', '+1', '-1', '1,000', ' 1', 'abc'])(
    'refuses the amount %p rather than reinterpreting it',
    (amount) => {
      expect(() => parseUnits(amount, 6)).toThrow(Erc20DecodeError);
    },
  );

  it('refuses an amount that rounds to nothing', () => {
    expect(() => parseUnits('0.0000001', 6)).toThrow();
    expect(() => parseUnits('0', 6)).toThrow();
  });
});
