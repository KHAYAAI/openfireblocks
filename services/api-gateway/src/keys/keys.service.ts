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

  constructor(
    private readonly postgres: PostgresService,
    private readonly temporal: KeysTemporalService,
    private readonly policy: PolicyService,
  ) {}

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
    // committee would make an n-of-n key out of a k-of-n one, and the point
    // of the threshold is that it tolerates absent parties. The first
    // threshold by id is a deterministic choice, not a load-balancing one.
    const signerCount = Math.min(key.threshold, ceremony.total_parties);
    const { partyIds, partyEndpoints } = derivePartyEndpoints(ceremony.total_parties);
    const parties = partyIds.slice(0, signerCount);

    this.logger.log(
      `threshold signing with key ${keyId} (ceremony ${ceremony.ceremony_id}, ` +
        `${signerCount} of ${ceremony.total_parties} parties)`,
    );

    let result: { status: string; signature?: string; error?: string };
    try {
      result = await this.temporal.signWithThreshold({
        requestId,
        ceremonyId: ceremony.ceremony_id,
        message: messageHash,
        partyIds: parties,
        partyEndpoints: partyEndpoints.slice(0, signerCount),
        chainId: key.blockchain,
      });
    } catch (err) {
      this.logger.error(
        `threshold signing workflow failed for key ${keyId}: ${(err as Error).message}`,
      );
      throw new ServiceUnavailableException('threshold signing is unavailable');
    }

    // The workflow reports a failed ceremony as a *result*, not an
    // exception (see ThresholdSigningWorkflow), so a caller that only
    // checked for a thrown error would treat a failure as success and
    // return no signature with a 200.
    if (result.status !== 'completed' || !result.signature) {
      throw new ServiceUnavailableException(
        `threshold signing did not complete: ${result.error ?? result.status}`,
      );
    }

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
