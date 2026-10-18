import { planSweep } from './sweep-plan';

describe('planSweep', () => {
  it('sweeps everything above the reserve once it reaches the threshold', () => {
    expect(planSweep(1_500_000n, 1_000_000n, 5_000n)).toMatchObject({ sweep: true, amount: 1_495_000n });
  });
  it('leaves the reserve behind, always', () => {
    const p = planSweep(10_000_000n, 1n, 5_000n);
    expect(p.amount).toBe(9_995_000n);
    expect(10_000_000n - p.amount).toBe(5_000n);
  });
  it('does nothing below the threshold, and says why', () => {
    const p = planSweep(1_004_999n, 1_000_000n, 5_000n);
    expect(p.sweep).toBe(false);
    expect(p.amount).toBe(0n);
    expect(p.reason).toMatch(/below the threshold/);
  });
  it('sweeps exactly at the threshold', () => {
    expect(planSweep(1_005_000n, 1_000_000n, 5_000n)).toMatchObject({ sweep: true, amount: 1_000_000n });
  });
  it('does nothing when the balance does not exceed the reserve', () => {
    expect(planSweep(5_000n, 1n, 5_000n).sweep).toBe(false);
    expect(planSweep(0n, 1n, 0n).sweep).toBe(false);
  });
  it('handles balances beyond 2^64 without losing precision', () => {
    const big = 123456789012345678901234567890n;
    expect(planSweep(big, 1n, 0n).amount).toBe(big);
  });
  it('rejects nonsense inputs rather than guessing', () => {
    expect(() => planSweep(-1n, 1n, 0n)).toThrow(RangeError);
    expect(() => planSweep(1n, 0n, 0n)).toThrow(RangeError);
    expect(() => planSweep(1n, 1n, -1n)).toThrow(RangeError);
  });
});
