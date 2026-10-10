import { IsBoolean, IsObject, IsOptional, IsString, Matches } from 'class-validator';
import type { TravelRuleInput } from '../../travel-rule/travel-rule';

// Validated body for POST /keys/:keyId/solana-transactions.
//
// Asks only for what the customer knows: where the money goes and how much.
// The blockhash, the fee, the account ordering and the rent rules are the
// platform's to work out, and all of them are things a caller gets wrong in
// ways that lose money or strand a transfer after a ceremony has run.
export class SolanaTransactionDto {
  // base58, 32 bytes -> 32 to 44 characters. Decoding to 32 bytes is checked
  // by the signer; this only keeps obvious garbage out of it.
  @Matches(/^[1-9A-HJ-NP-Za-km-z]{32,44}$/, { message: 'destination must be a base58 Solana address' })
  destination: string;

  // Lamports (1 SOL = 1,000,000,000), base-10, as a string so no precision
  // is lost to a JavaScript number.
  @Matches(/^[1-9][0-9]{0,18}$/, { message: 'amount must be a positive integer number of lamports, as a string' })
  amount: string;

  // Assemble and verify without relaying. Off by default is wrong for a
  // payment API, so it defaults to relaying and this opts out.
  @IsOptional()
  @IsBoolean()
  broadcast?: boolean;

  @IsOptional()
  @IsString()
  country?: string;

  @IsOptional()
  @IsString()
  idempotencyKey?: string;

  @IsOptional()
  @IsObject()
  travelRule?: TravelRuleInput;
}
