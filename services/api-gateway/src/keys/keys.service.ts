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
import { v4 as uuidv4 } from 'uuid';

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
  ) {}

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

    // The same to/value/chainId that went into the bytes just hashed.
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

    return {
      request_id: requestId,
      key_id: keyId,
      from: assembled.from,
      to: req.to,
      value: req.value,
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
    intent: { to: string; value: string; chainId: number; country?: string },
  ) {
    const overrides = (customer.policies ?? {}) as Record<string, unknown>;
    const decision = await this.policy.evaluate({
      customerId: customer.customer_id,
      customerTier: customer.tier,
      to: intent.to,
      value: intent.value,
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
