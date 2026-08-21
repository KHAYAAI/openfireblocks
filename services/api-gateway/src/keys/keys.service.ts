import { Injectable, Logger } from '@nestjs/common';
import { PostgresService } from '../database/postgres.service';
import { CreateKeyRequest } from './dto/create-key.dto';
import { Customer } from '../customers/customer.service';
import { v4 as uuidv4 } from 'uuid';

@Injectable()
export class KeysService {
  private readonly logger = new Logger(KeysService.name);

  constructor(private readonly postgres: PostgresService) {}

  async createKey(customer: Customer, req: CreateKeyRequest) {
    const keyId = uuidv4();
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

    // Start DKG ceremony via Temporal workflow
    // TODO: Trigger DKG ceremony workflow

    return {
      id: keyId,
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
