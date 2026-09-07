import { IsInt, IsOptional, IsString, Matches, Min } from 'class-validator';

// Validated body for POST /keys/:keyId/transactions.
//
// Note what is NOT here: a message/digest field. The gateway builds the
// transaction from these fields and hashes it itself, so the digest that
// gets threshold-signed is one this service computed from the same values
// it handed to the policy engine. That is the whole difference from
// POST /keys/:keyId/sign, where the caller supplies an opaque digest and
// policy can only evaluate what the caller *claims* it commits to.
export class SignTransactionDto {
  @Matches(/^0x[0-9a-fA-F]{40}$/, { message: 'to must be a 20-byte hex address' })
  to: string;

  @Matches(/^[0-9]+$/, { message: 'value must be a base-10 wei string' })
  value: string;

  // Contract calldata. Policy evaluates to/value/chainId, not the meaning of
  // this payload -- a transfer to a whitelisted address that carries a call
  // to something else is still within policy as written. Constraining that
  // properly means decoding calldata against an ABI allowlist, which is a
  // separate piece of work.
  @IsOptional()
  @Matches(/^0x([0-9a-fA-F]{2})*$/, { message: 'data must be 0x-prefixed hex' })
  data?: string;

  @IsInt()
  @Min(21000)
  gasLimit: number;

  @IsInt()
  @Min(0)
  nonce: number;

  // Required, not defaulted: it is part of the signing hash, and a wrong or
  // absent chain id makes the signed transaction replayable on other EVM
  // chains.
  @IsInt()
  @Min(1)
  chainId: number;

  // Legacy fee.
  @IsOptional()
  @Matches(/^[0-9]+$/, { message: 'gasPrice must be a base-10 wei string' })
  gasPrice?: string;

  // EIP-1559 pair; supply both or neither.
  @IsOptional()
  @Matches(/^[0-9]+$/, { message: 'maxFeePerGas must be a base-10 wei string' })
  maxFeePerGas?: string;

  @IsOptional()
  @Matches(/^[0-9]+$/, {
    message: 'maxPriorityFeePerGas must be a base-10 wei string',
  })
  maxPriorityFeePerGas?: string;

  @IsOptional()
  @IsString()
  country?: string; // ISO country code for geographic policy checks

  @IsOptional()
  @IsString()
  idempotencyKey?: string;
}
