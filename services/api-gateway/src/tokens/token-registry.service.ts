import { Inject, Injectable, Logger, BadRequestException, NotFoundException } from '@nestjs/common';
import { Pool } from 'pg';
import { ethers } from 'ethers';
import { PG_POOL } from '../database/database.module';
import { EvmRpcService } from './evm-rpc.service';
import { SELECTOR_DECIMALS, SELECTOR_SYMBOL } from '../keys/erc20';

// Which contracts the platform will move money through, and on whose say-so.
//
// The registry is what stops "send 10,000 USDC" from being ambiguous. Left
// to name a contract per request, a customer -- or anyone who has got hold
// of their API key -- can point the word USDC at a contract they deployed
// this morning. The platform would sign it, the policy engine would
// evaluate a transfer to a whitelisted recipient, and the tokens would be
// whatever that contract says they are.
//
// So: contracts are registered once, by an operator, and verified against
// the chain before they can be used.

export type TokenStatus = 'awaiting_address' | 'unverified' | 'verified' | 'suspended';

export interface Token {
  tokenId: string;
  chainId: number;
  contractAddress: string | null;
  symbol: string;
  name: string;
  decimals: number;
  pegCurrency: string | null;
  issuer: string | null;
  status: TokenStatus;
  verifiedSymbol: string | null;
  verifiedDecimals: number | null;
  verifiedAt: Date | null;
  verificationError: string | null;
  notes: string | null;
}

interface TokenRow {
  token_id: string;
  chain_id: number;
  contract_address: string | null;
  symbol: string;
  name: string;
  decimals: number;
  peg_currency: string | null;
  issuer: string | null;
  status: TokenStatus;
  verified_symbol: string | null;
  verified_decimals: number | null;
  verified_at: Date | null;
  verification_error: string | null;
  notes: string | null;
}

function toToken(row: TokenRow): Token {
  return {
    tokenId: row.token_id,
    chainId: row.chain_id,
    contractAddress: row.contract_address,
    symbol: row.symbol,
    name: row.name,
    decimals: Number(row.decimals),
    pegCurrency: row.peg_currency ? row.peg_currency.trim() : null,
    issuer: row.issuer,
    status: row.status,
    verifiedSymbol: row.verified_symbol,
    verifiedDecimals:
      row.verified_decimals === null ? null : Number(row.verified_decimals),
    verifiedAt: row.verified_at,
    verificationError: row.verification_error,
    notes: row.notes,
  };
}

const COLUMNS = `token_id, chain_id, contract_address, symbol, name, decimals,
                 peg_currency, issuer, status, verified_symbol, verified_decimals,
                 verified_at, verification_error, notes`;

@Injectable()
export class TokenRegistryService {
  private readonly logger = new Logger(TokenRegistryService.name);

  constructor(
    @Inject(PG_POOL) private readonly pool: Pool,
    private readonly rpc: EvmRpcService,
  ) {}

  // No withTenant: this is platform reference data, not customer rows, and
  // it is granted SELECT to the app role precisely so every tenant reads
  // the same answer about what USDC is.
  async list(chainId?: number): Promise<Token[]> {
    const res = chainId
      ? await this.pool.query<TokenRow>(
          `SELECT ${COLUMNS} FROM tokens WHERE chain_id = $1 ORDER BY symbol`,
          [chainId],
        )
      : await this.pool.query<TokenRow>(
          `SELECT ${COLUMNS} FROM tokens ORDER BY chain_id, symbol`,
        );
    return res.rows.map(toToken);
  }

  async byId(tokenId: string): Promise<Token | null> {
    const res = await this.pool.query<TokenRow>(
      `SELECT ${COLUMNS} FROM tokens WHERE token_id = $1`,
      [tokenId],
    );
    return res.rows[0] ? toToken(res.rows[0]) : null;
  }

  // Lowercased on both sides. EIP-55 gives every address two spellings and
  // a case-sensitive comparison is how a registered token fails to match
  // itself when the caller happens to checksum it.
  async byContract(chainId: number, contractAddress: string): Promise<Token | null> {
    const res = await this.pool.query<TokenRow>(
      `SELECT ${COLUMNS} FROM tokens WHERE chain_id = $1 AND lower(contract_address) = lower($2)`,
      [chainId, contractAddress],
    );
    return res.rows[0] ? toToken(res.rows[0]) : null;
  }

  async bySymbol(chainId: number, symbol: string): Promise<Token | null> {
    const res = await this.pool.query<TokenRow>(
      `SELECT ${COLUMNS} FROM tokens WHERE chain_id = $1 AND upper(symbol) = upper($2)`,
      [chainId, symbol],
    );
    return res.rows[0] ? toToken(res.rows[0]) : null;
  }

