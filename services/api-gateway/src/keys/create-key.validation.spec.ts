import { BadRequestException } from '@nestjs/common';
import { validateCreateKey } from './create-key.validation';

const key = (threshold: number, total_parties: number) => ({ blockchain: 'ethereum', threshold, total_parties } as never);

describe('validateCreateKey', () => {
  it.each([[2, 3], [2, 2], [3, 4], [3, 5], [4, 6], [5, 7]])('accepts %i-of-%i (a majority)', (t, n) => {
    expect(() => validateCreateKey(key(t, n))).not.toThrow();
  });
  it.each([[1, 3], [2, 4], [2, 5], [3, 6], [1, 2]])('refuses %i-of-%i (not a majority)', (t, n) => {
    expect(() => validateCreateKey(key(t, n))).toThrow(BadRequestException);
  });
  it('refuses a threshold above the party count', () => {
    expect(() => validateCreateKey(key(4, 3))).toThrow(/<= total_parties/);
  });
  it('refuses a single-party key unless explicitly allowed for development', () => {
    expect(() => validateCreateKey(key(1, 1))).toThrow(/single-party/);
    process.env.ALLOW_SINGLE_PARTY_KEYS = 'true';
    try { expect(() => validateCreateKey(key(1, 1))).not.toThrow(); } finally { delete process.env.ALLOW_SINGLE_PARTY_KEYS; }
  });
  it('still refuses an unknown chain', () => {
    expect(() => validateCreateKey({ blockchain: 'dogecoin', threshold: 2, total_parties: 3 } as never)).toThrow(/Unsupported/);
  });
});
