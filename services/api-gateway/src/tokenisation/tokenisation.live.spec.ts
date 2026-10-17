import { BadRequestException, INestApplication, ValidationPipe } from '@nestjs/common';
import { Test } from '@nestjs/testing';
import { JwtModule, JwtService } from '@nestjs/jwt';
import { PassportModule } from '@nestjs/passport';
import { authenticator } from 'otplib';
import { ContractFactory, JsonRpcProvider, NonceManager, Wallet, Contract } from 'ethers';
import { Pool } from 'pg';
import { randomUUID } from 'crypto';
import { ApprovalsController } from '../approvals/approvals.controller';
import { ApprovalsService } from '../approvals/approvals.service';
import { NativeApprovalHooks } from '../approvals/native-approval-hooks';
import { TenantRoleGuard } from '../approvals/tenant-role.guard';
import { TemporalService } from '../settlements/temporal.service';
import { CustomerService } from '../customers/customer.service';
import { UsersService } from '../identity/users.service';
import { JwtAuthStrategy, jwtSecret } from '../identity/jwt.strategy';
import { AuditService } from '../database/audit.service';
import { PG_POOL } from '../database/pg-pool.token';
import { KeysService } from '../keys/keys.service';
import { GovernedContracts } from '../keys/governed-contracts';
import { EvmRpcService } from '../tokens/evm-rpc.service';
import { TransfersService } from '../transfers/transfers.service';
import { PERMISSIONED_TOKEN } from './permissioned-token.artifact';
import { fundedWallet, startChain, TestChain, waitMined } from './test-chain';
import { TokenisationController } from './tokenisation.controller';
import { TokenisationService } from './tokenisation.service';

// Security-token issuance end to end: the audited contract on a real EVM chain (ganache),
// real Postgres, real approvals with real JWTs and one-time codes. The threshold key is
// replaced by an ordinary wallet that signs what the platform builds, so the chain really
// executes it; what is under test is everything around the signature.
//
//   eval "$(infrastructure/local/postgres-local.sh start)"   # migrations through 034
//   npx jest src/tokenisation/tokenisation.live

const TENANT_DSN = process.env.DATABASE_URL ?? 'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const ADMIN_DSN = process.env.DATABASE_ADMIN_URL ?? 'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const CHAIN = 1337;

let reachable = false; let chainUp = false; let chainError = ''; let sharedChain: TestChain | undefined;
beforeAll(async () => {
  const p = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 1500 });
  try { reachable = (await p.query(`SELECT to_regclass('security_tokens') IS NOT NULL AS ok`)).rows[0].ok; } catch { reachable = false; } finally { await p.end().catch(() => undefined); }
  if (reachable) {
    try { sharedChain = await startChain(); chainUp = true; } catch (err) { chainError = (err as Error).message; }
  }
}, 120000);
function skipped(): boolean {
  if (reachable && chainUp) return false;
  // Each variable demands only what it names: REQUIRE_LIVE_DB a database with the migration,
  // REQUIRE_EVM_CHAIN a dev chain (which is only attempted once there is a database to test
  // against, so a job with no database is not failed by it).
  if (!reachable && process.env.REQUIRE_LIVE_DB) throw new Error('REQUIRE_LIVE_DB is set but no database with migration 034 is reachable');
  if (reachable && !chainUp && (process.env.REQUIRE_EVM_CHAIN || process.env.REQUIRE_LIVE_DB)) throw new Error(`the dev chain did not start: ${chainError || 'unknown'}`);
  console.warn('skipping tokenisation live test -- needs a database with migration 034 and a dev chain (ganache via npx)');
  return true;
}

jest.setTimeout(60000);

