import { PERMISSIONED_TOKEN } from './permissioned-token.artifact';
import { buildCall, describe as words, isAuditedCode, iface, OPS, recogniseCall, TokenCallError, TokenOp, units } from './token-calls';

const A = '0x' + 'ab'.repeat(20); const B = '0x' + 'cd'.repeat(20); const C = '0x' + 'ef'.repeat(20);
const args: Record<TokenOp, Record<string, string>> = {
  admit: { holder: A }, remove: { holder: A }, freeze: { holder: A }, unfreeze: { holder: A }, pause: {}, unpause: {},
  mint: { to: A, amount: '1500000' }, burn: { from: A, amount: '7' }, force_transfer: { from: A, to: B, amount: '99' }, propose_owner: { next: C },
};

describe('the calls the platform will read', () => {
  it.each(Object.keys(OPS) as TokenOp[])('%s: builds calldata and reads exactly the same thing back', (op) => {
    const { data, params } = buildCall(op, args[op]);
    expect(recogniseCall(data)).toEqual({ op, params });
    expect(params).toEqual(args[op]);
  });

  it('refuses calldata with anything appended, however harmless it looks', () => {
    const { data } = buildCall('mint', args.mint);
    expect(recogniseCall(data + '00')).toBeNull();
    expect(recogniseCall(data + 'deadbeef')).toBeNull();
    expect(recogniseCall(data.slice(0, -2))).toBeNull();
  });

  it('refuses a selector that is not an administrative act: transfers, approvals, anything else', () => {
    expect(recogniseCall(iface.encodeFunctionData('transfer', [A, 1]))).toBeNull();
    expect(recogniseCall(iface.encodeFunctionData('approve', [A, 1]))).toBeNull();
    expect(recogniseCall(iface.encodeFunctionData('transferFrom', [A, B, 1]))).toBeNull();
    expect(recogniseCall(iface.encodeFunctionData('acceptOwnership', []))).toBeNull();
    expect(recogniseCall('0xdeadbeef')).toBeNull();
    expect(recogniseCall('0x')).toBeNull();
  });

  it('refuses a mint whose address word has dirty high bytes', () => {
    const { data } = buildCall('mint', args.mint);
    const dirty = data.slice(0, 10) + 'ff' + data.slice(12);
    expect(recogniseCall(dirty)).toBeNull();
  });

  it('validates arguments strictly when building', () => {
    expect(() => buildCall('mint', { to: A, amount: '1.5' })).toThrow(TokenCallError);
    expect(() => buildCall('mint', { to: A, amount: '0' })).toThrow(/positive/);
    expect(() => buildCall('mint', { to: A })).toThrow(/amount/);
    expect(() => buildCall('mint', { to: 'nope', amount: '1' })).toThrow(/address/);
    expect(() => buildCall('pause', { amount: '1' })).toThrow(/does not take amount/);
    expect(() => buildCall('selfdestruct' as TokenOp, {})).toThrow(/unknown operation/);
  });

  it('describes each act in words an approver can act on, and shouts the dangerous ones', () => {
    expect(words('mint', args.mint, 'ACME', 6)).toBe(`mint 1.5 ACME to ${A}`);
    expect(words('force_transfer', args.force_transfer, 'ACME', 0)).toMatch(/^FORCE-move 99 ACME .* without the holder's consent$/);
    expect(words('propose_owner', args.propose_owner, 'ACME', 6)).toMatch(/new OWNER.*every power/);
    expect(words('pause', {}, 'ACME', 6)).toMatch(/^PAUSE/);
  });

  it('formats base units exactly, with no float in sight', () => {
    expect(units('1', 6)).toBe('0.000001');
    expect(units('1000000', 6)).toBe('1');
    expect(units('123456789012345678901234567890', 18)).toBe('123456789012.34567890123456789');
    expect(units('5', 0)).toBe('5');
  });
});

describe('is this the audited contract?', () => {
  it('accepts the audited code', () => {
    expect(isAuditedCode(PERMISSIONED_TOKEN.deployedBytecode)).toBe(true);
  });
  it('accepts it with different constructor immutables (decimals, supply cap), which differ per deployment', () => {
    const bytes = Buffer.from(PERMISSIONED_TOKEN.deployedBytecode.slice(2), 'hex');
    for (const r of PERMISSIONED_TOKEN.immutableReferences) bytes.fill(0x7f, r.start, r.start + r.length);
    expect(isAuditedCode('0x' + bytes.toString('hex'))).toBe(true);
  });
  it('refuses a single changed byte anywhere else', () => {
    const base = Buffer.from(PERMISSIONED_TOKEN.deployedBytecode.slice(2), 'hex');
    const refs = PERMISSIONED_TOKEN.immutableReferences;
    const inside = (i: number) => refs.some((r) => i >= r.start && i < r.start + r.length);
    let tried = 0;
    for (let i = 0; i < base.length && tried < 60; i += 97) {
      if (inside(i)) continue;
      const b = Buffer.from(base); b[i] ^= 1; tried++;
      expect(isAuditedCode('0x' + b.toString('hex'))).toBe(false);
    }
    expect(tried).toBeGreaterThan(20);
  });
  it('refuses empty code, truncated code and appended code', () => {
    expect(isAuditedCode('0x')).toBe(false);
    expect(isAuditedCode(PERMISSIONED_TOKEN.deployedBytecode.slice(0, -2))).toBe(false);
    expect(isAuditedCode(PERMISSIONED_TOKEN.deployedBytecode + '00')).toBe(false);
    expect(isAuditedCode('not hex')).toBe(false);
  });
});
