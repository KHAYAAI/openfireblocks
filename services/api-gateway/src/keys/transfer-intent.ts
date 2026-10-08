import { BadRequestException, ServiceUnavailableException } from '@nestjs/common';
import { Erc20Call, decodeErc20Call, hasCalldata } from './erc20';

// What a transaction moves, as opposed to what it looks like on the wire.
//
// policyTo and assetAmount are what the controls evaluate; effectiveTo and
// effectiveAmount are what gets recorded against the transaction so that a
// later reader -- a regulatory aggregate, an auditor, a customer's own
// reconciliation -- can answer "who received how much of what" without an
// ABI decoder. They are null when the platform could not tell, which is a
// truthful answer and a better one than a guess.
//
// Shared by every route that signs an EVM transaction (KeysService's own
// threshold-key path and SignService's single-key POST /sign) so that
// calldata is decoded, and policy evaluated against what it actually
// moves, exactly once and the same way everywhere. A route that built its
// own copy of this -- or skipped it -- is how a transaction carrying an
// ERC-20 transfer() ended up evaluated on its envelope's to/value (the
// token contract and zero) instead of the decoded recipient and amount.
export interface TransferIntent {
  policyTo: string;
  asset: string;
  assetAmount: string;
  assetDecimals: number;
  pegCurrency?: string;
  isAllowance?: boolean;
  effectiveTo: string | null;
  effectiveAmount: string | null;
  contractAddress?: string;
  method?: string;
  unreadable?: string;
}

// Narrow interfaces for the two optional collaborators, so this module
// does not need to import TokenRegistryService or GovernedContracts
// themselves (and the modules that provide them do not need to import
// this one either way).
export interface TokenLookup {
  byContract(chainId: number, address: string): Promise<{
    symbol: string;
    decimals: number;
    pegCurrency?: string | null;
    contractAddress?: string | null;
    status: string;
  } | null>;
}

export interface GovernedCallRecogniser {
  recognise(
    customerId: string,
    chainId: number,
    to: string,
    value: string,
    data: string,
  ): Promise<{ description: string } | null>;
}

function nativeIntent(to: string, amount: string): TransferIntent {
  return {
    policyTo: to,
    asset: 'NATIVE',
    assetAmount: amount,
    assetDecimals: 18,
    effectiveTo: to,
    effectiveAmount: amount,
  };
}

// Calldata the platform cannot account for.
//
// Refused by default. The tenant-level escape hatch carries the same
// warning everywhere it is checked: with it on, the to and value that
// policy evaluates are the contract and zero, so the amount limit and
// the whitelist are not evaluating this transaction's payment at all.
// Calling a contract that is not an ERC-20 is a real requirement and
// there is no other route for it, so the capability exists -- turning it
// on is a decision somebody makes on the record, not a default.
function unreadableCalldata(
  arbitraryContractCallsEnabled: boolean,
  to: string,
  value: string,
  why: string,
): TransferIntent {
  if (!arbitraryContractCallsEnabled) {
    throw new BadRequestException(
      `this transaction carries calldata the platform cannot account for: ${why}. ` +
        'Policy determines the recipient and amount by decoding the call, and it cannot ' +
        'decode this one -- so signing it would mean no control had read what it does. ' +
        'To send a token, register and verify it and use POST /keys/:keyId/token-transfers. ' +
        'To call a contract that is not an ERC-20, arbitrary contract calls must be ' +
        'enabled for this account explicitly.',
    );
  }
  return {
    policyTo: to,
    asset: 'UNKNOWN',
    assetAmount: value,
    assetDecimals: 18,
    effectiveTo: null,
    effectiveAmount: null,
    unreadable: why,
  };
}

// Works out what a transaction actually moves, before anything decides
// whether it is allowed.
//
// Three outcomes, and the third is the interesting one:
//
//   no calldata          -- a native transfer, governed as before
//   a registered token   -- governed on the decoded recipient and amount
//   anything else        -- refused, unless the tenant has explicitly
//                           accepted that policy cannot read it
export async function resolveTransferIntent(
  ctx: {
    customerId: string;
    arbitraryContractCallsEnabled: boolean;
    tokens?: TokenLookup;
    governed?: GovernedCallRecogniser;
  },
  chainId: number,
  to: string,
  value: string,
  data: string | undefined,
): Promise<TransferIntent> {
  if (!hasCalldata(data)) {
    return nativeIntent(to, value);
  }

  // A call to a contract a module of this platform governs, with a selector and
  // arguments that module has read in full. Not a payment: nothing is paid to anyone, so
  // it has no recipient or amount to limit, and it is not an unreadable call either. The
  // policy engine sees an unnamed asset of zero and escalates it for approval.
  const governed = await ctx.governed?.recognise(ctx.customerId, chainId, to, value, data as string);
  if (governed) {
    return {
      policyTo: to,
      asset: 'GOVERNED',
      assetAmount: '0',
      assetDecimals: 18,
      effectiveTo: null,
      effectiveAmount: null,
      method: governed.description,
    };
  }

  let call: Erc20Call | null;
  try {
    call = decodeErc20Call(data);
  } catch (err) {
    // Undecodable calldata. The platform cannot say who gets paid or how
    // much, so it cannot claim any control evaluated this transaction.
    return unreadableCalldata(ctx.arbitraryContractCallsEnabled, to, value, (err as Error).message);
  }
  if (!call) {
    // hasCalldata said yes and the decoder said no calldata. Not
    // reachable, but returning a native intent here would mean treating
    // a contract call as a payment to the contract.
    throw new BadRequestException('calldata could not be interpreted');
  }

  if (!ctx.tokens) {
    throw new ServiceUnavailableException(
      'the token registry is not configured, so token transfers cannot be governed or signed',
    );
  }

  const token = await ctx.tokens.byContract(chainId, to);
  if (!token || token.status !== 'verified') {
    // A token transfer to a contract nobody registered. The calldata
    // decoded, so the recipient and amount are known -- but the asset is
    // not, which means its decimals are not, which means the amount is a
    // number with no unit. A limit cannot be applied to that.
    return unreadableCalldata(
      ctx.arbitraryContractCallsEnabled,
      to,
      value,
      token
        ? `${token.symbol} on chain ${chainId} is registered but not verified (${token.status})`
        : `no token is registered at ${to} on chain ${chainId}`,
    );
  }

  return {
    policyTo: call.recipient,
    asset: token.symbol,
    assetAmount: call.amount,
    assetDecimals: token.decimals,
    pegCurrency: token.pegCurrency ?? undefined,
    isAllowance: call.isAllowance,
    effectiveTo: call.recipient,
    effectiveAmount: call.amount,
    contractAddress: token.contractAddress ?? to,
    method: call.method,
  };
}
