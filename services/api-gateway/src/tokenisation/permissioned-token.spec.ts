import { ContractFactory, Interface, JsonRpcProvider, NonceManager } from 'ethers';
import { compile } from '../../scripts/build-contracts';
import { PERMISSIONED_TOKEN } from './permissioned-token.artifact';
import { fundedWallet, startChain, TestChain, waitMined } from './test-chain';

// The contract's rules, on a real EVM: an in-process dev chain (ganache) started by this spec.
//
//   npx jest src/tokenisation/permissioned-token

it('the committed artifact is exactly what contracts/PermissionedToken.sol compiles to', () => {
  const fresh = compile();
  expect(PERMISSIONED_TOKEN.sourceSha256).toBe(fresh.sourceSha256);
  expect(PERMISSIONED_TOKEN.bytecode).toBe(fresh.bytecode);
  expect(JSON.stringify(PERMISSIONED_TOKEN.abi)).toBe(JSON.stringify(fresh.abi));
});

// A shared dev chain under parallel load is slow, not broken; a genuinely stuck transaction still fails at this limit.
jest.setTimeout(60000);

describe('PermissionedToken (real EVM)', () => {
  let chain: TestChain; let provider: JsonRpcProvider; let owner: NonceManager; let ownerAddr = '';
  let reachable = false;
  const people: Record<string, NonceManager> = {}; const addr: Record<string, string> = {};
  const iface = new Interface(PERMISSIONED_TOKEN.abi as any);
  let token: any;

  beforeAll(async () => {
    try { chain = await startChain(); provider = chain.provider; reachable = true; } catch (err) { startError = (err as Error).message; return; }
    const base = await fundedWallet(provider);
    owner = new NonceManager(base); ownerAddr = base.address;
    for (const n of ['alice', 'bob', 'carol', 'mallory']) {
      const w = await fundedWallet(provider, '1');
      people[n] = new NonceManager(w); addr[n] = w.address;
    }
  }, 60000);
  afterAll(async () => { await chain?.stop(); });
  let startError = '';
  const skipped = () => {
    if (reachable) return false;
    if (process.env.REQUIRE_EVM_CHAIN) throw new Error(`REQUIRE_EVM_CHAIN is set but the dev chain did not start: ${startError}`);
    console.warn(`skipping contract tests -- ${startError}`);
    return true;
  };
  const deploy = async () => {
    const f = new ContractFactory(PERMISSIONED_TOKEN.abi as any, PERMISSIONED_TOKEN.bytecode, owner);
    const c: any = await f.deploy('Acme Bond 2030', 'ACME30', 6, 1_000_000n * 10n ** 6n, ownerAddr);
    await waitMined(provider, c.deploymentTransaction().hash);
    return c;
  };
  const as = (who: string) => token.connect(people[who]);
  const tx = async (p: Promise<any>) => waitMined(provider, (await p).hash);
  // A revert is read from an eth_call (which carries the reason) and never sent: sending
  // a transaction that fails estimation also burns a nonce on this wallet wrapper.
  const reverts = async (c: any, fn: string, args: unknown[], reason: RegExp) => {
    await expect(c.getFunction(fn).staticCall(...args)).rejects.toThrow(reason);
  };
  const U = (n: number) => BigInt(n) * 10n ** 6n;

  beforeEach(async () => { if (reachable) token = await deploy(); });

  it('has the identity and cap it was deployed with, and the deployer-named owner', async () => {
    if (skipped()) return;
    expect([await token.name(), await token.symbol(), Number(await token.decimals()), await token.supplyCap(), await token.owner()])
      .toEqual(['Acme Bond 2030', 'ACME30', 6, U(1_000_000), ownerAddr]);
    expect(await token.totalSupply()).toBe(0n);
  });

  it('only the owner can administer: admit, mint, burn, freeze, pause, force', async () => {
    if (skipped()) return;
    const m = as('mallory');
    for (const [fn, args] of [['addHolder', [addr.mallory]], ['mint', [addr.mallory, 1]], ['burn', [addr.alice, 1]], ['freeze', [addr.alice]],
      ['pause', []], ['forceTransfer', [addr.alice, addr.mallory, 1]], ['proposeOwner', [addr.mallory]]] as Array<[string, unknown[]]>) {
      await reverts(m, fn, args, /not the owner/);
    }
  });

  it('mints only to an admitted holder and never past the cap', async () => {
    if (skipped()) return;
    await reverts(token, 'mint', [addr.alice, U(10)], /not an admitted holder/);
    await tx(token.addHolder(addr.alice));
    await tx(token.mint(addr.alice, U(900_000)));
    expect(await token.balanceOf(addr.alice)).toBe(U(900_000));
    await reverts(token, 'mint', [addr.alice, U(100_001)], /supply cap/);
    await tx(token.mint(addr.alice, U(100_000)));
    expect(await token.totalSupply()).toBe(U(1_000_000));
  });

  it('lets admitted holders trade with each other and nobody else', async () => {
    if (skipped()) return;
    await tx(token.addHolder(addr.alice)); await tx(token.addHolder(addr.bob)); await tx(token.mint(addr.alice, U(100)));
    await tx(as('alice').transfer(addr.bob, U(40)));
    expect([await token.balanceOf(addr.alice), await token.balanceOf(addr.bob)]).toEqual([U(60), U(40)]);
    await reverts(as('alice'), 'transfer', [addr.mallory, U(1)], /recipient is not an admitted holder/);
    await reverts(as('mallory'), 'transfer', [addr.alice, 0], /sender is not an admitted holder/);
    await reverts(as('alice'), 'transfer', [addr.bob, U(61)], /balance too low/);
  });

  it('enforces the same rules for transferFrom, and a spender cannot route around them', async () => {
    if (skipped()) return;
    await tx(token.addHolder(addr.alice)); await tx(token.addHolder(addr.bob)); await tx(token.mint(addr.alice, U(100)));
    await tx(as('alice').approve(addr.mallory, U(50)));
    await reverts(as('mallory'), 'transferFrom', [addr.alice, addr.mallory, U(10)], /recipient is not an admitted holder/);
    await tx(as('mallory').transferFrom(addr.alice, addr.bob, U(30)));
    expect(await token.allowance(addr.alice, addr.mallory)).toBe(U(20));
    await reverts(as('mallory'), 'transferFrom', [addr.alice, addr.bob, U(21)], /allowance too low/);
  });

  it('a frozen holder can neither send nor receive; unfreezing restores it', async () => {
    if (skipped()) return;
    await tx(token.addHolder(addr.alice)); await tx(token.addHolder(addr.bob)); await tx(token.mint(addr.alice, U(100)));
    await tx(token.freeze(addr.alice));
    await reverts(as('alice'), 'transfer', [addr.bob, 1], /sender is frozen/);
    await tx(token.mint(addr.bob, U(1)));
    await reverts(as('bob'), 'transfer', [addr.alice, 1], /recipient is frozen/);
    await tx(token.unfreeze(addr.alice));
    await tx(as('alice').transfer(addr.bob, 1));
  });

  it('pausing stops every ordinary transfer and the owner\'s powers still work', async () => {
    if (skipped()) return;
    await tx(token.addHolder(addr.alice)); await tx(token.addHolder(addr.bob)); await tx(token.mint(addr.alice, U(100)));
    await tx(token.pause());
    await reverts(as('alice'), 'transfer', [addr.bob, 1], /paused/);
    await tx(token.forceTransfer(addr.alice, addr.bob, U(10)));
    expect(await token.balanceOf(addr.bob)).toBe(U(10));
    await tx(token.unpause());
    await tx(as('alice').transfer(addr.bob, 1));
  });

  it('forces a transfer off a frozen account to an admitted holder only (recovery), and burns for redemption', async () => {
    if (skipped()) return;
    await tx(token.addHolder(addr.alice)); await tx(token.addHolder(addr.bob)); await tx(token.mint(addr.alice, U(100)));
    await tx(token.freeze(addr.alice));
    await reverts(token, 'forceTransfer', [addr.alice, addr.mallory, U(10)], /not an admitted holder/);
    await tx(token.forceTransfer(addr.alice, addr.bob, U(100)));
    expect(await token.balanceOf(addr.alice)).toBe(0n);
    await tx(token.burn(addr.bob, U(60)));
    expect([await token.balanceOf(addr.bob), await token.totalSupply()]).toEqual([U(40), U(40)]);
    await reverts(token, 'burn', [addr.bob, U(41)], /balance too low/);
  });

  it('will not remove a holder who still has a balance, so tokens are never stranded', async () => {
    if (skipped()) return;
    await tx(token.addHolder(addr.alice)); await tx(token.mint(addr.alice, U(5)));
    await reverts(token, 'removeHolder', [addr.alice], /still has a balance/);
    await tx(token.burn(addr.alice, U(5)));
    await tx(token.removeHolder(addr.alice));
    expect(await token.isHolder(addr.alice)).toBe(false);
    await reverts(token, 'removeHolder', [addr.alice], /not a holder/);
  });

  it('moves ownership in two steps: a proposal changes nothing until the proposed owner accepts', async () => {
    if (skipped()) return;
    await tx(token.proposeOwner(addr.carol));
    expect(await token.owner()).toBe(ownerAddr);
    await reverts(as('mallory'), 'acceptOwnership', [], /not the proposed owner/);
    await tx(as('carol').acceptOwnership());
    expect(await token.owner()).toBe(addr.carol);
    await reverts(token, 'mint', [addr.alice, 1], /not the owner/);
  });

  it('canTransfer reports the reason a transfer would fail, for wallets and pre-checks', async () => {
    if (skipped()) return;
    await tx(token.addHolder(addr.alice)); await tx(token.mint(addr.alice, U(1)));
    expect(await token.canTransfer(addr.alice, addr.mallory, 1)).toEqual([false, 'recipient is not an admitted holder']);
    expect(await token.canTransfer(addr.alice, addr.alice, U(2))).toEqual([false, 'balance too low']);
    expect(await token.canTransfer(addr.alice, addr.alice, 1)).toEqual([true, '']);
    expect(iface.getFunction('mint')!.selector).toBe(iface.getFunction('mint(address,uint256)')!.selector);
  });
});
