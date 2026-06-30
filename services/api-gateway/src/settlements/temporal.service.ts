import {
  Injectable,
  Logger,
  NotFoundException,
  OnModuleDestroy,
  ServiceUnavailableException,
} from '@nestjs/common';
import { Connection, WorkflowClient } from '@temporalio/client';
import { v4 as uuid } from 'uuid';
import { SignRequestDto } from '../sign/dto/sign-request.dto';

const TASK_QUEUE = 'transaction-settlement';
const WORKFLOW_TYPE = 'TransactionSettlementWorkflow';

export interface SettlementStatus {
  workflowId: string;
  status: string;
  result?: unknown;
}

// Starts and inspects durable settlement workflows on the Temporal worker.
//
// The connection is lazy and optional: if TEMPORAL_HOSTPORT is unset the
// /settlements endpoints return 503, so the gateway runs fine without Temporal.
// Workflow ids are namespaced by tenant so a customer can only see their own.
@Injectable()
export class TemporalService implements OnModuleDestroy {
  private readonly logger = new Logger(TemporalService.name);
  private connection: Connection | null = null;
  private client: WorkflowClient | null = null;
  private connecting: Promise<WorkflowClient> | null = null;

  private async getClient(): Promise<WorkflowClient> {
    if (this.client) return this.client;

    const address = process.env.TEMPORAL_HOSTPORT;
    if (!address) {
      throw new ServiceUnavailableException(
        'Temporal not configured (set TEMPORAL_HOSTPORT) — durable settlements unavailable',
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

  private prefix(customerId: string): string {
    return `settlement-${customerId}-`;
  }

  // Starts a settlement workflow for a tenant; returns its workflow id.
  async start(
    customerId: string,
    tier: string,
    req: SignRequestDto,
  ): Promise<{ workflowId: string }> {
    const client = await this.getClient();
    const workflowId = `${this.prefix(customerId)}${uuid()}`;

    await client.start(WORKFLOW_TYPE, {
      taskQueue: TASK_QUEUE,
      workflowId,
      args: [
        {
          customerId,
          customerTier: tier,
          chainId: req.chainId,
          to: req.to,
          data: req.data ?? '',
          value: req.value ?? '0',
          gasLimit: req.gasLimit,
          gasPrice: req.gasPrice ?? '',
          nonce: req.nonce,
          country: req.country ?? '',
        },
      ],
    });
    return { workflowId };
  }

  // Returns workflow status (and result if completed), scoped to the tenant.
  async status(
    customerId: string,
    workflowId: string,
  ): Promise<SettlementStatus> {
    this.assertOwned(customerId, workflowId);
    const client = await this.getClient();
    const handle = client.getHandle(workflowId);
    const desc = await handle.describe();

    const out: SettlementStatus = {
      workflowId,
      status: desc.status.name,
    };
    if (desc.status.name === 'COMPLETED') {
      out.result = await handle.result();
    }
    return out;
  }

  // Sends the human-approval signal for a high-value settlement.
  async approve(
    customerId: string,
    workflowId: string,
    approved: boolean,
  ): Promise<void> {
    this.assertOwned(customerId, workflowId);
    const client = await this.getClient();
    await client.getHandle(workflowId).signal('approval', approved);
  }

  private assertOwned(customerId: string, workflowId: string) {
    if (!workflowId.startsWith(this.prefix(customerId))) {
      // Treat another tenant's workflow as not found.
      throw new NotFoundException('settlement not found');
    }
  }

  async onModuleDestroy() {
    await this.connection?.close().catch(() => undefined);
  }
}
