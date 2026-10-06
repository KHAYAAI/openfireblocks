import { Injectable } from '@nestjs/common';

// A contract call the platform can read in full, and says what it does.
export interface GovernedCall { description: string }

// Something that knows a specific set of contracts and the exact calls to them it will
// vouch for. The signing path asks it before it falls back to refusing calldata it cannot
// decode: an ERC-20 call is read by the ERC-20 decoder, a call to a registered security
// token by the tokenisation module, and anything else is still refused unless the
// organisation has turned on arbitrary contract calls. That keeps a narrow, audited
// allowlist from needing the blanket escape hatch.
export interface GovernedContractRecogniser {
  recognise(customerId: string, chainId: number, to: string, value: string, data: string): Promise<GovernedCall | null>;
}

// A seam, like NativeApprovalHooks: the tokenisation module depends on the keys module,
// so the keys module cannot import it; the module that knows the contracts registers
// itself at start-up.
@Injectable()
export class GovernedContracts {
  private recogniser: GovernedContractRecogniser | null = null;
  register(r: GovernedContractRecogniser) { this.recogniser = r; }
  async recognise(customerId: string, chainId: number, to: string, value: string, data: string): Promise<GovernedCall | null> {
    return this.recogniser ? this.recogniser.recognise(customerId, chainId, to, value, data) : null;
  }
}
