import { BadRequestException, Injectable, Logger, OnModuleInit, ServiceUnavailableException } from '@nestjs/common';
import { v4 as uuidv4 } from 'uuid';
import { ApprovalsService, ApprovalRequestView, TransferKind } from '../approvals/approvals.service';
import { NativeApprovalHandler, NativeApprovalHooks, TransferExecution } from '../approvals/native-approval-hooks';
import { Customer, CustomerService } from '../customers/customer.service';
import { KeysService, toPolicyUnits } from '../keys/keys.service';
import { ApprovalRequiredException } from '../keys/approval-required.exception';
import { BitcoinTransactionDto } from '../keys/dto/bitcoin-transaction.dto';
import { SolanaTransactionDto } from '../keys/dto/solana-transaction.dto';
import { CosmosTransactionDto } from '../keys/dto/cosmos-transaction.dto';
import { EvmRpcService } from '../tokens/evm-rpc.service';

// A transfer from an EVM threshold key, as a person asks for it: where, and
// how much. Nonce and fees are the platform's to work out, at the moment the
// transfer actually runs -- possibly hours after it was asked for.
export interface EvmTransferRequest {
  chainId: number;
  destination: string;
  amount: string; // wei, base-10
  country?: string;
  travelRule?: Record<string, unknown>;
  idempotencyKey?: string;
}

type AnyRequest = BitcoinTransactionDto | SolanaTransactionDto | CosmosTransactionDto | EvmTransferRequest;

export interface Initiator {
  userId: string | null; // null for an API key: there is no person to exclude
  label: string;
}

export type SubmitResult =
  | { status: 'completed'; result: Record<string, unknown> }
  | { status: 'pending_approval'; approvalId: string; expiresAt: string; requiredApprovals: number; reasons: string[] };

const DISPLAY: Record<TransferKind, { asset: string; decimals: number }> = {
  bitcoin: { asset: 'BTC', decimals: 8 },
  solana: { asset: 'SOL', decimals: 9 },
  cosmos: { asset: 'ATOM', decimals: 6 },
  evm: { asset: 'ETH', decimals: 18 },
};

// The one place a transfer from a threshold key is started.
//
// It runs the transfer straight away when policy lets it. When policy says
// people must sign off, it parks the request, opens an approval for the
// organisation's approvers, and runs it only when the approvals reach quorum.
// What runs is exactly what was asked: the request is stored unchanged and
// the database refuses to alter it.
//
// Lives outside the keys module because it needs both that module and the
// approvals module, and those two cannot import each other.
@Injectable()
export class TransfersService implements OnModuleInit, NativeApprovalHandler {
  private readonly logger = new Logger(TransfersService.name);

  constructor(
    private readonly approvals: ApprovalsService,
    private readonly hooks: NativeApprovalHooks,
    private readonly keys: KeysService,
    private readonly customers: CustomerService,
    private readonly rpc: EvmRpcService,
  ) {}

  onModuleInit() {
    this.hooks.register(this);
  }

  async submit(customer: Customer, keyId: string, kind: TransferKind, req: AnyRequest, initiator: Initiator): Promise<SubmitResult> {
    try {
      const result = await this.run(kind, customer, keyId, req, false, undefined);
      return { status: 'completed', result };
    } catch (err) {
      if (!(err instanceof ApprovalRequiredException)) throw err;
      return this.park(customer, keyId, kind, req, initiator, err.reasons);
    }
  }

