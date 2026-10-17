import {
  BadRequestException, ConflictException, Inject, Injectable, Logger, NotFoundException,
} from '@nestjs/common';
import { Pool, PoolClient } from 'pg';
import { PG_POOL } from '../database/pg-pool.token';
import { UUID_RE, withTenant } from '../approvals/tenant-db';
import { Customer } from '../customers/customer.service';
import { KeysService } from '../keys/keys.service';
import { VALID_BLOCKCHAINS, validateCreateKey } from '../keys/create-key.validation';
import { kindOfBlockchain, parseTransfer } from '../transfers/parse-transfer';
import { Initiator, TransfersService } from '../transfers/transfers.service';

// Per-user wallets: wallet-as-a-service for a customer's own end users.
//
// A wallet is a set of threshold keys, one per chain, labelled with the
// customer's own reference for its user. Each key is created through
// KeysService.createKey, so it is an ordinary threshold key with its own key
// ceremony, and every transfer from it goes through TransfersService: the
// organisation's freeze, whitelist, spending policy, approvals and Travel
// Rule apply to a user's transfer exactly as to any other. What a wallet adds
// is the grouping, the reference, and a per-user freeze.

export const EXTERNAL_REF = /^[A-Za-z0-9._:-]{1,128}$/;
const MAX_CHAINS = VALID_BLOCKCHAINS.length;

export interface WalletKey {
  blockchain: string;
  key_id: string;
  status: string;
  address: string | null;
}

export interface Wallet {
  wallet_id: string;
  external_ref: string;
  label: string | null;
  status: 'active' | 'frozen';
  status_reason: string | null;
  status_changed_at: Date | null;
  created_at: Date;
  keys: WalletKey[];
}

export interface CreateWalletInput {
  externalRef: string;
  label?: string;
  blockchains: string[];
  threshold?: number;
  totalParties?: number;
}

