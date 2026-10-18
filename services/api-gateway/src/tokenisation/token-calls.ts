import { Interface } from 'ethers';
import { PERMISSIONED_TOKEN } from './permissioned-token.artifact';

// The administrative acts on a security token, as calldata: building them for the
// approvers, and recognising them again at signing time. The same table does both, so what
// the platform will sign is exactly what it will describe.

export type TokenOp = 'admit' | 'remove' | 'freeze' | 'unfreeze' | 'pause' | 'unpause' | 'mint' | 'burn' | 'force_transfer' | 'propose_owner';

export const iface = new Interface(PERMISSIONED_TOKEN.abi as any);

interface OpSpec {
  fn: string;
  // Parameter names, in the order of the contract function's arguments.
  params: string[];
}

export const OPS: Record<TokenOp, OpSpec> = {
  admit: { fn: 'addHolder', params: ['holder'] },
  remove: { fn: 'removeHolder', params: ['holder'] },
  freeze: { fn: 'freeze', params: ['holder'] },
  unfreeze: { fn: 'unfreeze', params: ['holder'] },
  pause: { fn: 'pause', params: [] },
  unpause: { fn: 'unpause', params: [] },
  mint: { fn: 'mint', params: ['to', 'amount'] },
  burn: { fn: 'burn', params: ['from', 'amount'] },
  force_transfer: { fn: 'forceTransfer', params: ['from', 'to', 'amount'] },
  propose_owner: { fn: 'proposeOwner', params: ['next'] },
};

const BY_FUNCTION = new Map(Object.entries(OPS).map(([op, s]) => [s.fn, op as TokenOp]));
const ADDRESS = /^0x[0-9a-fA-F]{40}$/;
const AMOUNT = /^[1-9][0-9]{0,77}$/;

export class TokenCallError extends Error {}

export function units(amount: string, decimals: number): string {
  const a = BigInt(amount); const base = 10n ** BigInt(decimals);
  const whole = a / base; const frac = (a % base).toString().padStart(decimals, '0').replace(/0+$/, '');
  return frac ? `${whole}.${frac}` : `${whole}`;
}

function short(a: string) { return `${a.slice(0, 8)}…${a.slice(-4)}`; }

// In words, for the people asked to approve it.
export function describe(op: TokenOp, p: Record<string, string>, symbol: string, decimals: number): string {
  switch (op) {
    case 'admit': return `admit ${p.holder} as a holder of ${symbol}`;
    case 'remove': return `remove ${p.holder} as a holder of ${symbol}`;
    case 'freeze': return `freeze ${p.holder}: it can neither send nor receive ${symbol}`;
    case 'unfreeze': return `unfreeze ${p.holder}`;
    case 'pause': return `PAUSE all ordinary ${symbol} transfers`;
    case 'unpause': return `resume ${symbol} transfers`;
    case 'mint': return `mint ${units(p.amount, decimals)} ${symbol} to ${p.to}`;
    case 'burn': return `burn ${units(p.amount, decimals)} ${symbol} held by ${p.from}`;
    case 'force_transfer': return `FORCE-move ${units(p.amount, decimals)} ${symbol} from ${p.from} to ${p.to}, without the holder's consent`;
    case 'propose_owner': return `propose ${p.next} as the new OWNER of ${symbol}, with every power the owner has`;
  }
}

export function buildCall(op: TokenOp, raw: Record<string, unknown>): { data: string; params: Record<string, string> } {
  const spec = OPS[op];
  if (!spec) throw new TokenCallError(`unknown operation ${JSON.stringify(op)}`);
  const params: Record<string, string> = {};
  for (const name of spec.params) {
    const v = raw[name];
    if (name === 'amount') {
      if (typeof v !== 'string' || !AMOUNT.test(v)) throw new TokenCallError('amount must be a positive integer in base units, as a string');
      params[name] = v;
    } else {
      if (typeof v !== 'string' || !ADDRESS.test(v)) throw new TokenCallError(`${name} must be a 20-byte hex address`);
      params[name] = v.toLowerCase();
    }
  }
  const extra = Object.keys(raw).filter((k) => !spec.params.includes(k) && k !== 'op');
  if (extra.length) throw new TokenCallError(`${op} does not take ${extra.join(', ')}`);
  return { data: iface.encodeFunctionData(spec.fn, spec.params.map((n) => params[n])), params };
}

// Reads calldata back. Accepts only the administrative functions above, and only if the
// bytes are exactly what encoding the decoded arguments produces: trailing bytes, padding
// tricks and anything else the contract's ABI decoder would tolerate are refused here.
export function recogniseCall(data: string): { op: TokenOp; params: Record<string, string> } | null {
  // Everything inside the try: ethers decodes lazily, so a malformed argument (an address
  // word with dirty high bytes, say) throws when it is first read, not when it is parsed.
  // Calldata that cannot be read is calldata that is not recognised, never an error.
  try {
    const parsed = iface.parseTransaction({ data });
    if (!parsed) return null;
    const op = BY_FUNCTION.get(parsed.name);
    if (!op) return null;
    const spec = OPS[op];
    const params: Record<string, string> = {};
    spec.params.forEach((name, i) => { params[name] = name === 'amount' ? parsed.args[i].toString() : String(parsed.args[i]).toLowerCase(); });
    if (iface.encodeFunctionData(spec.fn, parsed.args).toLowerCase() !== data.toLowerCase()) return null;
    return { op, params };
  } catch {
    return null;
  }
}

// Is the code at an address the audited contract? Compared with the constructor's two
// immutables (decimals, supplyCap) masked out of both, since those differ per deployment.
export function isAuditedCode(deployed: string): boolean {
  const strip = (hex: string) => {
    const bytes = Buffer.from(hex.replace(/^0x/, ''), 'hex');
    for (const r of PERMISSIONED_TOKEN.immutableReferences) bytes.fill(0, r.start, r.start + r.length);
    return bytes.toString('hex');
  };
  try { return strip(deployed) === strip(PERMISSIONED_TOKEN.deployedBytecode); } catch { return false; }
}

export { short };
