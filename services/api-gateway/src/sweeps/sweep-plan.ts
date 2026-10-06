// What a sweep run should do with a balance. Pure, so the arithmetic that
// decides how much money moves is tested without a database or a chain.

export interface SweepPlan {
  sweep: boolean;
  amount: bigint;
  reason: string;
}

// Sweeps (balance - reserve) when that is at least minAmount. The reserve is
// what stays behind for fees and for the next transfer to be affordable; a
// sweep that empties an account leaves nothing to pay the fee of the next
// one, and on some chains an account below a minimum balance stops existing.
export function planSweep(balance: bigint, minAmount: bigint, reserve: bigint): SweepPlan {
  if (balance < 0n || minAmount <= 0n || reserve < 0n) {
    throw new RangeError('balance, minAmount and reserve must be non-negative, and minAmount positive');
  }
  const spendable = balance - reserve;
  if (spendable <= 0n) {
    return { sweep: false, amount: 0n, reason: `balance ${balance} does not exceed the reserve ${reserve}` };
  }
  if (spendable < minAmount) {
    return { sweep: false, amount: 0n, reason: `${spendable} above the reserve is below the threshold ${minAmount}` };
  }
  return { sweep: true, amount: spendable, reason: `${spendable} above the reserve of ${reserve}` };
}