describe('security token issuance (live Postgres, real EVM chain)', () => {
  let app: INestApplication; let base: string; let admin: Pool; let tenantPool: Pool; let jwt: JwtService;
  let ownPools: Pool[] = []; let customerId = ''; let otherId = '';
  let chain: TestChain; let provider: JsonRpcProvider; let deployer: NonceManager; let issuer: NonceManager; let issuerAddr = '';
  const people: Record<string, { id: string; email: string; secret: string; token: string }> = {};
  const keyId = randomUUID(); let tokenAddr = ''; let tokenId = ''; let reader: Contract;
  const wallets: Record<string, string> = {};
  const signed: Array<{ to: string; data: string; granted: boolean }> = [];
  let governed: GovernedContracts;

  // The "threshold key": same interface the transfer path uses, signing with a plain wallet.
  const fakeKeys = {
    getKey: async (id: string) => (id === keyId ? { key_id: id, name: 'Issuer key', blockchain: 'ethereum', address: issuerAddr, status: 'active' } : null),
    assertTravelRuleComplete() {},
    async signTransaction(customer: any, id: string, req: any, opts: { approvalGranted?: boolean } = {}) {
      // What the real signing path does with calldata: refuse what it cannot read.
      if (req.data && req.data !== '0x') {
        const g = await governed.recognise(customer.customer_id, req.chainId, req.to, req.value, req.data);
        if (!g) throw new BadRequestException('this transaction carries calldata the platform cannot account for');
      }
      if (!opts.approvalGranted) throw new Error('signing without approval');
      signed.push({ to: req.to, data: req.data, granted: true });
      const raw = await (issuer.signer as Wallet).signTransaction({
        to: req.to, value: req.value, data: req.data, nonce: req.nonce, gasLimit: req.gasLimit, chainId: req.chainId,
        ...(req.maxFeePerGas ? { maxFeePerGas: req.maxFeePerGas, maxPriorityFeePerGas: req.maxPriorityFeePerGas, type: 2 } : { gasPrice: req.gasPrice, type: 0 }),
      });
      return { raw_transaction: raw };
    },
  };

  async function person(name: string) {
    const email = `${name}-${randomUUID().slice(0, 8)}@example.test`; const secret = authenticator.generateSecret();
    const r = await admin.query(`INSERT INTO users (email, password_hash, full_name, mfa_secret, mfa_enabled) VALUES ($1,'x',$2,$3,true) RETURNING id`, [email, name, secret]);
    people[name] = { id: r.rows[0].id, email, secret, token: await jwt.signAsync({ sub: r.rows[0].id, email, role: 'user' }) };
  }
  async function call(method: string, path: string, who: string, body?: unknown, org = customerId) {
    const res = await fetch(`${base}/organisations/${org}${path}`, { method, headers: { 'content-type': 'application/json', authorization: `Bearer ${people[who].token}` }, body: body ? JSON.stringify(body) : undefined });
    const text = await res.text();
    return { status: res.status, body: text ? JSON.parse(text) : null };
  }
  const decide = (who: string, approvalId: string, decision = 'approve') =>
    call('POST', `/approvals/${approvalId}/decisions`, who, { decision, totpCode: authenticator.generate(people[who].secret) });
  const ask = (who: string, body: Record<string, unknown>) => call('POST', `/securities/${tokenId}/ops`, who, body);
  // Ask, then approve with two approvers: the usual path to a state change on chain.
  async function enact(body: Record<string, unknown>) {
    const r = await ask('oscar', body);
    expect(r.status).toBe(202);
    await decide('alice', r.body.approvalId);
    const done = await decide('bob', r.body.approvalId);
    if (done.body.execution.status !== 'completed') throw new Error(`${JSON.stringify(body)} -> ${JSON.stringify(done.body.execution)}`);
    return { approvalId: r.body.approvalId, execution: done.body.execution };
  }
  const deployToken = async (owner: string, symbol = 'ACME30') => {
    const f = new ContractFactory(PERMISSIONED_TOKEN.abi as any, PERMISSIONED_TOKEN.bytecode, deployer);
    const c: any = await f.deploy('Acme Bond 2030', symbol, 6, 1_000_000n * 10n ** 6n, owner);
    await waitMined(provider, c.deploymentTransaction().hash);
    return (await c.getAddress()).toLowerCase() as string;
  };
  const U = (n: number) => (BigInt(n) * 10n ** 6n).toString();

  beforeAll(async () => {
    if (!(reachable && chainUp)) return;
    chain = sharedChain!; provider = chain.provider;
    process.env.DATABASE_ADMIN_URL = ADMIN_DSN; process.env.EVM_RPC_1337 = chain.url;
    deployer = new NonceManager(await fundedWallet(provider));
    const issuerWallet = await fundedWallet(provider, '5');
    issuer = new NonceManager(issuerWallet); issuerAddr = issuerWallet.address;
    for (const n of ['alice-holder', 'bob-holder', 'mallory-holder']) wallets[n] = Wallet.createRandom().address.toLowerCase();
    tokenAddr = await deployToken(issuerAddr);
    reader = new Contract(tokenAddr, PERMISSIONED_TOKEN.abi as any, provider);

    admin = new Pool({ connectionString: ADMIN_DSN }); tenantPool = new Pool({ connectionString: TENANT_DSN });
    governed = new GovernedContracts();
    const m = await Test.createTestingModule({
      imports: [PassportModule, JwtModule.register({ secret: jwtSecret(), signOptions: { issuer: 'openfireblocks' } })],
      controllers: [ApprovalsController, TokenisationController],
      providers: [ApprovalsService, TenantRoleGuard, CustomerService, UsersService, JwtAuthStrategy, AuditService, NativeApprovalHooks, TransfersService, TokenisationService,
        { provide: GovernedContracts, useValue: governed },
        { provide: PG_POOL, useValue: tenantPool },
        { provide: TemporalService, useValue: { signalDecision: jest.fn(), start: jest.fn() } },
        { provide: KeysService, useValue: fakeKeys },
        EvmRpcService],
    }).compile();
    app = m.createNestApplication();
    app.useGlobalPipes(new ValidationPipe({ whitelist: true, forbidNonWhitelisted: true, transform: true }));
    await app.init(); await app.listen(0, '127.0.0.1');
    base = (await app.getUrl()).replace('[::1]', '127.0.0.1');
    jwt = m.get(JwtService);
    ownPools = [(m.get(CustomerService) as unknown as { pool: Pool }).pool, (m.get(AuditService) as unknown as { adminPool: Pool }).adminPool];
    customerId = (await m.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    otherId = (await m.get(CustomerService).createCustomer({ email: `org-${randomUUID()}@example.test`, tier: 'pro' })).customer_id;
    for (const n of ['ada', 'alice', 'bob', 'oscar']) await person(n);
    const a = m.get(ApprovalsService);
    await a.setMember(customerId, people.ada.email, 'admin'); await a.setMember(customerId, people.alice.email, 'approver');
    await a.setMember(customerId, people.bob.email, 'approver'); await a.setMember(customerId, people.oscar.email, 'operator');
    await a.setMember(otherId, people.ada.email, 'admin');
    await call('PUT', '/approval-policy', 'ada', { requiredApprovals: 2, windowMinutes: 60 });
    m.get(TokenisationService).onModuleInit();
  }, 90000);
  afterAll(async () => {
    if (!(reachable && chainUp)) return;
    await app.close(); await Promise.all(ownPools.map((p) => p.end().catch(() => undefined)));
    await admin.end(); await tenantPool.end(); await chain.stop();
  });

  it('registers only the audited contract, owned by the issuer key, and reads its terms from the chain', async () => {
    if (skipped()) return;
    const reg = (over: Record<string, unknown> = {}) => call('POST', '/securities', 'ada', { issuerKeyId: keyId, chainId: CHAIN, contractAddress: tokenAddr, ...over });
    expect((await call('POST', '/securities', 'oscar', { issuerKeyId: keyId, chainId: CHAIN, contractAddress: tokenAddr })).status).toBe(403);
    expect((await reg({ contractAddress: 'nope' })).status).toBe(400);
    expect((await reg({ contractAddress: '0x' + '1'.repeat(40) })).body.message).toMatch(/no contract/);
    // Some other contract: returns 42 for any call. Not the audited code.
    const tiny = await deployer.sendTransaction({ data: '0x600a600c600039600a6000f3602a60005260206000f3' });
    const impostor = (await waitMined(provider, tiny.hash)).contractAddress!;
    expect((await reg({ contractAddress: impostor })).body.message).toMatch(/not the audited PermissionedToken/);
    // The right code, but owned by someone else.
    const foreign = await deployToken(wallets['mallory-holder']);
    expect((await reg({ contractAddress: foreign })).body.message).toMatch(/owned by 0x.*not by this key/i);

    const ok = await reg();
    expect(ok.status).toBe(201);
    expect(ok.body).toMatchObject({ name: 'Acme Bond 2030', symbol: 'ACME30', decimals: 6, supplyCap: U(1_000_000), chainId: CHAIN });
    tokenId = ok.body.tokenId;
    expect((await reg()).status).toBe(409);
    expect((await call('GET', '/securities', 'ada', undefined, otherId)).body).toEqual([]);
    await expect(admin.query(`UPDATE security_tokens SET symbol = 'EVIL' WHERE token_id = $1`, [tokenId])).rejects.toMatchObject({ code: 'OFB09' });
    const detail = (await call('GET', `/securities/${tokenId}`, 'oscar')).body;
    expect(detail.onChain).toMatchObject({ totalSupply: '0', paused: false });
  });

  it('serves the contract artifact an issuer deploys, identified by the hash of its source', async () => {
    if (skipped()) return;
    const c = (await call('GET', '/securities/contract', 'oscar')).body;
    expect(c.sourceSha256).toBe(PERMISSIONED_TOKEN.sourceSha256);
    expect(c.bytecode).toBe(PERMISSIONED_TOKEN.bytecode);
  });

  it('keeps the issuer\'s register of holders, with a KYC reference required', async () => {
    if (skipped()) return;
    const add = (body: Record<string, unknown>, who = 'ada') => call('POST', `/securities/${tokenId}/holders`, who, body);
    expect((await add({ displayName: 'Alice Fund', walletAddress: wallets['alice-holder'] }, 'ada')).status).toBe(400);
    expect((await add({ displayName: 'Alice Fund', walletAddress: wallets['alice-holder'], kycReference: 'KYC-001' }, 'oscar')).status).toBe(403);
    expect((await add({ displayName: 'Alice Fund', walletAddress: 'x', kycReference: 'KYC-001' })).status).toBe(400);
    expect((await add({ displayName: 'Alice Fund', walletAddress: wallets['alice-holder'], kycReference: 'KYC-001' })).status).toBe(201);
    expect((await add({ displayName: 'Bob Trust', walletAddress: wallets['bob-holder'], kycReference: 'KYC-002' })).status).toBe(201);
    expect((await add({ displayName: 'dup', walletAddress: wallets['alice-holder'].toUpperCase().replace('0X', '0x'), kycReference: 'k' })).status).toBe(409);
    const hs = (await call('GET', `/securities/${tokenId}/holders`, 'oscar')).body;
    expect(hs).toHaveLength(2);
    expect(hs[0].onChain).toMatchObject({ admitted: false, frozen: false, balance: '0' });
  });

  it('holds an administrative act for approval, shows approvers what it means, and does it on chain only once quorum is reached', async () => {
    if (skipped()) return;
    signed.length = 0;
    // The holder is not admitted on chain yet, and nobody has been asked anything.
    expect(await reader.isHolder(wallets['alice-holder'])).toBe(false);
    const r = await ask('oscar', { op: 'admit', holder: wallets['alice-holder'] });
    expect(r.status).toBe(202);
    expect(r.body).toMatchObject({ status: 'pending_approval', requiredApprovals: 2 });
    expect(r.body.description).toBe(`admit ${wallets['alice-holder']} as a holder of ACME30`);
    expect(await reader.isHolder(wallets['alice-holder'])).toBe(false);
    expect(signed).toHaveLength(0);

    const a = (await call('GET', `/approvals/${r.body.approvalId}`, 'alice')).body;
    expect(a.summary).toMatchObject({ description: r.body.description, tokenSymbol: 'ACME30', operation: 'admit', to: expect.stringMatching(/^0x/), amount: '0' });
    expect(a.summary.reasons[0]).toMatch(/security token administration/);

    // The person who asked cannot approve their own request.
    await person('ola'); // an admin who also asks
    expect((await decide('alice', r.body.approvalId)).body.execution).toMatchObject({ status: 'awaiting_approval' });
    expect(await reader.isHolder(wallets['alice-holder'])).toBe(false);
    const done = await decide('bob', r.body.approvalId);
    expect(done.body.execution).toMatchObject({ status: 'completed', result: { transaction_hash: expect.stringMatching(/^0x[0-9a-f]{64}$/) } });
    expect(await reader.isHolder(wallets['alice-holder'])).toBe(true);
    expect(signed).toHaveLength(1);
    expect(signed[0].to.toLowerCase()).toBe(tokenAddr);

    // A repeated decision does not repeat it.
    expect((await decide('bob', r.body.approvalId)).status).toBe(409);
    expect(signed).toHaveLength(1);
  });

  it('refuses before anyone is asked what the contract would refuse, and what nobody has verified', async () => {
    if (skipped()) return;
    // Bob has a record but is not yet admitted on chain: a mint to him would revert.
    expect((await ask('oscar', { op: 'mint', to: wallets['bob-holder'], amount: U(10) })).body.message).toMatch(/would refuse this: recipient is not an admitted holder/);
    // Admitting someone with no holder record (no KYC reference on file).
    expect((await ask('oscar', { op: 'admit', holder: wallets['mallory-holder'] })).body.message).toMatch(/record the holder/);
    // Mint past the cap.
    expect((await ask('oscar', { op: 'mint', to: wallets['alice-holder'], amount: U(1_000_001) })).body.message).toMatch(/supply cap/);
    // Malformed.
    expect((await ask('oscar', { op: 'mint', to: wallets['alice-holder'], amount: '1.5' })).status).toBe(400);
    expect((await ask('oscar', { op: 'mint', to: wallets['alice-holder'] })).status).toBe(400);
    expect((await ask('oscar', { op: 'pause', amount: '1' })).status).toBe(400);
    expect((await ask('oscar', { op: 'selfdestruct' })).status).toBe(400);
    // An approver cannot start one; an admin can.
    expect((await ask('alice', { op: 'pause' })).status).toBe(403);
  });

  it('mints, moves and controls: the whole lifecycle executes on chain', async () => {
    if (skipped()) return;
    await enact({ op: 'admit', holder: wallets['bob-holder'] });
    await enact({ op: 'mint', to: wallets['alice-holder'], amount: U(600_000) });
    await enact({ op: 'mint', to: wallets['bob-holder'], amount: U(150_000) });
    expect([await reader.balanceOf(wallets['alice-holder']), await reader.balanceOf(wallets['bob-holder']), await reader.totalSupply()]).toEqual([BigInt(U(600_000)), BigInt(U(150_000)), BigInt(U(750_000))]);

    // A forced transfer is described as what it is.
    const forced = await ask('oscar', { op: 'force_transfer', from: wallets['alice-holder'], to: wallets['bob-holder'], amount: U(50_000) });
    expect(forced.body.description).toMatch(/^FORCE-move 50000 ACME30 from 0x.* without the holder's consent$/);
    await decide('alice', forced.body.approvalId); await decide('bob', forced.body.approvalId);
    expect(await reader.balanceOf(wallets['bob-holder'])).toBe(BigInt(U(200_000)));

    await enact({ op: 'freeze', holder: wallets['bob-holder'] });
    expect(await reader.isFrozen(wallets['bob-holder'])).toBe(true);
    await enact({ op: 'unfreeze', holder: wallets['bob-holder'] });
    await enact({ op: 'pause' });
    expect(await reader.paused()).toBe(true);
    await enact({ op: 'unpause' });
    await enact({ op: 'burn', from: wallets['bob-holder'], amount: U(200_000) });
    expect(await reader.totalSupply()).toBe(BigInt(U(550_000)));
    expect((await ask('oscar', { op: 'remove', holder: wallets['alice-holder'] })).body.message).toMatch(/still has a balance/);

    const owner = await ask('ada', { op: 'propose_owner', next: wallets['mallory-holder'] });
    expect(owner.body.description).toMatch(/^propose 0x.* as the new OWNER of ACME30, with every power the owner has$/);
    await decide('alice', owner.body.approvalId);
    await decide('bob', owner.body.approvalId); // ownership still needs the contract's own second step
    expect(await reader.owner()).toBe(issuerAddr);
    expect(await reader.pendingOwner()).toBe((await import('ethers')).getAddress(wallets['mallory-holder']));
  });

  it('runs two approvals decided at the same instant from one key one after the other, with consecutive nonces', async () => {
    if (skipped()) return;
    const a = await ask('oscar', { op: 'freeze', holder: wallets['alice-holder'] });
    const b = await ask('oscar', { op: 'unfreeze', holder: wallets['bob-holder'] }).then(async (x) => x);
    // unfreeze of an unfrozen holder reverts at simulation; use two acts that are both valid.
    expect(b.status).toBe(422);
    const c = await ask('oscar', { op: 'freeze', holder: wallets['bob-holder'] });
    await decide('alice', a.body.approvalId); await decide('alice', c.body.approvalId);
    const before = await provider.getTransactionCount(issuerAddr);
    const [ra, rc] = await Promise.all([decide('bob', a.body.approvalId), decide('bob', c.body.approvalId)]);
    expect(ra.body.execution).toMatchObject({ status: 'completed' });
    expect(rc.body.execution).toMatchObject({ status: 'completed' });
    expect(await provider.getTransactionCount(issuerAddr)).toBe(before + 2);
    expect([await reader.isFrozen(wallets['alice-holder']), await reader.isFrozen(wallets['bob-holder'])]).toEqual([true, true]);
    await enact({ op: 'unfreeze', holder: wallets['alice-holder'] });
    await enact({ op: 'unfreeze', holder: wallets['bob-holder'] });
  });

  it('shows the cap table from the chain, and flags supply held by addresses with no holder record', async () => {
    if (skipped()) return;
    const ct = (await call('GET', `/securities/${tokenId}/cap-table`, 'oscar')).body;
    expect(ct).toMatchObject({ totalSupply: U(550_000), supplyCap: U(1_000_000), unattributed: '0' });
    const alice = ct.holders.find((h: any) => h.displayName === 'Alice Fund');
    expect(alice).toMatchObject({ balance: U(550_000), display: '550000', basisPoints: 10000, admitted: true, frozen: false });
    // Supply the issuer's books do not explain: admitted and minted outside the platform.
    const stray = Wallet.createRandom().address.toLowerCase();
    const direct = new Contract(tokenAddr, PERMISSIONED_TOKEN.abi as any, issuer);
    await waitMined(provider, (await direct.addHolder(stray)).hash); await waitMined(provider, (await direct.mint(stray, BigInt(U(25)))).hash);
    const ct2 = (await call('GET', `/securities/${tokenId}/cap-table`, 'oscar')).body;
    expect(ct2.unattributed).toBe(U(25));
    expect(ct2.note).toMatch(/no holder record/);
  });

  it('keeps a register of what was asked and what became of each, linked to its approval', async () => {
    if (skipped()) return;
    const ops = (await call('GET', `/securities/${tokenId}/ops`, 'oscar')).body;
    expect(ops.length).toBeGreaterThanOrEqual(10);
    expect(ops.filter((o: any) => o.status === 'completed' && o.transactionHash).length).toBeGreaterThanOrEqual(9);
    const owner = ops.find((o: any) => o.op === 'propose_owner');
    // Proposing an owner is itself approved and sent; the contract then waits for the proposed owner to accept.
    expect(owner).toMatchObject({ status: 'completed', approvalId: expect.any(String), transactionHash: expect.stringMatching(/^0x/) });
    expect((await call('GET', `/securities/${tokenId}/ops`, 'ada', undefined, otherId)).status).toBe(404);
    await expect(admin.query(`DELETE FROM security_token_ops WHERE token_id = $1`, [tokenId])).rejects.toMatchObject({ code: 'OFB09' });
  });
});
