import { IsString, IsNumber, IsOptional, Min, Matches } from 'class-validator';

// Validated request body for POST /sign. ValidationPipe rejects anything that
// does not satisfy these constraints before the controller runs.
export class SignRequestDto {
  @IsNumber()
  chainId: number; // 11155111 for Sepolia, 1 for mainnet

  @Matches(/^0x[0-9a-fA-F]{40}$/, { message: 'to must be a 20-byte hex address' })
  to: string;

  @IsOptional()
  @IsString()
  data?: string; // 0x-prefixed call data, or omitted for a plain transfer

  @IsOptional()
  @Matches(/^[0-9]+$/, { message: 'value must be a base-10 wei string' })
  value?: string;

  @IsNumber()
  @Min(21000)
  gasLimit: number;

  @Matches(/^[0-9]+$/, { message: 'gasPrice must be a base-10 wei string' })
  gasPrice: string;

  @IsNumber()
  @Min(0)
  nonce: number;

  @IsOptional()
  @IsString()
  country?: string; // ISO country code for geographic policy checks
}
