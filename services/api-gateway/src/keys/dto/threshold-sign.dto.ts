import { IsNumber, IsOptional, IsString, Matches } from 'class-validator';

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
  @Matches(/^0x[0-9a-fA-F]{40}$/, { message: 'to must be a 20-byte hex address' })
  to: string;

  @Matches(/^[0-9]+$/, { message: 'value must be a base-10 wei string' })
  value: string;

  // Numeric EVM chain id (1 for mainnet, 11155111 for Sepolia), NOT the
  // key's `blockchain` name. The policy service takes chainId as an int
  // and rejects a string outright, so the two are not interchangeable.
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