  // The gate. Everything that can move money goes through here.
  //
  // Each refusal says what an operator has to do next, because the states
  // are genuinely different: an unverified token needs a verification run,
  // one awaiting an address needs somebody to go and get it from the
  // issuer, and a suspended one was withdrawn deliberately and should not
  // be quietly re-enabled by a retry.
  async requireTransactable(chainId: number, symbolOrAddress: string): Promise<Token> {
    const token = symbolOrAddress.startsWith('0x')
      ? await this.byContract(chainId, symbolOrAddress)
      : await this.bySymbol(chainId, symbolOrAddress);

    if (!token) {
      throw new NotFoundException(
        `no token ${symbolOrAddress} is registered on chain ${chainId}. ` +
          'Tokens are registered by an operator so that a symbol means one ' +
          'contract for every tenant, rather than whichever contract a caller names.',
      );
    }
    switch (token.status) {
      case 'verified':
        return token;
      case 'awaiting_address':
        throw new BadRequestException(
          `${token.symbol} on chain ${chainId} is registered but has no confirmed contract ` +
            `address yet. Obtain it from the issuer${token.issuer ? ` (${token.issuer})` : ''}, ` +
            'record it, and run verification.',
        );
      case 'unverified':
        throw new BadRequestException(
          `${token.symbol} on chain ${chainId} has never been verified against the chain. ` +
            'Run verification so the contract\'s own symbol() and decimals() are checked ' +
            'against the registry before anything is signed against it.',
        );
      case 'suspended':
        throw new BadRequestException(
          `${token.symbol} on chain ${chainId} is suspended and cannot be transacted.`,
        );
    }
  }

  async register(input: {
    chainId: number;
    contractAddress?: string;
    symbol: string;
    name: string;
    decimals: number;
    pegCurrency?: string;
    issuer?: string;
    notes?: string;
  }): Promise<Token> {
    let address: string | null = null;
    if (input.contractAddress) {
      try {
        // Checksum-validate, then store lowercase.
        address = ethers.getAddress(input.contractAddress).toLowerCase();
      } catch {
        throw new BadRequestException(
          `${input.contractAddress} is not a valid contract address`,
        );
      }
    }

    const res = await this.pool.query<TokenRow>(
      `INSERT INTO tokens (chain_id, contract_address, symbol, name, decimals,
                           peg_currency, issuer, status, notes)
       VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
       RETURNING ${COLUMNS}`,
      [
        input.chainId,
        address,
        input.symbol,
        input.name,
        input.decimals,
        input.pegCurrency ?? null,
        input.issuer ?? null,
        // Never 'verified' on registration, whatever the caller wants.
        // Verified means a node agreed, and no node has been asked yet.
        address ? 'unverified' : 'awaiting_address',
        input.notes ?? null,
      ],
    );
    return toToken(res.rows[0]);
  }

  // Records a contract address against a token that was registered without
  // one. Returns it to 'unverified', never straight to usable.
  async setAddress(tokenId: string, contractAddress: string): Promise<Token> {
    let address: string;
    try {
      address = ethers.getAddress(contractAddress).toLowerCase();
    } catch {
      throw new BadRequestException(`${contractAddress} is not a valid contract address`);
    }
    const res = await this.pool.query<TokenRow>(
      `UPDATE tokens
          SET contract_address = $2,
              status = 'unverified',
              verified_symbol = NULL, verified_decimals = NULL,
              verified_at = NULL, verification_error = NULL,
              updated_at = NOW()
        WHERE token_id = $1
        RETURNING ${COLUMNS}`,
      [tokenId, address],
    );
    if (!res.rows[0]) {
      throw new NotFoundException(`no token ${tokenId}`);
    }
    return toToken(res.rows[0]);
  }

  async suspend(tokenId: string, reason: string): Promise<Token> {
    const res = await this.pool.query<TokenRow>(
      `UPDATE tokens SET status = 'suspended', notes = $2, updated_at = NOW()
        WHERE token_id = $1 RETURNING ${COLUMNS}`,
      [tokenId, reason],
    );
    if (!res.rows[0]) {
      throw new NotFoundException(`no token ${tokenId}`);
    }
    return toToken(res.rows[0]);
  }

