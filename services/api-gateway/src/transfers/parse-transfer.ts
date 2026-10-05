import { BadRequestException } from '@nestjs/common';
import { plainToInstance } from 'class-transformer';
import { validate } from 'class-validator';
import type { TransferKind } from '../approvals/approvals.service';
import { BitcoinTransactionDto } from '../keys/dto/bitcoin-transaction.dto';
import { SolanaTransactionDto } from '../keys/dto/solana-transaction.dto';
import { CosmosTransactionDto } from '../keys/dto/cosmos-transaction.dto';
import { EvmTransferDto } from './evm-transfer.dto';

// Which kind of transfer a key's blockchain makes. The caller does not say: a
// key can only ever send on its own chain, and letting the request pick would
// be a way to ask for a Solana transfer from a Bitcoin key.
export function kindOfBlockchain(blockchain: string): TransferKind {
  switch (blockchain) {
    case 'bitcoin': return 'bitcoin';
    case 'solana': return 'solana';
    case 'cosmos': return 'cosmos';
    case 'ethereum':
    case 'polygon': return 'evm';
  }
  throw new BadRequestException(`transfers are not supported for ${blockchain} keys`);
}

const CLASSES = { bitcoin: BitcoinTransactionDto, solana: SolanaTransactionDto, cosmos: CosmosTransactionDto, evm: EvmTransferDto } as const;

// Validates a body against the DTO for the key's chain, the way the global
// ValidationPipe would for a fixed route: unknown fields are refused.
export async function parseTransfer(kind: TransferKind, body: unknown) {
  const instance = plainToInstance(CLASSES[kind] as never, body ?? {}) as object;
  const errors = await validate(instance, { whitelist: true, forbidNonWhitelisted: true });
  if (errors.length) {
    throw new BadRequestException(errors.flatMap((e) => Object.values(e.constraints ?? { x: `${e.property} is invalid` })));
  }
  return instance as never;
}
