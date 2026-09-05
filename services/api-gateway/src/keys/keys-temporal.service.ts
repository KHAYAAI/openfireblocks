import {
  Injectable,
  Logger,
  OnModuleDestroy,
  ServiceUnavailableException,
} from '@nestjs/common';
import { Connection, WorkflowClient } from '@temporalio/client';

const TASK_QUEUE = 'transaction-settlement';
const WORKFLOW_TYPE = 'ProvisionKeyWorkflow';

// Starts services/temporal-worker/workflows/provision_key.go's
// ProvisionKeyWorkflow -- this is what closes KeysService.createKey's
// prior `// TODO: Trigger DKG ceremony workflow`. Every real DKG ceremony
// in this codebase, until this, was only reachable by constructing its
// request struct directly (a test, or ceremony-orchestrator's own
// separate, disconnected /ceremonies API writing to a `ceremonies` table
// that doesn't exist in any migration -- see keys.service.ts's doc
// comment); this is the first path that starts one from where a real
// customer's request actually enters the system.
//
// Same lazy/optional-connection shape as settlements/temporal.service.ts:
// if TEMPORAL_HOSTPORT is unset, key creation fails closed (503) rather
// than leaving a pending_dkg row with a ceremony nobody will ever run.
@Injectable()
export class KeysTemporalService implements OnModuleDestroy {
  private readonly logger = new Logger(KeysTemporalService.name);
  private connection: Connection | null = null;
  private client: WorkflowClient | null = null;
  private connecting: Promise<WorkflowClient> | null = null;

  private async getClient(): Promise<WorkflowClient> {
    if (this.client) return this.client;

    const address = process.env.TEMPORAL_HOSTPORT;
    if (!address) {
      throw new ServiceUnavailableException(
        'Temporal not configured (set TEMPORAL_HOSTPORT) — key provisioning unavailable',
      );
    }

    if (!this.connecting) {
      this.connecting = (async () => {
        this.connection = await Connection.connect({ address });
        const client = new WorkflowClient({
          connection: this.connection,
          namespace: process.env.TEMPORAL_NAMESPACE ?? 'default',
        });
        this.client = client;
        this.logger.log(`connected to Temporal at ${address}`);
        return client;
      })().catch((err) => {
        this.connecting = null; // allow retry on next request
        throw new ServiceUnavailableException(
          `Temporal connection failed: ${(err as Error).message}`,
        );
      });
    }
    return this.connecting;
  }

  // Starts ProvisionKeyWorkflow for a brand-new key. workflowId is
  // deterministic (provision-key-<keyId>) so a duplicate call for the
  // same key_pairs row is idempotent at the Temporal level, not just the
  // DB level.
  async start(params: {
    keyId: string;
    ceremonyId: string;
    customerId: string;
    chainId: string;
    n: number;
    k: number;
    partyIds: number[];
    partyEndpoints: string[];
  }): Promise<{ workflowId: string }> {
    const client = await this.getClient();
    const workflowId = `provision-key-${params.keyId}`;

    await client.start(WORKFLOW_TYPE, {
      taskQueue: TASK_QUEUE,
      workflowId,
      args: [
        {
          ceremony: {
            customerId: params.customerId,
            ceremonyId: params.ceremonyId,
            chainId: params.chainId,
            n: params.n,
            k: params.k,
            partyIds: params.partyIds,
            partyEndpoints: params.partyEndpoints,
          },
          keyId: params.keyId,
        },
      ],
    });
    return { workflowId };
  }

  async onModuleDestroy() {
    await this.connection?.close().catch(() => undefined);
  }
}