  private async park(customer: Customer, keyId: string, kind: TransferKind, req: AnyRequest, initiator: Initiator, reasons: string[]): Promise<SubmitResult> {
    const { asset, decimals } = DISPLAY[kind];
    const amount = (req as { amount: string }).amount;
    const to = (req as { destination: string }).destination;

    // Before anyone is asked: information the transfer will need at the time
    // it runs. Approvers must not approve something that then fails for a
    // missing Travel Rule field.
    this.keys.assertTravelRuleComplete(
      { asset: 'NATIVE', amount: toPolicyUnits(amount, decimals), decimals: 18 },
      (req as { travelRule?: never }).travelRule,
    );

    const key = await this.keys.getKey(keyId, customer.customer_id);
    const policy = await this.approvals.getPolicy(customer.customer_id);
    // idempotencyKey is dropped: execution uses the approval's own.
    const { idempotencyKey: _ignored, ...request } = req as unknown as Record<string, unknown>;
    const opened = await this.approvals.openTransferRequest({
      customerId: customer.customer_id,
      initiator,
      requiredApprovals: policy.requiredApprovals,
      windowMinutes: policy.windowMinutes,
      keyId,
      kind,
      request,
      // What the approver reads. Everything an approver needs to judge the
      // transfer is here; nothing they are shown is something other than what
      // will run.
      summary: {
        kind: 'native-transfer',
        blockchain: kind,
        keyId,
        keyName: key?.name ?? null,
        to,
        asset,
        decimals,
        amount,
        // 18-decimal equivalent, for readers that only know this field.
        valueWei: toPolicyUnits(amount, decimals),
        chainId: (req as EvmTransferRequest).chainId ?? null,
        data: '',
        memo: (req as CosmosTransactionDto).memo ?? null,
        reasons,
      },
    });
    return { status: 'pending_approval', approvalId: opened.approvalId, expiresAt: opened.expiresAt, requiredApprovals: policy.requiredApprovals, reasons };
  }

  // ---- after a decision ---------------------------------------------------

  async onDecision(customerId: string, request: ApprovalRequestView): Promise<TransferExecution | undefined> {
    const pending = await this.approvals.pendingTransfer(customerId, request.approvalId);
    if (!pending) return undefined;
    if (request.status === 'rejected') {
      await this.approvals.transitionTransfer(customerId, request.approvalId, ['awaiting_approval'], 'rejected');
      return { status: 'rejected' };
    }
    if (request.status === 'expired') {
      await this.approvals.transitionTransfer(customerId, request.approvalId, ['awaiting_approval'], 'expired');
      return { status: 'expired' };
    }
    if (request.status !== 'approved') return { status: 'awaiting_approval' };
    return this.execute(customerId, request.approvalId, ['awaiting_approval']);
  }

  async retry(customerId: string, approvalId: string): Promise<TransferExecution> {
    const pending = await this.approvals.pendingTransfer(customerId, approvalId);
    if (!pending) throw new BadRequestException('no pending transfer for this approval');
    if (pending.status !== 'failed') {
      return { status: pending.status, result: pending.result, error: pending.error };
    }
    return this.execute(customerId, approvalId, ['failed']);
  }

  private async execute(customerId: string, approvalId: string, from: string[]): Promise<TransferExecution> {
    // Taking "executing" is the claim. The database lets one caller have it
    // (and only for an approved request), so a decision delivered twice, or a
    // retry while a run is in flight, cannot send the transfer twice.
    const claimed = await this.approvals.transitionTransfer(customerId, approvalId, from, 'executing');
    if (!claimed) {
      const now = await this.approvals.pendingTransfer(customerId, approvalId);
      return { status: now?.status ?? 'awaiting_approval', result: now?.result, error: now?.error };
    }
    const pending = (await this.approvals.pendingTransfer(customerId, approvalId))!;
    try {
      const customer = await this.customers.getByCustomerId(customerId);
      // The approval's own id, so a run repeated after a crash replays the
      // recorded signature instead of signing a second transaction.
      const key = `approval:${approvalId}`;
      const result = await this.run(
        pending.kind, customer, pending.keyId, { ...pending.request, idempotencyKey: key } as AnyRequest, true,
        { prepared: pending.prepared ?? undefined, save: (p) => this.approvals.transitionTransfer(customerId, approvalId, ['executing'], 'executing', { prepared: p }) },
      );
      await this.approvals.transitionTransfer(customerId, approvalId, ['executing'], 'completed', { result });
      return { status: 'completed', result };
    } catch (err) {
      const message = errorText(err);
      this.logger.error(`executing approved transfer ${approvalId} failed: ${message}`);
      await this.approvals.transitionTransfer(customerId, approvalId, ['executing'], 'failed', { error: message });
      return { status: 'failed', error: message };
    }
  }

  // ---- running a transfer -------------------------------------------------

