import { IsNumber, IsOptional, IsString, Length, Matches } from 'class-validator';

// Validated body for POST /keys/:keyId/sign.
export class ThresholdSignRequestDto {
  // The 32-byte digest to sign, hex, no 0x prefix. ECDSA signs a digest,
  // not a message, so the caller does the hashing (keccak256 for an
  // Ethereum transaction or EIP-191 message).
  @Matches(/^[0-9a-fA-F]{64}$/, {
    message: 'message must be a 32-byte hex digest (64 hex characters, no 0x prefix)',
  })
  message: string;

  // Declared intent, evaluated by the policy engine.
  //
  // Required, and deliberately so: this platform should not produce a
  // threshold signature over an opaque digest with no stated purpose.
  // But note the limitation, which is real and cannot be engineered away
  // at this layer -- a digest is opaque, so the platform CANNOT verify
  // that these fields describe what the digest actually commits to. A
  // caller who lies here gets a policy decision about a transaction they
  // are not signing. Policy enforcement over *verified* intent requires
  // the settlement path, where the gateway builds the transaction itself
  // and hashes it. See docs/deployment/CLUSTER-DEPLOYMENT.md.
  //
  // Not constrained to an Ethereum address. This route is how a chain the
  // gateway cannot build transactions for gets a digest signed -- Bitcoin
  // goes through it, and a Bitcoin destination is a base58 or bech32 string,
  // not 20 bytes of hex. Requiring an EVM address here made the platform
  // multi-chain in its key derivation and single-chain in its signing API.
  //
  // Length-bounded rather than pattern-matched: the policy engine treats
  // this as an opaque destination identifier for whitelist comparison, and
  // any per-chain regex here would be a second, drifting copy of address
  // validation that the chain layer already does properly.
  @IsString()
  @Length(1, 128, { message: 'to must be a destination address for the target chain' })
  to: string;

  // Base-10 integer in the chain's smallest unit: wei for Ethereum,
  // satoshis for Bitcoin. Policy compares magnitudes, so the unit has to be
  // consistent per chain rather than universal.
  @Matches(/^[0-9]+$/, { message: 'value must be a base-10 integer string in the chain\'s smallest unit' })
  value: string;

  // Numeric EVM chain id (1 for mainnet, 11155111 for Sepolia), NOT the
  // key's `blockchain` name. The policy service takes chainId as an int
  // and rejects a string outright, so the two are not interchangeable.
  //
  // Zero for chains that have no such concept, which is how Bitcoin
  // requests arrive. The field stays required rather than optional so a
  // caller has to state which chain a signature is for, even when the
  // answer is "not an EVM one".
  @IsNumber()
  chainId: number;

  @IsOptional()
  @IsString()
  country?: string; // ISO country code for geographic policy checks

  // Makes a retried request reuse the same signing ceremony instead of
  // starting a second one over the same digest.
  @IsOptional()
  @IsString()
  idempotencyKey?: string;
}
