import {
  BadRequestException,
  ConflictException,
  ForbiddenException,
  Injectable,
  Logger,
  NotFoundException,
  ServiceUnavailableException,
} from '@nestjs/common';
import { PostgresService } from '../database/postgres.service';
import { KeysTemporalService } from './keys-temporal.service';
import { CreateKeyRequest } from './dto/create-key.dto';
import { Customer } from '../customers/customer.service';
import { PolicyService } from '../policies/policy.service';
import { ThresholdSignRequestDto } from './dto/threshold-sign.dto';
import { SignTransactionDto } from './dto/sign-transaction.dto';
import { BitcoinTransactionDto } from './dto/bitcoin-transaction.dto';
import {
  BitcoinSignerClient,
  SignerError,
  splitSignature,
} from './bitcoin-signer-client';
import { WebhookEmitter } from '../webhooks/webhooks.service';
import {
  BuiltTx,
  SignedTx,
  TransactionBuildError,
  assembleSignedTransaction,
  buildUnsignedTransaction,
} from './eth-transaction';
import { v4 as uuidv4, v5 as uuidv5 } from 'uuid';
import {
  Erc20Call,
  Erc20DecodeError,
  decodeErc20Call,
  encodeTransfer,
  encodeBalanceOf,
  formatUnits,
  hasCalldata,
  parseUnits,
} from './erc20';
import { TokenRegistryService } from '../tokens/token-registry.service';
import { EvmRpcService } from '../tokens/evm-rpc.service';
import { TokenTransferDto } from './dto/token-transfer.dto';

// What a transaction moves, as opposed to what it looks like on the wire.
//
// policyTo and assetAmount are what the controls evaluate; effectiveTo and
// effectiveAmount are what gets recorded against the transaction so that a
// later reader -- a regulatory aggregate, an auditor, a customer's own
// reconciliation -- can answer "who received how much of what" without an
// ABI decoder. They are null when the platform could not tell, which is a
// truthful answer and a better one than a guess.
interface TransferIntent {
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

// Derives each party's endpoint from MPC_PARTY_ENDPOINT_TEMPLATE (default
// http://party-{id}:7000, matching infrastructure/helm/openfireblocks's
// per-party Service naming -- templates/mpc-party.yaml creates one
// Service named party-<N> per party, port from .Values.mpcParty.port).
// Party ids are 1-indexed, matching that chart's {{ range until }} loop
// and services/mpc-party/main.go's own PARTY_ID convention.
function derivePartyEndpoints(totalParties: number): {
  partyIds: number[];
  partyEndpoints: string[];
} {
  const template = process.env.MPC_PARTY_ENDPOINT_TEMPLATE ?? 'http://party-{id}:7000';
  const partyIds = Array.from({ length: totalParties }, (_, i) => i + 1);
  const partyEndpoints = partyIds.map((id) => template.replace('{id}', String(id)));
  return { partyIds, partyEndpoints };
}

// A UUID for a row keyed by a caller-supplied idempotency key.
//
// signing.transactions.request_id is a UUID column; an idempotency key is
// a free string the customer chose. Generating a fresh UUID per call would
// make a retry insert a second row, so one logical transfer would appear
// twice in the aggregate that decides whether a regulatory filing is due
// -- and over-reporting is as much a defect as under-reporting when the
// number is what a filing is based on. Deriving it instead makes retries
// land on the same row.
//
// A caller-supplied key that already is a UUID is used as-is, so existing
// rows keep their identity.
const REQUEST_ID_NAMESPACE = '6d3a1b5e-1f0f-4a3a-9f1a-9b0f0f0d0c0b';
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

export function stableUuid(requestId: string): string {
  return UUID_PATTERN.test(requestId)
    ? requestId
    : uuidv5(requestId, REQUEST_ID_NAMESPACE);
}

@Injectable()
export class KeysService {
  private readonly logger = new Logger(KeysService.name);

  private readonly bitcoin = new BitcoinSignerClient();

  constructor(
    private readonly postgres: PostgresService,
    private readonly temporal: KeysTemporalService,
    private readonly policy: PolicyService,
    // Optional so the many tests that construct this service directly do
    // not each have to stub a notifier they are not testing. A missing
    // emitter means no announcements, which is the same as a deployment
    // without the webhooks service.
    private readonly webhooks?: WebhookEmitter,
    // Optional for the same reason as the emitter: the existing tests
    // construct this service directly and none of them transact tokens. A
    // missing registry means no token is transactable, which is the
    // correct behaviour for a deployment that has not configured one.
    private readonly tokens?: TokenRegistryService,
    private readonly rpc?: EvmRpcService,
  ) {}

