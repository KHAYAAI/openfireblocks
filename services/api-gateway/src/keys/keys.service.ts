import { Injectable, Logger, ServiceUnavailableException } from '@nestjs/common';
import { PostgresService } from '../database/postgres.service';
import { KeysTemporalService } from './keys-temporal.service';
import { CreateKeyRequest } from './dto/create-key.dto';
import { Customer } from '../customers/customer.service';
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

    await this.postgres.createKey(key);

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
