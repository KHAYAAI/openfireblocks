import {
  IsInt,
  IsOptional,
  IsString,
  Length,
  Matches,
  Max,
  Min,
} from 'class-validator';

// Registering a token an operator is prepared to have the platform move.
//
// Note what a caller cannot do here: mark a token verified. That status
// only ever comes from a node agreeing with the row, so the worst outcome
// of a mistake in this body is a token that does not work.
export class RegisterTokenDto {
  @IsInt()
  @Min(1)
  chainId: number;

  // Optional, deliberately. A token can be registered before anyone has
  // confirmed its contract address -- which is the honest state for an
  // asset the platform intends to support and whose address has to come
  // from the issuer rather than from a search result.
  @IsOptional()
  @Matches(/^0x[0-9a-fA-F]{40}$/, { message: 'contractAddress must be a 20-byte hex address' })
  contractAddress?: string;

  @IsString()
  @Length(1, 32)
  symbol: string;

  @IsString()
  @Length(1, 128)
  name: string;

  // No default. USDC is 6 and DAI is 18, so a default would be wrong for
  // roughly half of everything and wrong by a factor of a million million.
  @IsInt()
  @Min(0)
  @Max(36)
  decimals: number;

  // ISO 4217, uppercase: 'USD', 'ZAR'. Absent means the token is not
  // pegged and money-denominated controls will not treat it as currency.
  @IsOptional()
  @Matches(/^[A-Z]{3}$/, { message: 'pegCurrency must be a three-letter ISO 4217 code' })
  pegCurrency?: string;

  @IsOptional()
  @IsString()
  @Length(1, 128)
  issuer?: string;

  @IsOptional()
  @IsString()
  notes?: string;
}

export class SetTokenAddressDto {
  @Matches(/^0x[0-9a-fA-F]{40}$/, { message: 'contractAddress must be a 20-byte hex address' })
  contractAddress: string;
}

export class SuspendTokenDto {
  // Required. A token withdrawn from use without a recorded reason is a
  // support ticket for whoever finds it later.
  @IsString()
  @Length(1, 500)
  reason: string;
}