  // Works out what a transaction actually moves, before anything decides
  // whether it is allowed.
  //
  // This is the whole fix. An ERC-20 transfer carries its recipient and
  // amount inside the calldata; the transaction's own `to` is the token
  // contract and its `value` is zero. Handing those two fields to the
  // policy engine -- which is what happened before this existed -- means
  // the amount limit compares zero against the ceiling and the
  // counterparty whitelist sees an address that is identical for every
  // transfer of that token. Neither control could deny anything, and
  // nothing about the system looked different.
  //
  // Three outcomes, and the third is the interesting one:
  //
  //   no calldata          -- a native transfer, governed as before
  //   a registered token   -- governed on the decoded recipient and amount
  //   anything else        -- refused, unless the tenant has explicitly
  //                           accepted that policy cannot read it
  private async resolveTransferIntent(
    customer: Customer,
    chainId: number,
    to: string,
    value: string,
    data: string | undefined,
  ): Promise<TransferIntent> {
    if (!hasCalldata(data)) {
      return {
        policyTo: to,
        asset: 'NATIVE',
        assetAmount: value,
        assetDecimals: 18,
        effectiveTo: to,
        effectiveAmount: value,
      };
    }

    let call: Erc20Call | null;
    try {
      call = decodeErc20Call(data);
    } catch (err) {
      // Undecodable calldata. The platform cannot say who gets paid or how
      // much, so it cannot claim any control evaluated this transaction.
      return this.unreadableCalldata(customer, to, value, (err as Error).message);
    }
    if (!call) {
      // hasCalldata said yes and the decoder said no calldata. Not
      // reachable, but returning a native intent here would mean treating
      // a contract call as a payment to the contract.
      throw new BadRequestException('calldata could not be interpreted');
    }

    if (!this.tokens) {
      throw new ServiceUnavailableException(
        'the token registry is not configured, so token transfers cannot be governed or signed',
      );
    }

    const token = await this.tokens.byContract(chainId, to);
    if (!token || token.status !== 'verified') {
      // A token transfer to a contract nobody registered. The calldata
      // decoded, so the recipient and amount are known -- but the asset is
      // not, which means its decimals are not, which means the amount is a
      // number with no unit. A limit cannot be applied to that.
      return this.unreadableCalldata(
        customer,
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

  // Calldata the platform cannot account for.
  //
  // Refused by default. The tenant-level escape hatch mirrors
  // raw_digest_signing_enabled and carries the same warning: with it on,
  // the to and value that policy evaluates are the contract and zero, so
  // the amount limit and the whitelist are not evaluating this
  // transaction's payment at all. Calling a contract that is not an ERC-20
  // is a real requirement and there is no other route for it, so the
  // capability exists -- turning it on is a decision somebody makes on the
  // record, not a default.
  private unreadableCalldata(
    customer: Customer,
    to: string,
    value: string,
    why: string,
  ): TransferIntent {
    if (!customer.arbitrary_contract_calls_enabled) {
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
      // Not given a symbol. An unnamed asset with no peg is escalated for
      // approval by the policy engine rather than held to a limit it has
      // no units for.
      asset: 'UNKNOWN',
      assetAmount: value,
      assetDecimals: 18,
      effectiveTo: null,
      effectiveAmount: null,
      unreadable: why,
    };
  }

  // Announce, without ever letting the announcement affect the work.
  //
  // Deliberately not awaited by callers: the signature exists and the
  // money has moved by the time this runs, and a customer's notification
  // endpoint being slow must not extend or fail their request.
  private announce(
    customerId: string,
    eventType: Parameters<WebhookEmitter['emit']>[1],
    data: Record<string, unknown>,
  ): void {
    void this.webhooks?.emit(customerId, eventType, data);
  }

  async createKey(customer: Customer, req: CreateKeyRequest) {
    const keyId = uuidv4();
    const ceremonyId = uuidv4();
    const now = new Date();

    this.logger.log(
      `Creating key ${keyId} for customer ${customer.customer_id}`,
    );

    // Create key pair record in database
    const key = {
      key_id: keyId,
      customer_id: customer.customer_id,
      name: req.name,
      blockchain: req.blockchain,
      threshold: req.threshold,
      total_parties: req.total_parties,
      status: 'pending_dkg',
      created_at: now,
    };

    try {
      await this.postgres.createKey(key);
    } catch (err) {
      // 23505 = unique_violation. The only unique constraint reachable here
      // is (customer_id, name), so this is a name collision with one of the
      // customer's own live keys -- a client error, not a server one. It
      // used to escape as a raw 500 carrying the Postgres constraint text.
      // Migration 016 excludes failed keys from that uniqueness rule, so a
      // retry after a failed provisioning no longer lands here at all.
      if ((err as { code?: string }).code === '23505') {
        throw new ConflictException(
          `a key named ${JSON.stringify(req.name)} already exists`,
        );
      }
      throw err;
    }

    const workflowId = `provision-key-${keyId}`;
    await this.postgres.createCeremony({
      ceremonyId,
      keyId,
      customerId: customer.customer_id,
      threshold: req.threshold,
      totalParties: req.total_parties,
      workflowId,
    });

    const { partyIds, partyEndpoints } = derivePartyEndpoints(req.total_parties);

    try {
      // req.threshold here is "how many signatures required" (the
      // customer-facing meaning, e.g. threshold=2 for 2-of-3) --
      // DKGCeremonyRequest.K is tss-lib's threshold, meaning K+1
      // signatures required (see that field's doc comment in
      // services/temporal-worker/workflows/ceremony_types.go and
      // real_tss_live_test.go's "K: 1, // 2-of-3"). k = threshold - 1
      // converts between the two conventions.
      await this.temporal.start({
        keyId,
        ceremonyId,
        customerId: customer.customer_id,
        chainId: req.blockchain,
        n: req.total_parties,
        k: req.threshold - 1,
        partyIds,
        partyEndpoints,
      });
    } catch (err) {
      // The key_pairs/dkg_ceremonies rows above are a real, honest record
      // of an attempted ceremony that never actually started -- mark it
      // failed rather than leaving it looking perpetually "initiated", or
      // silently deleting the attempt from the audit trail.
      this.logger.error(
        `failed to start ProvisionKeyWorkflow for key ${keyId}: ${(err as Error).message}`,
      );
      await this.postgres
        .setCeremonyFailed(ceremonyId, customer.customer_id, (err as Error).message)
        .catch(() => undefined);
      // ...and the key itself, which otherwise sits at 'pending_dkg' with
      // no workflow behind it -- reading as perpetually provisioning, and
      // (before migration 016) permanently consuming its own name.
      await this.postgres
        .setKeyFailed(keyId, customer.customer_id)
        .catch(() => undefined);
      if (err instanceof ServiceUnavailableException) throw err;
      throw new ServiceUnavailableException('failed to start key provisioning');
    }

    this.announce(customer.customer_id, 'key.created', {
      key_id: keyId,
      ceremony_id: ceremonyId,
      blockchain: req.blockchain,
      threshold: req.threshold,
      total_parties: req.total_parties,
    });

    return {
      id: keyId,
      ceremony_id: ceremonyId,
      name: req.name,
      blockchain: req.blockchain,
      threshold: req.threshold,
      total_parties: req.total_parties,
      status: 'pending_dkg',
      address: null,
      public_key: null,
      created_at: now,
    };
  }

  // Produces a threshold signature with a key this customer provisioned.
  //
  // Before this existed there was no route that could use a key created by
  // POST /keys at all: POST /sign goes to mpc-signer, which is the
  // separate single-key, non-threshold service, and
  // ThresholdSigningWorkflow could only be started from inside Temporal.
  // A customer could provision a 2-of-3 key through the public API and had
  // no public API with which to sign with it.
  //
  // Fail-closed on policy, matching SignService: the policy engine is
  // consulted first and an unreachable policy service denies rather than
  // permits. See ThresholdSignRequestDto for what policy can and cannot
  // actually verify about a request to sign an opaque digest.
  //
  // Gated behind an explicit per-tenant capability, because policy here is
  // weaker than it looks. The digest is opaque, so the to/value/chainId the
  // policy engine evaluates are a *claim* by the caller: declare one
  // transaction, submit the digest of another, and the approval that comes
  // back is for a transaction nobody reviewed. signTransaction has no such
  // gap and is the route a tenant gets by default.
  //
  // This route still exists because signing things that are not Ethereum
  // transactions has no other path. Turning it on is a decision somebody
  // makes on the record, not a default.
  async signWithKey(customer: Customer, keyId: string, req: ThresholdSignRequestDto) {
    if (!customer.raw_digest_signing_enabled) {
      throw new ForbiddenException(
        'Signing a caller-supplied digest is not enabled for this account. ' +
          'Use POST /keys/:keyId/transactions, where the transaction is built ' +
          'and hashed by the service so policy governs exactly what is signed. ' +
          'If you need to sign something that is not an Ethereum transaction, ' +
          'raw digest signing can be enabled for this account explicitly.',
      );
    }

    const requestId = req.idempotencyKey ?? uuidv4();

    const key = await this.loadSignableKey(keyId, customer.customer_id);
    await this.enforcePolicy(customer, requestId, {
      to: req.to,
      value: req.value,
      chainId: req.chainId,
      country: req.country,
    });
    const signed = await this.runSigningCeremony(
      key,
      keyId,
      customer.customer_id,
      requestId,
      req.message,
    );

    return {
      request_id: requestId,
      key_id: keyId,
      address: key.address,
      message: req.message,
      signature: signed.signature,
      parties: signed.parties,
      threshold: key.threshold,
      total_parties: signed.totalParties,
    };
  }

  // Signs a transaction this service builds, rather than a digest the
  // caller supplies.
  //
  // That is the entire point of it existing alongside signWithKey. There,
  // the digest is opaque: policy evaluates what the caller *claims* it
  // commits to, and a caller who declares one transaction and submits the
  // digest of another gets a policy decision about the wrong transaction.
  // Here the fields policy sees are the fields that get hashed -- there is
  // no caller-supplied digest to disagree with them.
  async signTransaction(customer: Customer, keyId: string, req: SignTransactionDto) {
    const requestId = req.idempotencyKey ?? uuidv4();

    const key = await this.loadSignableKey(keyId, customer.customer_id);

    // Built BEFORE the policy call, deliberately: a request that cannot
    // produce a valid transaction should be rejected as malformed rather
    // than consuming a policy evaluation and a signing ceremony.
    let built: BuiltTx;
    try {
      built = buildUnsignedTransaction({
        to: req.to,
        value: req.value,
        data: req.data,
        gasLimit: req.gasLimit,
        nonce: req.nonce,
        chainId: req.chainId,
        gasPrice: req.gasPrice,
        maxFeePerGas: req.maxFeePerGas,
        maxPriorityFeePerGas: req.maxPriorityFeePerGas,
      });
    } catch (err) {
      if (err instanceof TransactionBuildError) {
        throw new BadRequestException((err as Error).message);
      }
      throw err;
    }

    // What the transaction actually moves, decoded from the same calldata
    // that just went into the bytes -- not the envelope's own to/value,
    // which for a token transfer are the contract and zero.
    const intent = await this.resolveTransferIntent(
      customer,
      req.chainId,
      req.to,
      req.value,
      req.data,
    );

    // The same fields that went into the bytes just hashed, read through
    // the calldata rather than around it.
    await this.enforcePolicy(customer, requestId, {
      to: intent.policyTo,
      value: req.value,
      chainId: req.chainId,
      country: req.country,
      asset: intent.asset,
      assetAmount: intent.assetAmount,
      assetDecimals: intent.assetDecimals,
      pegCurrency: intent.pegCurrency,
      isAllowance: intent.isAllowance,
    });

    const signed = await this.runSigningCeremony(
      key,
      keyId,
      customer.customer_id,
      requestId,
      built.signingHash,
    );

    // Refuses if the signature recovers to anything but this key's
    // address -- see assembleSignedTransaction. A ceremony can return a
    // well-formed signature belonging to a different key, and handing that
    // back would give the customer a valid transaction spending from an
    // address they do not control.
    let assembled: SignedTx;
    try {
      assembled = assembleSignedTransaction(built, signed.signature, key.address);
    } catch (err) {
      this.logger.error(
        `assembling the signed transaction for key ${keyId} failed: ${(err as Error).message}`,
      );
      throw new ServiceUnavailableException((err as Error).message);
    }

    await this.recordTransfer(customer, requestId, req.chainId, key.blockchain, {
      to: req.to,
      value: req.value,
      data: req.data ?? null,
      gasLimit: req.gasLimit,
      gasPrice: req.gasPrice ?? null,
      nonce: req.nonce,
      signedTx: assembled.raw,
      txHash: assembled.hash,
      intent,
    });

    return {
      request_id: requestId,
      key_id: keyId,
      from: assembled.from,
      to: req.to,
      value: req.value,
      chain_id: req.chainId,
      nonce: req.nonce,
      // What this transaction actually moves, as the platform understands
      // it. For a native transfer these repeat to/value; for a token they
      // are the only place the real recipient and amount appear.
      asset: intent.asset,
      recipient: intent.effectiveTo,
      amount: intent.effectiveAmount,
      signing_hash: built.signingHash,
      raw_transaction: assembled.raw,
      transaction_hash: assembled.hash,
      signature: signed.signature,
      parties: signed.parties,
      threshold: key.threshold,
      total_parties: signed.totalParties,
    };
  }

  // Sends a registered token from a threshold key.
  //
  // The stablecoin route, and the reason it is separate from
  // signTransaction: here the customer names a token, a recipient and an
  // amount, and the platform encodes the ERC-20 call. Nothing the caller
  // sends is bytes, so there is no encoding for the platform's
  // understanding and the signed transaction to differ about.
  //
  // The amount is in the token's own units -- "100.50" -- and converted
  // with the decimals the registry holds and a node has confirmed. That
  // conversion is the single most dangerous arithmetic in this file: USDC
  // is six decimals and DAI is eighteen, so using one for the other is a
  // factor of a million million, in either direction, on a number that is
  // about to move money.
  async sendToken(customer: Customer, keyId: string, req: TokenTransferDto) {
    const requestId = req.idempotencyKey ?? uuidv4();

    if (!this.tokens) {
      throw new ServiceUnavailableException(
        'the token registry is not configured, so token transfers cannot be signed',
      );
    }

    const key = await this.loadSignableKey(keyId, customer.customer_id);
    if (key.blockchain === 'bitcoin' || key.blockchain === 'solana') {
      throw new BadRequestException(
        `key ${keyId} is a ${key.blockchain} key; this route sends ERC-20 tokens on EVM chains`,
      );
    }

    // Resolved before anything else: an unregistered or unverified token
    // must fail here, with an explanation, rather than after a signing
    // ceremony has run.
    const token = await this.tokens.requireTransactable(req.chainId, req.token);
    if (!token.contractAddress) {
      throw new BadRequestException(
        `${token.symbol} on chain ${req.chainId} has no contract address recorded`,
      );
    }

    let baseUnits: string;
    let data: string;
    try {
      baseUnits = parseUnits(req.amount, token.decimals);
      data = encodeTransfer(req.recipient, baseUnits);
    } catch (err) {
      if (err instanceof Erc20DecodeError) {
        throw new BadRequestException((err as Error).message);
      }
      throw err;
    }

    // An ERC-20 transfer is around 65,000 gas. Defaulting rather than
    // making the caller supply it: a caller reusing the 21,000 of a native
    // send produces a transaction that runs out of gas, fails, and still
    // pays the fee.
    const gasLimit = req.gasLimit ?? Number(process.env.ERC20_TRANSFER_GAS_LIMIT ?? 100_000);

    let built: BuiltTx;
    try {
      built = buildUnsignedTransaction({
        to: token.contractAddress,
        // Zero, always. The money is in the calldata; attaching ether to
        // an ERC-20 transfer sends it to the token contract, where most
        // contracts will reject it and some will simply keep it.
        value: '0',
        data,
        gasLimit,
        nonce: req.nonce,
        chainId: req.chainId,
        gasPrice: req.gasPrice,
        maxFeePerGas: req.maxFeePerGas,
        maxPriorityFeePerGas: req.maxPriorityFeePerGas,
      });
    } catch (err) {
      if (err instanceof TransactionBuildError) {
        throw new BadRequestException((err as Error).message);
      }
      throw err;
    }

    // The recipient and amount policy sees are the ones just encoded into
    // the bytes about to be hashed -- not the transaction's own to and
    // value, which are the token contract and zero.
    await this.enforcePolicy(customer, requestId, {
      to: req.recipient,
      value: '0',
      chainId: req.chainId,
      country: req.country,
      asset: token.symbol,
      assetAmount: baseUnits,
      assetDecimals: token.decimals,
      pegCurrency: token.pegCurrency ?? undefined,
      isAllowance: false,
    });

    const signed = await this.runSigningCeremony(
      key,
      keyId,
      customer.customer_id,
      requestId,
      built.signingHash,
    );

    let assembled: SignedTx;
    try {
      assembled = assembleSignedTransaction(built, signed.signature, key.address);
    } catch (err) {
      this.logger.error(
        `assembling the signed token transfer for key ${keyId} failed: ${(err as Error).message}`,
      );
      throw new ServiceUnavailableException((err as Error).message);
    }

    const intent: TransferIntent = {
      policyTo: req.recipient,
      asset: token.symbol,
      assetAmount: baseUnits,
      assetDecimals: token.decimals,
      pegCurrency: token.pegCurrency ?? undefined,
      effectiveTo: req.recipient,
      effectiveAmount: baseUnits,
      contractAddress: token.contractAddress,
      method: 'transfer',
    };
    await this.recordTransfer(customer, requestId, req.chainId, key.blockchain, {
      to: token.contractAddress,
      value: '0',
      data,
      gasLimit,
      gasPrice: req.gasPrice ?? null,
      nonce: req.nonce,
      signedTx: assembled.raw,
      txHash: assembled.hash,
      intent,
    });

    this.announce(customer.customer_id, 'signature.created', {
      key_id: keyId,
      request_id: requestId,
      asset: token.symbol,
      amount: req.amount,
      recipient: req.recipient,
    });

    return {
      request_id: requestId,
      key_id: keyId,
      from: assembled.from,
      token: {
        symbol: token.symbol,
        contract_address: token.contractAddress,
        decimals: token.decimals,
        peg_currency: token.pegCurrency,
      },
      recipient: req.recipient,
      // Both forms, deliberately. The decimal amount is what the customer
      // asked for and what their reconciliation will compare against; the
      // base units are what is actually in the signed bytes. Returning
      // only one leaves the other to be re-derived by whoever needs it,
      // with the decimals conversion done a second time somewhere else.
      amount: req.amount,
      amount_base_units: baseUnits,
      chain_id: req.chainId,
      nonce: req.nonce,
      signing_hash: built.signingHash,
      raw_transaction: assembled.raw,
      transaction_hash: assembled.hash,
      signature: signed.signature,
      parties: signed.parties,
      threshold: key.threshold,
      total_parties: signed.totalParties,
    };
  }

  // Records what was signed, so a later reader can answer "who received
  // how much of what" without an ABI decoder.
  //
  // Never allowed to fail the request. The signature exists and the
  // customer holds a transaction they can broadcast by the time this runs;
  // refusing to return it because an audit row did not insert would be a
  // worse outcome than an audit row that did not insert. Logged loudly
  // instead, because a gap here is a gap in a regulatory aggregate.
  private async recordTransfer(
    customer: Customer,
    requestId: string,
    chainId: number,
    blockchain: string,
    tx: {
      to: string;
      value: string;
      data: string | null;
      gasLimit?: number | null;
      gasPrice?: string | null;
      nonce?: number | null;
      signedTx: string;
      txHash: string;
      intent: TransferIntent;
    },
  ): Promise<void> {
    try {
      await this.postgres.recordTransfer({
        // request_id is a UUID column and an idempotency key is a free
        // string the customer chose. Deriving a stable UUID rather than
        // generating a fresh one: a retry with the same idempotency key
        // has to land on the same row, or one logical transfer appears
        // twice in the aggregate that decides whether a filing is due.
        rowId: stableUuid(requestId),
        requestId,
        customerId: customer.customer_id,
        chain: blockchain,
        to: tx.to,
        data: tx.data,
        value: tx.value,
        gasLimit: tx.gasLimit ?? null,
        gasPrice: tx.gasPrice ?? null,
        nonce: tx.nonce ?? null,
        signedTx: tx.signedTx,
        txHash: tx.txHash,
        status: 'signed',
        assetSymbol: tx.intent.asset,
        assetContract: tx.intent.contractAddress ?? null,
        assetDecimals: tx.intent.assetDecimals,
        // Copied onto the row rather than joined from the registry later:
        // a token's peg is a fact about the transfer at the time it
        // happened, and a later registry edit must not rewrite what a
        // historical filing was based on.
        assetPeg: tx.intent.pegCurrency ?? null,
        effectiveTo: tx.intent.effectiveTo,
        effectiveAmount: tx.intent.effectiveAmount,
      });
    } catch (err) {
      this.logger.error(
        `recording transfer ${requestId} for customer ${customer.customer_id} failed: ` +
          `${(err as Error).message}. The signature was returned; this transfer will be ` +
          'missing from regulatory aggregates until the row is reconciled.',
      );
    }
  }

  // Where to send money so this key can spend it.
  //
  // A custody platform that cannot answer this is not usable: a customer
  // has to be able to fund a key before they can move anything out of it.
  // For Bitcoin the answer is two addresses, not one -- the same key is
  // payable at a segwit and a legacy address, and both are spendable, so
  // publishing only one would leave the platform unable to explain a
  // deposit made to the other.
  async getDepositAddresses(customer: Customer, keyId: string) {
    const key = await this.postgres.getKey(keyId, customer.customer_id);
    if (!key) {
      throw new NotFoundException(`no key ${keyId}`);
    }
    if (!key.public_key) {
      throw new ConflictException(
        `key ${keyId} is ${key.status}; its addresses cannot be derived until its DKG ceremony completes`,
      );
    }

    if (key.blockchain !== 'bitcoin') {
      // Ethereum and every EVM chain share one address, already recorded
      // when the ceremony finished.
      return {
        key_id: keyId,
        blockchain: key.blockchain,
        addresses: { preferred: key.address },
      };
    }
    return this.bitcoinDepositAddresses(keyId, key);
  }

  // What a key holds, in each registered token as well as the native coin.
  //
  // Separate from getDepositAddresses because it is a different kind of
  // answer -- that one is derivable from the public key alone and always
  // succeeds, this one is a set of live chain reads, any of which can be
  // slow or fail. Conflating them would make "where do I deposit" depend
  // on an RPC endpoint being up.
  //
  // A customer holding two million USDC saw a zero balance before this
  // existed, because nothing in the platform had ever read an ERC-20
  // balance. That reads, to them, exactly like their money not being
  // there.
  async getBalances(customer: Customer, keyId: string, chainId: number) {
    const key = await this.postgres.getKey(keyId, customer.customer_id);
    if (!key) {
      throw new NotFoundException(`no key ${keyId}`);
    }
    if (!key.address) {
      throw new ConflictException(
        `key ${keyId} is ${key.status}; it has no address until its DKG ceremony completes`,
      );
    }
    if (!this.rpc || !this.tokens) {
      throw new ServiceUnavailableException(
        'balance reads need a token registry and a JSON-RPC endpoint, and one is not configured',
      );
    }
    if (!this.rpc.configured(chainId)) {
      throw new BadRequestException(
        `no JSON-RPC endpoint is configured for chain ${chainId}, so balances there cannot be read`,
      );
    }

    const tokens = (await this.tokens.list(chainId)).filter((t) => t.status === 'verified');

    // Read concurrently, and let one token's failure be that token's
    // failure. A single unresponsive contract must not blank out every
    // other balance -- a customer looking at an incomplete list with a
    // named error on one row can act on it; one looking at an error page
    // cannot tell whether their money is gone.
    const balances = await Promise.all(
      tokens.map(async (token) => {
        try {
          const raw = await this.rpc!.call(
            chainId,
            token.contractAddress!,
            encodeBalanceOf(key.address),
          );
          // An empty reply means the call reverted or the address holds no
          // contract. Reporting that as zero would be a lie with the same
          // shape as the truth.
          if (!raw || raw === '0x') {
            throw new Error('the contract returned no balance');
          }
          const baseUnits = BigInt(raw).toString(10);
          return {
            symbol: token.symbol,
            contract_address: token.contractAddress,
            decimals: token.decimals,
            peg_currency: token.pegCurrency,
            balance: formatUnits(baseUnits, token.decimals),
            balance_base_units: baseUnits,
          };
        } catch (err) {
          return {
            symbol: token.symbol,
            contract_address: token.contractAddress,
            decimals: token.decimals,
            peg_currency: token.pegCurrency,
            balance: null,
            balance_base_units: null,
            error: (err as Error).message,
          };
        }
      }),
    );

    let native: string | null = null;
    let nativeError: string | undefined;
    try {
      native = (await this.rpc.provider(chainId).getBalance(key.address)).toString();
    } catch (err) {
      nativeError = (err as Error).message;
    }

    return {
      key_id: keyId,
      chain_id: chainId,
      address: key.address,
      native: {
        balance_wei: native,
        balance: native === null ? null : formatUnits(native, 18),
        ...(nativeError ? { error: nativeError } : {}),
      },
      tokens: balances,
    };
  }

  private async bitcoinDepositAddresses(keyId: string, key: { public_key: string }) {

    const network = process.env.BITCOIN_NETWORK ?? 'mainnet';
    let derived;
    try {
      derived = await this.bitcoin.addresses(key.public_key, network);
    } catch (err) {
      throw this.translateSignerError(err, 'deriving the key\'s Bitcoin addresses');
    }
    return {
      key_id: keyId,
      blockchain: 'bitcoin',
      network,
      addresses: {
        preferred: derived.preferred,
        segwit: derived.segwit,
        legacy: derived.legacy,
      },
    };
  }

  // Sends Bitcoin from a threshold key.
  //
  // The Bitcoin equivalent of signTransaction, and it exists for the same
  // reason: until it did, the only way to spend Bitcoin was POST
  // :keyId/sign, where the customer computes their own sighash and the
  // platform signs a digest it cannot inspect. That route is gated per
  // tenant precisely because a digest is opaque to policy -- so Bitcoin was
  // either unavailable or governed by a policy decision about whatever the
  // caller claimed the digest meant.
  //
  // Here the platform selects the coins, computes the fee, builds the
  // transaction and hashes it. The destination and amount policy evaluates
  // are the destination and amount that end up in the bytes.
  //
  // One ceremony per input. That is unavoidable: each input is a separate
  // signature over a separate digest, and a threshold signature cannot be
  // batched. It is also why coin selection minimises the input count.
  async sendBitcoin(customer: Customer, keyId: string, req: BitcoinTransactionDto) {
    const requestId = req.idempotencyKey ?? uuidv4();

    const key = await this.loadSignableKey(keyId, customer.customer_id);
    if (key.blockchain !== 'bitcoin') {
      throw new BadRequestException(
        `key ${keyId} is a ${key.blockchain} key; this route spends Bitcoin`,
      );
    }
    if (!key.public_key) {
      throw new ConflictException(
        `key ${keyId} has no public key recorded, so its Bitcoin addresses cannot be derived`,
      );
    }

    const amount = Number(req.amount);
    if (!Number.isSafeInteger(amount)) {
      throw new BadRequestException('amount is too large to be a number of satoshis');
    }

    // Policy first, before any node work: a spend that policy refuses
    // should not cost a UTXO scan. chainId 0 because Bitcoin has no chain
    // id -- the field is EVM-shaped and the policy engine treats it as an
    // opaque discriminator.
    await this.enforcePolicy(customer, requestId, {
      to: req.destination,
      value: req.amount,
      chainId: 0,
      country: req.country,
    });

    const network = process.env.BITCOIN_NETWORK ?? 'mainnet';

    let prepared;
    try {
      prepared = await this.bitcoin.prepare({
        network,
        pubkey_hex: key.public_key,
        destination: req.destination,
        amount,
        fee_rate: req.feeRate,
        confirmation_target: req.confirmationTarget,
        change_address: req.changeAddress,
        min_confirmations: req.minConfirmations,
      });
    } catch (err) {
      throw this.translateSignerError(err, 'preparing the Bitcoin transaction');
    }

    this.logger.log(
      `key ${keyId}: spending ${prepared.selection.selected.length} input(s) ` +
        `totalling ${prepared.selection.total_in} sats, fee ${prepared.selection.fee} ` +
        `at ${prepared.selection.fee_rate} sat/vB`,
    );

    // Sequential rather than parallel. The parties run one ceremony at a
    // time, so firing several at once would queue them anyway while making
    // a partial failure much harder to reason about.
    const signatures: Array<{ r: string; s: string }> = [];
    const parties: number[][] = [];
    for (const [index, sighash] of prepared.plan.sighashes.entries()) {
      // A distinct request id per input: they are separate ceremonies, and
      // sharing an idempotency key across them would make the second one
      // return the first one's signature -- which would be a valid
      // signature over the wrong digest.
      const signed = await this.runSigningCeremony(
        key,
        keyId,
        customer.customer_id,
        `${requestId}:input-${index}`,
        sighash,
      );
      signatures.push(splitSignature(signed.signature));
      parties.push(signed.parties);
    }

    let finalized;
    try {
      finalized = await this.bitcoin.finalize({
        plan: prepared.plan,
        signatures,
        pubkey_hex: key.public_key,
        broadcast: req.broadcast ?? true,
      });
    } catch (err) {
      throw this.translateSignerError(err, 'assembling the Bitcoin transaction');
    }

    if (finalized.broadcast) {
      this.announce(customer.customer_id, 'transaction.broadcast', {
        key_id: keyId,
        blockchain: 'bitcoin',
        network,
        txid: finalized.txid,
        to: req.destination,
        amount: String(prepared.selection.amount),
        fee: String(prepared.selection.fee),
      });
    }

    return {
      request_id: requestId,
      key_id: keyId,
      network,
      from: prepared.addresses,
      to: req.destination,
      amount: String(prepared.selection.amount),
      fee: String(prepared.selection.fee),
      fee_rate: prepared.selection.fee_rate,
      change: String(prepared.selection.change),
      change_dropped_to_fee: prepared.selection.change_dropped_to_fee,
      virtual_size: prepared.selection.virtual_size,
      inputs: prepared.selection.selected.length,
      balance_before: String(prepared.balance),
      txid: finalized.txid,
      raw_transaction: finalized.raw_tx_hex,
      broadcast: finalized.broadcast,
      parties: parties[0] ?? [],
      threshold: key.threshold,
    };
  }

  // Maps a signer failure onto the right HTTP status.
  //
  // Worth doing carefully: "you do not have enough Bitcoin" and "the node
  // is unreachable" are both failures to send money, and only one of them
  // is the customer's to fix. Collapsing them into a 500 makes every
  // insufficient balance look like an outage.
  private translateSignerError(err: unknown, context: string): Error {
    if (!(err instanceof SignerError)) {
      return err as Error;
    }
    this.logger.error(`${context} failed (${err.status}): ${err.message}`);
    if (err.status === 400) {
      return new BadRequestException(err.message);
    }
    return new ServiceUnavailableException(`${context} failed: ${err.message}`);
  }

  // Loads a key the customer owns and that is in a state where signing is
  // meaningful. Tenant-scoped, so another customer's key is simply absent.
  private async loadSignableKey(keyId: string, customerId: string) {
    const key = await this.postgres.getKey(keyId, customerId);
    if (!key) {
      throw new NotFoundException(`no key ${keyId}`);
    }
    if (key.status !== 'active') {
      throw new ConflictException(
        `key ${keyId} is ${key.status}, not active; only an active key can sign`,
      );
    }
    return key;
  }

  // Fail-closed policy evaluation, shared by both signing routes so neither
  // can quietly end up ungated.
  private async enforcePolicy(
    customer: Customer,
    requestId: string,
    intent: {
      to: string;
      value: string;
      chainId: number;
      country?: string;
      asset?: string;
      assetAmount?: string;
      assetDecimals?: number;
      pegCurrency?: string;
      isAllowance?: boolean;
    },
  ) {
    const overrides = (customer.policies ?? {}) as Record<string, unknown>;
    const decision = await this.policy.evaluate({
      customerId: customer.customer_id,
      customerTier: customer.tier,
      to: intent.to,
      value: intent.value,
      asset: intent.asset,
      assetAmount: intent.assetAmount,
      assetDecimals: intent.assetDecimals,
      pegCurrency: intent.pegCurrency,
      isAllowance: intent.isAllowance,
      // A number, not key.blockchain (a name like "ethereum"):
      // policy-service's PolicyRequest.ChainID is an int and rejects the
      // name with 400.
      chainId: intent.chainId,
      whitelist: overrides.whitelist as string[] | undefined,
      blockedCountries: overrides.blockedCountries as string[] | undefined,
      country: intent.country,
    });
    if (!decision.approved) {
      throw new ForbiddenException({
        error: 'policy denied',
        denials: decision.denials,
        requiresApproval: decision.requiresApproval,
        requestId,
      });
    }
  }

  // Asks every party whether it is up, and returns the ids that answered,
  // in id order.
  //
  // Over the plaintext liveness port, not the mTLS one: this service holds
  // no client certificate, and the health endpoint deliberately serves
  // nothing but GET /health (see services/mpc-party/main.go).
  //
  // Probed in parallel with a short timeout, because this sits directly in
  // front of a signing request. A party that cannot answer a health check
  // within a second is not one to hand a signing ceremony to; the timeout
  // is the point, not an implementation detail.
  private async healthyParties(partyIds: number[]): Promise<number[]> {
    const template =
      process.env.MPC_PARTY_HEALTH_TEMPLATE ?? 'http://party-{id}:7000/health';
    const timeoutMs = Number(process.env.MPC_PARTY_HEALTH_TIMEOUT_MS ?? 1000);

    const results = await Promise.all(
      partyIds.map(async (id) => {
        const url = template.replace('{id}', String(id));
        try {
          const res = await fetch(url, {
            signal: AbortSignal.timeout(timeoutMs),
          });
          return res.ok ? id : null;
        } catch {
          return null;
        }
      }),
    );
    const up = results.filter((id): id is number => id !== null);
    if (up.length < partyIds.length) {
      const down = partyIds.filter((id) => !up.includes(id));
      this.logger.warn(
        `MPC parties not reachable: ${down.join(', ')}; ` +
          `choosing a signing committee from ${up.join(', ')}`,
      );
    }
    return up;
  }

  // Resolves the key's ceremony, picks a threshold-sized committee, and
  // runs the signing workflow over `messageHash`.
  private async runSigningCeremony(
    key: { threshold: number; blockchain: string; address: string },
    keyId: string,
    customerId: string,
    requestId: string,
    messageHash: string,
  ): Promise<{ signature: string; parties: number[]; totalParties: number }> {
    // Shares are addressed by ceremony, not by key -- each party sealed its
    // share under .../party-N/<ceremony-id> -- so signing means resolving
    // which completed ceremony produced this key. customerId is passed
    // explicitly rather than read off the key row: the lookup is
    // tenant-scoped, and a silently-empty customer id would return no
    // ceremony and surface as a confusing "shares do not exist yet".
    const ceremony = await this.postgres.getCompletedCeremonyForKey(keyId, customerId);
    if (!ceremony) {
      throw new ConflictException(
        `key ${keyId} has no completed DKG ceremony; its shares do not exist yet`,
      );
    }

    // threshold signers, not all total_parties: signing with the whole
    // committee would make an n-of-n key out of a k-of-n one.
    const signerCount = Math.min(key.threshold, ceremony.total_parties);
    const { partyIds, partyEndpoints } = derivePartyEndpoints(ceremony.total_parties);

    // Chosen from the parties that are actually up.
    //
    // This used to be partyIds.slice(0, signerCount) -- always parties 1
    // and 2 for a 2-of-3 key. That reduces a k-of-n key to a specific
    // k-of-k: lose party 1 and every signature fails, even though party 3
    // is healthy and holds a perfectly good share. The whole availability
    // argument for threshold signing is that it survives losing a party,
    // and a fixed committee is exactly the thing that makes it not.
    //
    // Found by draining a node. See infrastructure/kind/node-failure-drill.sh.
    const healthy = await this.healthyParties(partyIds);
    if (healthy.length < signerCount) {
      throw new ServiceUnavailableException(
        `only ${healthy.length} of ${ceremony.total_parties} MPC parties are reachable; ` +
          `${signerCount} are required to sign with this key`,
      );
    }
    const parties = healthy.slice(0, signerCount);
    const endpointOf = (id: number) => partyEndpoints[partyIds.indexOf(id)];

    this.logger.log(
      `threshold signing with key ${keyId} (ceremony ${ceremony.ceremony_id}, ` +
        `${signerCount} of ${ceremony.total_parties} parties)`,
    );

    // A repeat of a request that already produced a signature.
    //
    // Every signing route has accepted an idempotencyKey since it was
    // written and none of them consulted it, so a client retrying a
    // timed-out call ran a second threshold ceremony -- and for Bitcoin
    // broadcast a second transaction. The schema has carried
    // UNIQUE (customer_id, idempotency_key) the whole time; this is the
    // code that finally uses it.
    //
    // Replaying rather than refusing is the useful behaviour: a retry
    // after a network timeout should get the answer it missed. The digest
    // is part of the record, so a key reused for a *different* message is
    // caught below rather than silently returning the wrong signature.
    // requestId is the caller's idempotency key, which is any string --
    // for a Bitcoin spend it is "<key>:input-2", one per ceremony. The
    // row's own request_id is a UUID column and a different thing, so the
    // two are kept apart rather than one being forced into the other.
    const existing = await this.postgres.findSigningRequestByIdempotencyKey(
      customerId,
      requestId,
    );
    if (existing) {
      if (existing.transaction_hash !== messageHash) {
        throw new ConflictException(
          `idempotency key ${requestId} was already used for a different message; ` +
            'reusing it would return a signature over something else',
        );
      }
      if (existing.status === 'completed' && existing.signature) {
        this.logger.log(`replaying the recorded signature for request ${requestId}`);
        return {
          signature: existing.signature.toString('hex'),
          parties: existing.signing_parties ?? [],
          totalParties: ceremony.total_parties,
        };
      }
      if (existing.status === 'in_progress') {
        throw new ConflictException(
          `a request with idempotency key ${requestId} is still running`,
        );
      }
      // A failed request may be retried, but not under the same primary
      // key -- the row already exists, so the retry proceeds without
      // recording a second one.
    }

    // Recorded before the ceremony, not after. A ceremony that starts and
    // never returns is exactly the event an audit trail has to contain,
    // and a row written only on success would omit it.
    const startedAt = Date.now();
    const rowId = existing?.request_id ?? uuidv4();
    if (!existing) {
      try {
        await this.postgres.createSigningRequest({
          requestId: rowId,
          customerId,
          keyId,
          blockchain: key.blockchain,
          transactionHash: messageHash,
          transactionData: Buffer.from(messageHash, 'hex'),
          idempotencyKey: requestId,
          workflowId: `threshold-sign-${requestId}`,
        });
      } catch (err) {
        // 23505 = unique_violation: another request with this idempotency
        // key was recorded between the lookup above and this insert.
        if ((err as { code?: string }).code === '23505') {
          throw new ConflictException(
            `a request with idempotency key ${requestId} is already running`,
          );
        }
        throw err;
      }
    }

    const recordFailure = async (reason: string) => {
      await this.postgres
        .failSigningRequest(rowId, customerId, reason, Date.now() - startedAt)
        .catch((err) =>
          this.logger.error(`could not record the failed signing request: ${err.message}`),
        );
    };

    let result: { status: string; signature?: string; error?: string };
    try {
      result = await this.temporal.signWithThreshold({
        requestId,
        ceremonyId: ceremony.ceremony_id,
        message: messageHash,
        partyIds: parties,
        // Indexed by the chosen ids, not sliced off the front: with a
        // committee of [1, 3] the endpoints must be party-1's and
        // party-3's, and slicing would have sent party-2's.
        partyEndpoints: parties.map(endpointOf),
        chainId: key.blockchain,
      });
    } catch (err) {
      this.logger.error(
        `threshold signing workflow failed for key ${keyId}: ${(err as Error).message}`,
      );
      await recordFailure((err as Error).message);
      throw new ServiceUnavailableException('threshold signing is unavailable');
    }

    // The workflow reports a failed ceremony as a *result*, not an
    // exception (see ThresholdSigningWorkflow), so a caller that only
    // checked for a thrown error would treat a failure as success and
    // return no signature with a 200.
    if (result.status !== 'completed' || !result.signature) {
      const reason = result.error ?? result.status;
      await recordFailure(reason);
      throw new ServiceUnavailableException(`threshold signing did not complete: ${reason}`);
    }

    // Best-effort: the signature exists and the caller is entitled to it,
    // so a failure to write the audit row must not turn a successful
    // signature into an error. It is logged loudly instead, because a
    // signature that was produced and not recorded is a real problem --
    // just not the caller's.
    await this.postgres
      .completeSigningRequest(rowId, customerId, {
        signature: Buffer.from(result.signature, 'hex'),
        latencyMs: Date.now() - startedAt,
        parties,
      })
      .catch((err) =>
        this.logger.error(
          `signed with key ${keyId} but could not record request ${requestId}: ${err.message}`,
        ),
      );

    // Emitted here rather than in each route, for the same reason the
    // audit row is written here: three routes produce signatures and a
    // fourth will, and one of them would eventually forget.
    this.announce(customerId, 'signature.created', {
      key_id: keyId,
      request_id: rowId,
      idempotency_key: requestId,
      message: messageHash,
      parties,
      threshold: key.threshold,
    });

    return {
      signature: result.signature,
      parties,
      totalParties: ceremony.total_parties,
    };
  }

  async listKeys(customerId: string) {
    return this.postgres.listKeys(customerId);
  }

  async getKey(keyId: string, customerId: string) {
    return this.postgres.getKey(keyId, customerId);
  }

  async getKeyDetails(keyId: string, customerId: string) {
    const key = await this.postgres.getKey(keyId, customerId);
    if (!key) {
      return null;
    }

    // Enrich with additional details
    return {
      ...key,
      created_ceremonies: await this.postgres.getCeremoniesForKey(keyId, customerId),
      signing_requests: await this.postgres.getSigningRequestsForKey(keyId, customerId),
      total_signatures: await this.postgres.countSignaturesForKey(keyId, customerId),
    };
  }

  async getShareStatus(keyId: string, customerId: string) {
    const key = await this.postgres.getKey(keyId, customerId);
    if (!key) {
      return null;
    }

    // Get share distribution status from Vault
    const shares = await this.postgres.getKeyShares(keyId, customerId);

    return {
      key_id: keyId,
      status: key.status,
      threshold: key.threshold,
      total_parties: key.total_parties,
      shares_available: shares.length,
      share_details: shares.map((share) => ({
        party_id: share.party_id,
        status: share.status,
        backed_up: share.backed_up_at !== null,
      })),
    };
  }
}
