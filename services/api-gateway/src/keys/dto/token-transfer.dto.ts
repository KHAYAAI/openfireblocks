import { IsInt, IsOptional, IsString, Length, Matches, Min } from 'class-validator';

// Validated body for POST /keys/:keyId/token-transfers.
//
// Note what is NOT here: calldata. That is the entire reason this route
// exists alongside POST /keys/:keyId/transactions.
//
// There, the customer supplies the ABI-encoded call and the platform
// decodes it to find out what it does. The decoding is strict, so it is
// sound -- but the recipient and the amount that policy evaluates are
// still read out of bytes the caller composed, and every guarantee rests
// on the decoder being right about every encoding a caller might produce.
//
// Here the customer states the recipient and the amount, and the platform
// encodes the call. There is nothing for the caller's claim and the signed
// bytes to disagree about, because the caller never supplied any bytes.
// The same reasoning that put the Bitcoin route next to the Ethereum one:
// when the platform builds the transaction, policy governs what is really
// being signed rather than what the caller says it is.
export class TokenTransferDto {
  // Which token, by registry symbol ('USDC', 'ZARP') or by contract
  // address. A symbol is resolved against the registry for this chain, so
  // it cannot mean a contract the caller chose -- which is the point of
  // having a registry at all.
  @IsString()
  @Length(1, 42)
  token: string;

  @Matches(/^0x[0-9a-fA-F]{40}$/, { message: 'recipient must be a 20-byte hex address' })
  recipient: string;

  // A decimal amount in the token's own units: "100.50" of USDC, not
  // 100500000 base units.
  //
  // A string, and matched rather than typed as a number, because a
  // JavaScript number cannot hold an 18-decimal token amount exactly and
  // an amount that arrives as a float has already been rounded before any
  // check here could see it. Exponents, separators and signs are refused
  // rather than interpreted: "1e6" means different things to different
  // parsers, and the one place that must never disagree about an amount is
  // between the control that approved it and the bytes that move it.
  //
  // Excess precision is refused, not rounded -- see parseUnits. A customer
  // asking to send 1.0000005 USDC has misunderstood the token, and sending
  // them 1.000000 silently is a worse answer than telling them.
  @Matches(/^[0-9]+(\.[0-9]+)?$/, {
    message:
      'amount must be a plain decimal number in the token\'s own units, with no sign, exponent or separators',
  })
  amount: string;

  @IsInt()
  @Min(1)
  chainId: number;

  // The account nonce and the fee. Required for the same reason the
  // Ethereum route requires them: an account model expects the caller to
  // know the account's state, and guessing a nonce on their behalf is how
  // two transactions collide.
  @IsInt()
  @Min(0)
  nonce: number;

  // Optional, because the platform knows better than the caller what an
  // ERC-20 transfer costs. A plain transfer is around 65,000 gas against
  // the 21,000 of a native send, so a caller reusing the native figure
  // would produce a transaction that runs out of gas and still pays the
  // fee. Supplying it explicitly is allowed -- some tokens cost more, and
  // a first transfer to an address that has never held the token costs
  // more again.
  @IsOptional()
  @IsInt()
  @Min(21000)
  gasLimit?: number;

  @IsOptional()
  @Matches(/^[0-9]+$/, { message: 'gasPrice must be a base-10 wei string' })
  gasPrice?: string;

  @IsOptional()
  @Matches(/^[0-9]+$/, { message: 'maxFeePerGas must be a base-10 wei string' })
  maxFeePerGas?: string;

  @IsOptional()
  @Matches(/^[0-9]+$/, { message: 'maxPriorityFeePerGas must be a base-10 wei string' })
  maxPriorityFeePerGas?: string;

  @IsOptional()
  @IsString()
  country?: string;

  @IsOptional()
  @IsString()
  idempotencyKey?: string;
}
