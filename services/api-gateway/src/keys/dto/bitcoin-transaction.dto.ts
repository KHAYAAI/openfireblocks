import { IsBoolean, IsInt, IsOptional, IsString, Length, Matches, Max, Min } from 'class-validator';

// Validated body for POST /keys/:keyId/bitcoin-transactions.
//
// Deliberately smaller than SignTransactionDto. An Ethereum transaction
// makes the caller supply a nonce, a gas limit and a fee: those are
// consequences of an account model where the caller is expected to know the
// account's state. Bitcoin has no account state to know -- which coins to
// spend, what they are worth, and what the fee should be are all things the
// platform is better placed to work out than the customer is, and getting
// any of them wrong is how money is lost.
//
// So this asks for the two things only the customer knows: where the money
// goes and how much.
export class BitcoinTransactionDto {
  // A base58 or bech32 address. Not pattern-matched here: Bitcoin has five
  // address encodings in active use and a regex covering them is a second,
  // drifting copy of validation the chain layer already does properly --
  // against the network's own parameters, which is the part that actually
  // matters, since a mainnet address decodes perfectly well on testnet.
  @IsString()
  @Length(26, 90, { message: 'destination must be a Bitcoin address' })
  destination: string;

  // Satoshis, base-10. A string rather than a number because JavaScript
  // numbers lose integer precision above 2^53, and while 21 million BTC in
  // satoshis fits, an amount arriving as a float has already been rounded
  // by the time any check here could catch it.
  @Matches(/^[1-9][0-9]{0,17}$/, {
    message: 'amount must be a positive integer number of satoshis, as a string',
  })
  amount: string;

  // Satoshis per virtual byte. Omitted asks the node to estimate, which is
  // the right default -- a customer guessing a fee rate against live
  // mempool conditions will guess worse than the node does.
  @IsOptional()
  @IsInt()
  @Min(1, { message: 'feeRate must be at least 1 sat/vB; nothing relays below that' })
  @Max(10_000)
  feeRate?: number;

  // How many blocks the fee should target when the node estimates. Ignored
  // when feeRate is supplied.
  @IsOptional()
  @IsInt()
  @Min(1)
  @Max(1008)
  confirmationTarget?: number;

  // Change goes back to the key's own segwit address unless this says
  // otherwise. An explicit field because sending change elsewhere is a real
  // requirement and also the way a wallet accidentally pays its remainder
  // to a stranger, so it should never be implicit.
  @IsOptional()
  @IsString()
  @Length(26, 90)
  changeAddress?: string;

  // Defaults to 1. Zero would spend unconfirmed outputs -- legal, but it
  // makes this transaction invalid if the parent is replaced, which is not
  // a default a custody platform should pick for a customer.
  @IsOptional()
  @IsInt()
  @Min(0)
  @Max(100)
  minConfirmations?: number;

  // Whether to relay the transaction. Defaults to true: a customer calling
  // this route wants to send Bitcoin. Setting it false returns the signed
  // bytes without broadcasting, for a caller who wants to inspect or relay
  // them elsewhere.
  @IsOptional()
  @IsBoolean()
  broadcast?: boolean;

  // Reusing an idempotency key returns the original ceremony's result
  // rather than running a second one.
  @IsOptional()
  @IsString()
  @Length(1, 128)
  idempotencyKey?: string;

  // Passed to the policy engine for jurisdiction rules.
  @IsOptional()
  @IsString()
  @Length(2, 2)
  country?: string;
}
