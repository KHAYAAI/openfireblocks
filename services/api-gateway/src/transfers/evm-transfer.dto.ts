import { IsInt, IsObject, IsOptional, IsString, Matches, Min } from 'class-validator';

// An EVM transfer as a person asks for it. No nonce, gas limit or fees: those
// are read from the chain when the transfer runs.
export class EvmTransferDto {
  @IsInt()
  @Min(1)
  chainId: number;

  @Matches(/^0x[0-9a-fA-F]{40}$/, { message: 'destination must be a 20-byte hex address' })
  destination: string;

  // Wei, base-10, as a string: a JavaScript number cannot hold 18 decimals.
  @Matches(/^[1-9][0-9]{0,77}$/, { message: 'amount must be a positive integer number of wei, as a string' })
  amount: string;

  @IsOptional() @IsString() country?: string;
  @IsOptional() @IsString() idempotencyKey?: string;
  @IsOptional() @IsObject() travelRule?: Record<string, unknown>;
}