  private async run(
    kind: TransferKind,
    customer: Customer,
    keyId: string,
    req: AnyRequest,
    approvalGranted: boolean,
    ctx: { prepared?: Record<string, any>; save?: (p: Record<string, unknown>) => Promise<boolean> } | undefined,
  ): Promise<Record<string, unknown>> {
    switch (kind) {
      case 'bitcoin':
        return (await this.keys.sendBitcoin(customer, keyId, req as BitcoinTransactionDto, { approvalGranted })) as Record<string, unknown>;
      case 'solana':
        return (await this.keys.sendSolana(customer, keyId, req as SolanaTransactionDto, { approvalGranted })) as Record<string, unknown>;
      case 'cosmos':
        return (await this.keys.sendCosmos(customer, keyId, req as CosmosTransactionDto, { approvalGranted })) as Record<string, unknown>;
      case 'evm':
        return this.runEvm(customer, keyId, req as EvmTransferRequest, approvalGranted, ctx);
    }
  }

  // Nonce and fees are read when the transfer runs, not when it was asked for.
  // An approval can take hours; a nonce chosen at request time would be stale,
  // and fees from then wrong. Once chosen they are saved, so that a retry after
  // a failure signs the same transaction rather than a different one under the
  // same idempotency key.
  private async runEvm(
    customer: Customer,
    keyId: string,
    req: EvmTransferRequest,
    approvalGranted: boolean,
    ctx: { prepared?: Record<string, any>; save?: (p: Record<string, unknown>) => Promise<boolean> } | undefined,
  ): Promise<Record<string, unknown>> {
    if (!this.rpc.configured(req.chainId)) {
      throw new ServiceUnavailableException(`no JSON-RPC endpoint is configured for chain ${req.chainId} (EVM_RPC_${req.chainId}), so its nonce and fees cannot be read`);
    }
    const key = await this.keys.getKey(keyId, customer.customer_id);
    if (!key?.address) throw new BadRequestException(`key ${keyId} has no address yet`);
    const provider = this.rpc.provider(req.chainId);

    let prepared = ctx?.prepared;
    if (!prepared) {
      const [nonce, fee, estimate] = await Promise.all([
        provider.getTransactionCount(key.address, 'pending'),
        provider.getFeeData(),
        provider.estimateGas({ from: key.address, to: req.destination, value: BigInt(req.amount) }),
      ]);
      prepared = {
        nonce,
        // 20% headroom on the estimate; a plain transfer is 21000 exactly.
        gasLimit: Number((estimate * 12n) / 10n),
        ...(fee.maxFeePerGas != null && fee.maxPriorityFeePerGas != null
          ? { maxFeePerGas: fee.maxFeePerGas.toString(), maxPriorityFeePerGas: fee.maxPriorityFeePerGas.toString() }
          : { gasPrice: (fee.gasPrice ?? 0n).toString() }),
      };
      await ctx?.save?.(prepared);
    }

    const signed = await this.keys.signTransaction(
      customer, keyId,
      {
        to: req.destination, value: req.amount, chainId: req.chainId,
        nonce: prepared.nonce, gasLimit: Math.max(21000, prepared.gasLimit),
        maxFeePerGas: prepared.maxFeePerGas, maxPriorityFeePerGas: prepared.maxPriorityFeePerGas, gasPrice: prepared.gasPrice,
        country: req.country, travelRule: req.travelRule as never, idempotencyKey: req.idempotencyKey ?? uuidv4(),
      } as never,
      { approvalGranted },
    );

    // Signing alone is not sending. Broadcast, so the console's "send" means
    // what it says.
    const sent = await provider.broadcastTransaction(signed.raw_transaction);
    return { ...signed, transaction_hash: sent.hash, broadcast: true, amount: req.amount, asset: 'ETH', chain_id: req.chainId };
  }
}

function errorText(err: unknown): string {
  const e = err as { getResponse?: () => unknown; message?: string };
  const r = e?.getResponse?.();
  if (r && typeof r === 'object') {
    const m = (r as { message?: unknown }).message;
    if (typeof m === 'string') return m;
    if (Array.isArray(m)) return m.join('; ');
  }
  if (typeof r === 'string') return r;
  return e?.message ?? String(err);
}
