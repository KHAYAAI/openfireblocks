import { Injectable } from '@nestjs/common';
import type { ApprovalRequestView } from './approvals.service';

// What happened to the transfer an approval was for, as the decision handler
// reports it.
export interface TransferExecution {
  status: 'awaiting_approval' | 'executing' | 'completed' | 'failed' | 'rejected' | 'expired';
  result?: Record<string, unknown> | null;
  error?: string | null;
}

export interface NativeApprovalHandler {
  // Called after a decision on a transfer that the gateway signs itself. It
  // executes the transfer if the request just reached quorum, records a
  // rejection, and otherwise does nothing. Must not throw for an execution
  // failure: the decision stands, and the failure is reported in the result.
  onDecision(customerId: string, request: ApprovalRequestView): Promise<TransferExecution | undefined>;
  // Runs a transfer again after a failed execution.
  retry(customerId: string, approvalId: string): Promise<TransferExecution>;
}

// A seam, not an abstraction for its own sake. Executing a transfer needs the
// keys module, and the keys module already depends (through the Travel Rule
// module) on this one; importing it here would be a cycle. The module that
// owns execution registers itself at start-up instead.
@Injectable()
export class NativeApprovalHooks {
  private handler: NativeApprovalHandler | null = null;

  register(handler: NativeApprovalHandler) {
    this.handler = handler;
  }

  get registered(): NativeApprovalHandler | null {
    return this.handler;
  }
}