@Injectable()
export class WalletsService {
  private readonly logger = new Logger(WalletsService.name);

  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    private readonly keys: KeysService,
    private readonly transfers: TransfersService,
  ) {}

  // Idempotent on externalRef: asking again returns the same wallet, and
  // creates keys only for chains it does not have yet. So a caller that timed
  // out can simply retry, and adding a chain later is the same call.
  async create(customer: Customer, input: CreateWalletInput): Promise<Wallet> {
    if (!EXTERNAL_REF.test(input.externalRef)) {
      throw new BadRequestException('externalRef must be 1-128 characters of A-Z a-z 0-9 . _ : -');
    }
    const chains = [...new Set(input.blockchains)];
    if (chains.length === 0 || chains.length > MAX_CHAINS) {
      throw new BadRequestException(`blockchains must name 1 to ${MAX_CHAINS} chains`);
    }
    const unknown = chains.filter((c) => !VALID_BLOCKCHAINS.includes(c));
    if (unknown.length) {
      throw new BadRequestException(`unsupported blockchain(s): ${unknown.join(', ')}. Supported: ${VALID_BLOCKCHAINS.join(', ')}`);
    }

    const walletId = await withTenant(this.pool, customer.customer_id, async (c) => {
      await c.query(
        `INSERT INTO user_wallets (customer_id, external_ref, label) VALUES ($1, $2, $3)
         ON CONFLICT (customer_id, external_ref) DO NOTHING`,
        [customer.customer_id, input.externalRef, input.label ?? null],
      );
      const r = await c.query<{ wallet_id: string; status: string }>(
        `SELECT wallet_id, status FROM user_wallets WHERE customer_id = $1 AND external_ref = $2`,
        [customer.customer_id, input.externalRef],
      );
      return r.rows[0].wallet_id;
    });

    const existing = await this.get(customer.customer_id, walletId);
    if (existing.status === 'frozen') {
      throw new ConflictException(`wallet ${walletId} is frozen; no keys are added to a frozen wallet`);
    }
    const have = new Set(existing.keys.map((k) => k.blockchain));

    for (const blockchain of chains.filter((c) => !have.has(c))) {
      // The key name carries the reference so the key list stays readable,
      // and the (customer, name) uniqueness of keys stops two concurrent
      // creates making two keys for one chain.
      const name = `wallet:${input.externalRef}:${blockchain}`;
      const req = { name, blockchain, threshold: input.threshold ?? 2, total_parties: input.totalParties ?? 3 };
      validateCreateKey(req as never);
      let keyId: string;
      try {
        const key = await this.keys.createKey(customer, req as never);
        keyId = key.id;
      } catch (err) {
        if (!(err instanceof ConflictException)) throw err;
        const found = await this.keyByName(customer.customer_id, name);
        if (!found) throw err;
        keyId = found;
      }
      await withTenant(this.pool, customer.customer_id, (c) =>
        c.query(
          `INSERT INTO user_wallet_keys (wallet_id, customer_id, blockchain, key_id) VALUES ($1, $2, $3, $4)
           ON CONFLICT DO NOTHING`,
          [walletId, customer.customer_id, blockchain, keyId],
        ),
      );
    }
    return this.get(customer.customer_id, walletId);
  }

  async get(customerId: string, walletId: string): Promise<Wallet> {
    if (!UUID_RE.test(walletId)) throw new NotFoundException(`no wallet ${walletId}`);
    const wallet = await withTenant(this.pool, customerId, (c) => this.load(c, 'w.wallet_id = $2', [customerId, walletId]));
    if (!wallet[0]) throw new NotFoundException(`no wallet ${walletId}`);
    return wallet[0];
  }

  async byRef(customerId: string, externalRef: string): Promise<Wallet> {
    if (!EXTERNAL_REF.test(externalRef)) throw new NotFoundException(`no wallet for ${externalRef}`);
    const wallet = await withTenant(this.pool, customerId, (c) => this.load(c, 'w.external_ref = $2', [customerId, externalRef]));
    if (!wallet[0]) throw new NotFoundException(`no wallet for ${externalRef}`);
    return wallet[0];
  }

  // Newest first, paged by created_at.
  async list(customerId: string, opts: { limit?: number; before?: string } = {}): Promise<{ wallets: Wallet[]; next: string | null }> {
    const limit = Math.min(Math.max(opts.limit ?? 50, 1), 200);
    const before = opts.before ? new Date(opts.before) : null;
    if (before && Number.isNaN(before.getTime())) throw new BadRequestException('before must be an ISO timestamp');
    const wallets = await withTenant(this.pool, customerId, (c) =>
      this.load(c, before ? 'w.created_at < $2' : 'TRUE', before ? [customerId, before] : [customerId], limit + 1),
    );
    const page = wallets.slice(0, limit);
    return { wallets: page, next: wallets.length > limit ? page[page.length - 1].created_at.toISOString() : null };
  }

  async setStatus(customerId: string, walletId: string, status: 'active' | 'frozen', reason: string): Promise<Wallet> {
    if (!reason || reason.length < 3 || reason.length > 500) {
      throw new BadRequestException('a reason of 3-500 characters is required');
    }
    await this.get(customerId, walletId);
    await withTenant(this.pool, customerId, (c) =>
      c.query(
        `UPDATE user_wallets SET status = $2, status_reason = $3, status_changed_at = now() WHERE wallet_id = $1`,
        [walletId, status, reason],
      ),
    );
    this.logger.log(`wallet ${walletId} of ${customerId} set ${status}: ${reason}`);
    return this.get(customerId, walletId);
  }

  async addresses(customer: Customer, walletId: string) {
    const wallet = await this.get(customer.customer_id, walletId);
    const out = await Promise.all(wallet.keys.map(async (k) => {
      if (k.status !== 'active') return { blockchain: k.blockchain, key_id: k.key_id, status: k.status, addresses: null };
      const a = await this.keys.getDepositAddresses(customer, k.key_id);
      return { blockchain: k.blockchain, key_id: k.key_id, status: k.status, addresses: (a as { addresses?: unknown }).addresses ?? a };
    }));
    return { wallet_id: walletId, external_ref: wallet.external_ref, chains: out };
  }

  // EVM chains need a chain id (one key, many networks); the others do not.
  async balances(customer: Customer, walletId: string, chainId?: number) {
    const wallet = await this.get(customer.customer_id, walletId);
    const out = await Promise.all(wallet.keys.map(async (k) => {
      if (k.status !== 'active') return { blockchain: k.blockchain, key_id: k.key_id, status: k.status };
      const evm = k.blockchain === 'ethereum' || k.blockchain === 'polygon';
      if (evm && chainId === undefined) return { blockchain: k.blockchain, key_id: k.key_id, error: 'pass chainId to read EVM balances' };
      try {
        return { blockchain: k.blockchain, ...(await this.keys.getBalancesForKey(customer, k.key_id, evm ? chainId : undefined)) };
      } catch (err) {
        return { blockchain: k.blockchain, key_id: k.key_id, error: (err as Error).message };
      }
    }));
    return { wallet_id: walletId, external_ref: wallet.external_ref, chains: out };
  }

  // A transfer from the user's key on one chain. Exactly the organisation's
  // transfer path: it may complete, or wait for approval.
  async transfer(customer: Customer, walletId: string, body: Record<string, unknown>, initiator: Initiator) {
    const { blockchain, ...transfer } = body;
    if (typeof blockchain !== 'string') throw new BadRequestException('blockchain is required');
    const wallet = await this.get(customer.customer_id, walletId);
    if (wallet.status === 'frozen') {
      throw new ConflictException(`wallet ${walletId} is frozen${wallet.status_reason ? `: ${wallet.status_reason}` : ''}`);
    }
    const key = wallet.keys.find((k) => k.blockchain === blockchain);
    if (!key) throw new NotFoundException(`wallet ${walletId} has no ${blockchain} key; create it by calling POST /wallets again with that chain`);
    const kind = kindOfBlockchain(blockchain);
    const dto = await parseTransfer(kind, transfer);
    return this.transfers.submit(customer, key.key_id, kind, dto, initiator);
  }

  private async load(c: PoolClient, where: string, params: unknown[], limit = 1): Promise<Wallet[]> {
    const r = await c.query<Omit<Wallet, 'keys'> & { keys: WalletKey[] | null }>(
      `SELECT w.wallet_id, w.external_ref, w.label, w.status, w.status_reason, w.status_changed_at, w.created_at,
              (SELECT json_agg(json_build_object('blockchain', l.blockchain, 'key_id', l.key_id,
                                                 'status', k.status, 'address', k.address) ORDER BY l.blockchain)
                 FROM user_wallet_keys l JOIN key_pairs k ON k.key_id = l.key_id
                WHERE l.wallet_id = w.wallet_id) AS keys
         FROM user_wallets w
        WHERE w.customer_id = $1 AND ${where}
        ORDER BY w.created_at DESC
        LIMIT ${Number(limit)}`,
      params,
    );
    return r.rows.map((row) => ({ ...row, keys: row.keys ?? [] }));
  }

  private async keyByName(customerId: string, name: string): Promise<string | null> {
    const r = await withTenant(this.pool, customerId, (c) =>
      c.query<{ key_id: string }>(
        `SELECT key_id FROM key_pairs WHERE customer_id = $1 AND name = $2 AND status <> 'failed'`,
        [customerId, name],
      ),
    );
    return r.rows[0]?.key_id ?? null;
  }
}