  // Asks the contract what it is, and believes the contract.
  //
  // This is what makes a seeded or hand-entered address safe. If the
  // address in the registry is wrong -- a typo, a copy from the wrong
  // chain, a scam contract with a convincing name -- then either it is not
  // a token at all and the call reverts, or it is a different token and
  // its symbol and decimals will not match the row. Either way the token
  // stays unusable, and a wrong address is a configuration error rather
  // than a loss.
  //
  // Decimals is the one that has to match exactly. A six-decimal token
  // registered as eighteen makes every amount limit a million million
  // times too generous, and nothing about the transaction looks unusual.
  async verify(tokenId: string): Promise<Token> {
    const token = await this.byId(tokenId);
    if (!token) {
      throw new NotFoundException(`no token ${tokenId}`);
    }
    if (!token.contractAddress) {
      throw new BadRequestException(
        `${token.symbol} has no contract address recorded, so there is nothing to verify. ` +
          'Record the address obtained from the issuer first.',
      );
    }

    let onChainSymbol: string;
    let onChainDecimals: number;
    try {
      const [symbolRaw, decimalsRaw] = await Promise.all([
        this.rpc.call(token.chainId, token.contractAddress, SELECTOR_SYMBOL),
        this.rpc.call(token.chainId, token.contractAddress, SELECTOR_DECIMALS),
      ]);
      onChainSymbol = decodeSymbol(symbolRaw);
      onChainDecimals = decodeDecimals(decimalsRaw);
    } catch (err) {
      const message = (err as Error).message;
      await this.recordVerificationFailure(tokenId, message);
      throw new BadRequestException(
        `could not read ${token.symbol} at ${token.contractAddress} on chain ${token.chainId}: ${message}`,
      );
    }

    const mismatches: string[] = [];
    if (onChainDecimals !== token.decimals) {
      mismatches.push(
        `the contract reports ${onChainDecimals} decimals, the registry says ${token.decimals}`,
      );
    }
    // Symbol compared case-insensitively and trimmed. Issuers are not
    // consistent about case and a mismatch there is cosmetic, unlike
    // decimals -- but it is still reported, because a symbol that differs
    // entirely means this is not the contract somebody thought it was.
    if (onChainSymbol.trim().toUpperCase() !== token.symbol.trim().toUpperCase()) {
      mismatches.push(
        `the contract calls itself ${JSON.stringify(onChainSymbol)}, the registry says ${JSON.stringify(token.symbol)}`,
      );
    }

    if (mismatches.length > 0) {
      const detail = mismatches.join('; ');
      await this.recordVerificationFailure(tokenId, detail);
      throw new BadRequestException(
        `${token.contractAddress} on chain ${token.chainId} is not the token this row describes: ${detail}. ` +
          'The token has been left unusable.',
      );
    }

    const res = await this.pool.query<TokenRow>(
      `UPDATE tokens
          SET status = 'verified',
              verified_symbol = $2, verified_decimals = $3,
              verified_at = NOW(), verification_error = NULL,
              updated_at = NOW()
        WHERE token_id = $1
        RETURNING ${COLUMNS}`,
      [tokenId, onChainSymbol, onChainDecimals],
    );
    this.logger.log(
      `verified ${token.symbol} at ${token.contractAddress} on chain ${token.chainId}`,
    );
    return toToken(res.rows[0]);
  }

  private async recordVerificationFailure(tokenId: string, error: string) {
    // Status is not advanced. A failed verification leaves the token
    // exactly as unusable as it was, with the reason attached.
    await this.pool.query(
      `UPDATE tokens SET verification_error = $2, updated_at = NOW() WHERE token_id = $1`,
      [tokenId, error],
    );
  }
}

// symbol() returns a string, which ABI-encodes as offset, length, bytes.
//
// Some early tokens -- MKR is the well-known one -- return a bytes32
// instead, which is a different layout entirely and would decode as
// nonsense through the string path. Both are handled because getting this
// wrong means a legitimate token fails verification forever.
export function decodeSymbol(raw: string): string {
  const body = raw.startsWith('0x') ? raw.slice(2) : raw;
  if (body.length === 0) {
    throw new Error('the contract returned nothing for symbol(); it is probably not an ERC-20');
  }
  if (body.length === 64) {
    // bytes32: right-padded with zeros.
    const bytes = Buffer.from(body, 'hex');
    return bytes.toString('utf8').replace(/\0+$/, '');
  }
  if (body.length < 128) {
    throw new Error('the reply to symbol() is too short to be an ABI-encoded string');
  }
  const length = Number(BigInt('0x' + body.slice(64, 128)));
  if (!Number.isSafeInteger(length) || length > 256) {
    throw new Error('the reply to symbol() declares an implausible length');
  }
  return Buffer.from(body.slice(128, 128 + length * 2), 'hex').toString('utf8');
}

export function decodeDecimals(raw: string): number {
  const body = raw.startsWith('0x') ? raw.slice(2) : raw;
  if (body.length !== 64) {
    throw new Error('the reply to decimals() is not a single 32-byte word');
  }
  const value = BigInt('0x' + body);
  if (value > 36n) {
    throw new Error(`the contract reports ${value} decimals, which is not a real token`);
  }
  return Number(value);
}
