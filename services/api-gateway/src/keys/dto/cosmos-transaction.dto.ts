import { IsBoolean, IsObject, IsOptional, IsString, Matches, MaxLength } from 'class-validator';
import type { TravelRuleInput } from '../../travel-rule/travel-rule';

// Validated body for POST /keys/:keyId/cosmos-transactions.
export class CosmosTransactionDto {
  // bech32: a lowercase prefix, the separator "1", then at least 38
  // characters of the bech32 alphabet (no 1, b, i or o). The signer checks
  // the checksum and that the prefix is the chain's own.
  @Matches(/^[a-z]{1,20}1[02-9ac-hj-np-z]{38,}$/, { message: 'destination must be a bech32 address' })
  destination: string;

  // Base units (uatom, not ATOM), base-10, as a string.
  @Matches(/^[1-9][0-9]{0,37}$/, { message: 'amount must be a positive integer number of base units, as a string' })
  amount: string;

  // Defaults to the chain's staking/fee denom.
  @IsOptional()
  @Matches(/^[a-zA-Z][a-zA-Z0-9/:._-]{2,127}$/, { message: 'denom is not a valid Cosmos denomination' })
  denom?: string;

  @IsOptional()
  @IsString()
  @MaxLength(256)
  memo?: string;

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
