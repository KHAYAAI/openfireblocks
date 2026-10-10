import { Injectable } from '@nestjs/common';

// A transfer out of another custodian, as it is asked for and stored (and approved).
export interface CustodianTransferRequest {
  accountId: string;
  destination: string;
  asset: string;
  amount: string; // base units
  decimals: number;
  blockchain: string;
  memo?: string;
}

// What the transfer path needs from whoever knows the other custodians. A seam like
// NativeApprovalHooks: the custody module needs the transfer path (to ask for
// approval) and the transfer path needs the custody module (to execute once
// approved), so the module that owns execution registers itself at start-up.
export interface CustodyExecutor {
  execute(customerId: string, custodianId: string, req: CustodianTransferRequest, idempotencyKey: string): Promise<Record<string, unknown>>;
  describe(customerId: string, custodianId: string, accountId: string): Promise<{ custodian: string; account: string }>;
}

@Injectable()
export class CustodyExecutors {
  private executor: CustodyExecutor | null = null;
  register(e: CustodyExecutor) { this.executor = e; }
  get(): CustodyExecutor | null { return this.executor; }
}
