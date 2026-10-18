import { BadRequestException, ConflictException, Inject, Injectable, NotFoundException, OnModuleInit, ServiceUnavailableException, UnprocessableEntityException } from '@nestjs/common';
import { Contract, getAddress, isAddress } from 'ethers';
import { Pool } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { UUID_RE, withTenant } from '../approvals/tenant-db';
import { ApprovalsService } from '../approvals/approvals.service';
import { CustomerService } from '../customers/customer.service';
import { KeysService } from '../keys/keys.service';
import { GovernedCall, GovernedContractRecogniser, GovernedContracts } from '../keys/governed-contracts';
import { EvmRpcService } from '../tokens/evm-rpc.service';
import { Initiator, SubmitResult, TransfersService } from '../transfers/transfers.service';
import { PERMISSIONED_TOKEN } from './permissioned-token.artifact';
import { buildCall, describe, isAuditedCode, recogniseCall, TokenCallError, TokenOp, OPS, units } from './token-calls';

const abi = PERMISSIONED_TOKEN.abi as any;

// Issuing and administering permissioned (security) tokens from an organisation's threshold
// key.
//
// The contract is owned by that key, so every administrative act -- admitting a holder,
// minting, freezing, forcing a transfer -- is a transaction the key signs, and each is held
// for the organisation's approvers whatever the spending policy says: none of them moves
// value, which is exactly why a value limit would wave them through. The calls are
// simulated against the chain before anyone is asked, so approvers are never asked to
// approve something the contract will refuse.
//
// What this is not: a legal wrapper. Whether a token is a security, who may hold it, and
// what the holder's rights are is the issuer's counsel's question; the contract enforces
// who may transfer, and this module records who the issuer says the holders are.
@Injectable()
export class TokenisationService implements GovernedContractRecogniser, OnModuleInit {
  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    private readonly customers: CustomerService,
    private readonly keys: KeysService,
    private readonly rpc: EvmRpcService,
    private readonly transfers: TransfersService,
    private readonly approvals: ApprovalsService,
    private readonly governed: GovernedContracts,
  ) {}

  onModuleInit() { this.governed.register(this); }

  // ---- the signing path asks whether it may read a call --------------------------------

  async recognise(customerId: string, chainId: number, to: string, value: string, data: string): Promise<GovernedCall | null> {
    if (value !== '0') return null;
    const call = recogniseCall(data);
    if (!call) return null;
    const r = await withTenant(this.pool, customerId, (c) => c.query(
      `SELECT symbol, decimals FROM security_tokens WHERE customer_id = $1 AND chain_id = $2 AND contract_address = $3`, [customerId, chainId, to.toLowerCase()]));
    if (!r.rows[0]) return null;
    return { description: describe(call.op, call.params, r.rows[0].symbol, r.rows[0].decimals) };
  }

  // ---- the contract an issuer deploys --------------------------------------------------

  contract() {
    return {
      name: 'PermissionedToken',
      sourceSha256: PERMISSIONED_TOKEN.sourceSha256, compiler: PERMISSIONED_TOKEN.compiler,
      constructor: ['string name', 'string symbol', 'uint8 decimals', 'uint256 supplyCap', 'address owner'],
      note: 'Deploy with any wallet, setting owner to the address of the organisation\'s threshold key, then register the deployed address. The platform verifies the code on chain against this artifact before accepting it.',
      abi: PERMISSIONED_TOKEN.abi, bytecode: PERMISSIONED_TOKEN.bytecode,
    };
  }

  // ---- tokens --------------------------------------------------------------------------

  async registerToken(customerId: string, userId: string, input: { issuerKeyId: string; chainId: number; contractAddress: string }) {
    if (!isAddress(input.contractAddress ?? '')) throw new BadRequestException('contractAddress must be a 20-byte hex address');
    const address = input.contractAddress.toLowerCase();
    const key = await this.keys.getKey(input.issuerKeyId, customerId);
    if (!key) throw new NotFoundException(`no key ${input.issuerKeyId}`);
    if (!['ethereum', 'polygon'].includes(key.blockchain) || !key.address) throw new BadRequestException('the issuer key must be an EVM key with an address');
    if (key.status !== 'active') throw new ConflictException(`key ${input.issuerKeyId} is ${key.status}, not active`);
    if (!this.rpc.configured(input.chainId)) throw new ServiceUnavailableException(`no JSON-RPC endpoint is configured for chain ${input.chainId}`);
    const provider = this.rpc.provider(input.chainId);

    const code = await provider.getCode(address);
    if (code === '0x') throw new UnprocessableEntityException('there is no contract at that address on that chain');
    if (!isAuditedCode(code)) throw new UnprocessableEntityException('the code at that address is not the audited PermissionedToken (compare GET .../securities/contract); refusing to govern a contract this platform has not read');

    const c = new Contract(address, abi, provider);
    let owner: string; let name: string; let symbol: string; let decimals: bigint; let cap: bigint;
    try {
      [owner, name, symbol, decimals, cap] = await Promise.all([c.owner(), c.name(), c.symbol(), c.decimals(), c.supplyCap()]);
    } catch (err) { throw new UnprocessableEntityException(`could not read the contract: ${(err as Error).message}`); }
    if (owner.toLowerCase() !== key.address.toLowerCase()) {
      throw new UnprocessableEntityException(`the contract is owned by ${owner}, not by this key (${key.address}): nothing this platform signs could administer it`);
    }
    try {
      const r = await withTenant(this.pool, customerId, (cl) => cl.query(
        `INSERT INTO security_tokens (customer_id, chain_id, contract_address, name, symbol, decimals, supply_cap, issuer_key_id, created_by)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *`,
        [customerId, input.chainId, address, name, symbol, Number(decimals), cap.toString(), input.issuerKeyId, userId]));
      return tokenView(r.rows[0]);
    } catch (err) {
      if ((err as { code?: string }).code === '23505') throw new ConflictException('this token is already registered');
      throw err;
    }
  }

  async listTokens(customerId: string) {
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM security_tokens WHERE customer_id = $1 ORDER BY created_at DESC`, [customerId]));
    return r.rows.map(tokenView);
  }

  async getToken(customerId: string, tokenId: string) {
    const t = await this.token(customerId, tokenId);
    const c = this.contractFor(t);
    const [supply, paused, owner] = await Promise.all([c.totalSupply(), c.paused(), c.owner()]);
    return { ...tokenView(t), onChain: { totalSupply: supply.toString(), paused, owner, ownerIsIssuerKey: true } };
  }

  // ---- holders -------------------------------------------------------------------------

  async addHolder(customerId: string, userId: string, tokenId: string, input: { displayName: string; walletAddress: string; kycReference: string }) {
    await this.token(customerId, tokenId);
    if (!isAddress(input.walletAddress ?? '')) throw new BadRequestException('walletAddress must be a 20-byte hex address');
    if (!input.displayName?.trim()) throw new BadRequestException('displayName is required');
    if (!input.kycReference?.trim()) throw new BadRequestException('kycReference is required: where the issuer\'s own verification of this holder is recorded');
    try {
      const r = await withTenant(this.pool, customerId, (c) => c.query(
        `INSERT INTO security_token_holders (token_id, customer_id, display_name, wallet_address, kyc_reference, created_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING *`,
        [tokenId, customerId, input.displayName.trim(), input.walletAddress.toLowerCase(), input.kycReference.trim(), userId]));
      return holderView(r.rows[0]);
    } catch (err) {
      if ((err as { code?: string }).code === '23505') throw new ConflictException('that wallet is already a holder record on this token');
      throw err;
    }
  }

  // Holders as the issuer's books say, with what the chain says about each.
  async listHolders(customerId: string, tokenId: string) {
    const t = await this.token(customerId, tokenId);
    const c = this.contractFor(t);
    const rows = (await withTenant(this.pool, customerId, (cl) => cl.query(`SELECT * FROM security_token_holders WHERE token_id = $1 ORDER BY created_at`, [tokenId]))).rows;
    return Promise.all(rows.map(async (h) => {
      try {
        const [admitted, frozen, balance] = await Promise.all([c.isHolder(h.wallet_address), c.isFrozen(h.wallet_address), c.balanceOf(h.wallet_address)]);
        return { ...holderView(h), onChain: { admitted, frozen, balance: balance.toString() } };
      } catch (err) { return { ...holderView(h), onChain: { error: (err as Error).message } }; }
    }));
  }

  // The register of holders. Balances are read from the contract, not kept here; anything
  // the contract says is held by an address with no holder record shows as unattributed.
  async capTable(customerId: string, tokenId: string) {
    const t = await this.token(customerId, tokenId);
    const holders = await this.listHolders(customerId, tokenId);
    const c = this.contractFor(t);
    const supply: bigint = await c.totalSupply();
    let attributed = 0n;
    const rows = holders.map((h: any) => {
      const bal = h.onChain?.balance ? BigInt(h.onChain.balance) : 0n;
      attributed += bal;
      return {
        holderId: h.holderId, displayName: h.displayName, wallet: h.walletAddress, kycReference: h.kycReference,
        admitted: h.onChain?.admitted ?? null, frozen: h.onChain?.frozen ?? null,
        balance: bal.toString(), display: units(bal.toString(), t.decimals),
        // percentage of supply to two decimals, as an integer in basis points
        basisPoints: supply === 0n ? 0 : Number((bal * 10000n) / supply),
        ...(h.onChain?.error ? { error: h.onChain.error } : {}),
      };
    }).filter((r) => r.balance !== '0' || r.admitted);
    return {
      token: { symbol: t.symbol, decimals: t.decimals, contract: t.contract_address },
      totalSupply: supply.toString(), supplyCap: String(t.supply_cap), holders: rows,
      unattributed: (supply - attributed).toString(),
      note: (supply - attributed) === 0n ? undefined : 'Some supply is held by addresses with no holder record here. The contract only lets admitted holders hold tokens, so these were admitted outside this platform or are missing from the issuer\'s books.',
    };
  }

  // ---- administrative acts ---------------------------------------------------------------

  async requestOp(customerId: string, initiator: Initiator, tokenId: string, op: TokenOp, raw: Record<string, unknown>): Promise<SubmitResult & { opId: string; description: string }> {
    if (!(op in OPS)) throw new BadRequestException(`unknown operation ${JSON.stringify(op)}`);
    const t = await this.token(customerId, tokenId);
    let call;
    try { call = buildCall(op, raw); } catch (err) { if (err instanceof TokenCallError) throw new BadRequestException(err.message); throw err; }
    const { data, params } = call;

    // A holder nobody has verified is not admitted.
    if (op === 'admit') {
      const h = (await withTenant(this.pool, customerId, (c) => c.query(`SELECT 1 FROM security_token_holders WHERE token_id = $1 AND wallet_address = $2`, [tokenId, params.holder]))).rows[0];
      if (!h) throw new UnprocessableEntityException('record the holder (name and KYC reference) before admitting them');
    }

    // Would the contract do it? Asked of the chain, as the owner, before anyone is asked.
    const key = await this.keys.getKey(t.issuer_key_id, customerId);
    if (!key?.address) throw new ConflictException('the issuer key has no address');
    const provider = this.rpc.provider(t.chain_id);
    try {
      await provider.call({ from: key.address, to: t.contract_address, data });
    } catch (err) {
      const e = err as { reason?: string; shortMessage?: string; message: string };
      throw new UnprocessableEntityException(`the contract would refuse this: ${e.reason ?? e.shortMessage ?? e.message}`);
    }

    const description = describe(op, params, t.symbol, t.decimals);
    const customer = await this.customers.getByCustomerId(customerId);
    const out = await this.transfers.submit(customer, t.issuer_key_id, 'evm',
      { chainId: t.chain_id, destination: t.contract_address, amount: '0', data },
      initiator,
      { requireApproval: [`security token administration: ${description}`], summaryExtra: { description, tokenSymbol: t.symbol, operation: op } });
    if (out.status !== 'pending_approval') throw new ServiceUnavailableException('the administrative act was not held for approval; refusing to record it as asked');
    const r = await withTenant(this.pool, customerId, (c) => c.query(
      `INSERT INTO security_token_ops (token_id, customer_id, op, params, description, approval_id, requested_by) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING op_id`,
      [tokenId, customerId, op, JSON.stringify(params), description, out.approvalId, initiator.userId]));
    return { ...out, opId: r.rows[0].op_id, description };
  }

  async listOps(customerId: string, tokenId: string) {
    await this.token(customerId, tokenId);
    const rows = (await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM security_token_ops WHERE token_id = $1 ORDER BY created_at DESC LIMIT 200`, [tokenId]))).rows;
    return Promise.all(rows.map(async (o) => {
      const p = await this.approvals.pendingTransfer(customerId, o.approval_id);
      return { opId: o.op_id, op: o.op, params: o.params, description: o.description, approvalId: o.approval_id, requestedAt: new Date(o.created_at).toISOString(),
        status: p?.status ?? 'unknown', transactionHash: (p?.result as { transaction_hash?: string } | null)?.transaction_hash ?? null, error: p?.error ?? null };
    }));
  }

  // ---- internals -------------------------------------------------------------------------

  private async token(customerId: string, tokenId: string) {
    if (!UUID_RE.test(tokenId)) throw new NotFoundException('token not found');
    const r = await withTenant(this.pool, customerId, (c) => c.query(`SELECT * FROM security_tokens WHERE token_id = $1 AND customer_id = $2`, [tokenId, customerId]));
    if (!r.rows[0]) throw new NotFoundException('token not found');
    return r.rows[0];
  }

  private contractFor(t: Record<string, any>) {
    if (!this.rpc.configured(t.chain_id)) throw new ServiceUnavailableException(`no JSON-RPC endpoint is configured for chain ${t.chain_id}`);
    return new Contract(t.contract_address, abi, this.rpc.provider(t.chain_id));
  }
}

function tokenView(r: Record<string, any>) {
  return { tokenId: r.token_id, chainId: r.chain_id, contractAddress: getAddress(r.contract_address), name: r.name, symbol: r.symbol, decimals: r.decimals,
    supplyCap: String(r.supply_cap), issuerKeyId: r.issuer_key_id, createdAt: new Date(r.created_at).toISOString() };
}
function holderView(r: Record<string, any>) {
  return { holderId: r.holder_id, displayName: r.display_name, walletAddress: r.wallet_address, kycReference: r.kyc_reference, createdAt: new Date(r.created_at).toISOString() };
}
