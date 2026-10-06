import { UnprocessableEntityException } from '@nestjs/common';
import { ApprovalRequiredException } from '../keys/approval-required.exception';
import { TransfersService } from './transfers.service';

// The orchestration: when a transfer runs at once, when it waits, and that it
// runs exactly once after its approval. The approvals store here is an
// in-memory stand-in with the lifecycle the database enforces; the real rules
// are tested against Postgres in transfers.live.spec.ts.

const customer = { customer_id: 'cust-1', name: 'Forge', tier: 'pro', policies: {} } as never;
const APPROVER = { userId: 'u-oscar', label: 'oscar@forge.example' };

function fakeApprovals(requiredApprovals = 2) {
  const rows = new Map<string, any>();
  let n = 0;
  return {
    rows,
    getPolicy: jest.fn(async () => ({ requiredApprovals, windowMinutes: 60 })),
    openTransferRequest: jest.fn(async (i: any) => {
      const approvalId = `ap-${++n}`;
      rows.set(approvalId, { approvalId, keyId: i.keyId, kind: i.kind, request: i.request, prepared: null, status: 'awaiting_approval', result: null, error: null, summary: i.summary, initiator: i.initiator });
      return { approvalId, expiresAt: '2030-01-01T00:00:00Z' };
    }),
    pendingTransfer: jest.fn(async (_c: string, id: string) => (rows.has(id) ? { ...rows.get(id) } : null)),
    // Mirrors pending_transfer_guard: executing is only taken from the states
    // the caller names, and only one caller can take it.
    transitionTransfer: jest.fn(async (_c: string, id: string, from: string[], to: string, patch: any = {}) => {
      const r = rows.get(id);
      if (!r || !from.includes(r.status)) return false;
      r.status = to;
      if (patch.result) r.result = patch.result;
      r.error = patch.error ?? null;
      if (patch.prepared) r.prepared = patch.prepared;
      return true;
    }),
  };
}

function build(opts: { run?: jest.Mock; approvals?: ReturnType<typeof fakeApprovals>; travelThrows?: boolean; rpc?: any } = {}) {
  const approvals = opts.approvals ?? fakeApprovals();
  const keys = {
    sendSolana: opts.run ?? jest.fn().mockResolvedValue({ signature: 'SolSig', broadcast: true }),
    sendBitcoin: jest.fn().mockResolvedValue({ txid: 'btc', broadcast: true }),
    sendCosmos: jest.fn().mockResolvedValue({ txhash: 'cos', broadcast: true }),
    signTransaction: jest.fn().mockResolvedValue({ raw_transaction: '0xraw' }),
    getKey: jest.fn().mockResolvedValue({ key_id: 'k1', name: 'Treasury', address: '0xFrom' }),
    assertTravelRuleComplete: jest.fn(() => { if (opts.travelThrows) throw new UnprocessableEntityException({ missing: ['originator'] }); }),
  };
  const hooks = { register: jest.fn() };
  const customers = { getByCustomerId: jest.fn().mockResolvedValue(customer) };
  const events: Array<[string, string, Record<string, unknown>]> = [];
  const webhooks = { emit: jest.fn(async (c: string, t: string, d: Record<string, unknown>) => { events.push([c, t, d]); }) };
  const svc = new TransfersService(approvals as never, hooks as never, keys as never, customers as never, (opts.rpc ?? {}) as never, undefined, webhooks as never);
  svc.onModuleInit();
  return { svc, approvals, keys, hooks, events };
}

const dto = { destination: 'DST', amount: '10000000000' }; // 10 SOL
const needsApproval = () => jest.fn().mockRejectedValueOnce(new ApprovalRequiredException(['high-value transaction (> 10 ETH) requires approval'], 'r1'));

describe('TransfersService', () => {
  it('registers itself so approval decisions reach it', () => {
    expect(build().hooks.register).toHaveBeenCalled();
  });

  it('runs a transfer at once when policy lets it', async () => {
    const { svc, approvals } = build();
    const out = await svc.submit(customer, 'k1', 'solana', dto as never, APPROVER);
    expect(out).toMatchObject({ status: 'completed', result: { signature: 'SolSig' } });
    expect(approvals.openTransferRequest).not.toHaveBeenCalled();
  });

  it('parks a transfer that needs approval, asks the approvers, and signs nothing', async () => {
    const run = needsApproval();
    const { svc, approvals } = build({ run });
    const out = await svc.submit(customer, 'k1', 'solana', { ...dto, idempotencyKey: 'caller-key' } as never, APPROVER);

    expect(out).toMatchObject({ status: 'pending_approval', approvalId: 'ap-1', requiredApprovals: 2, reasons: ['high-value transaction (> 10 ETH) requires approval'] });
    const opened = approvals.openTransferRequest.mock.calls[0][0];
    expect(opened.initiator).toEqual(APPROVER); // so the database can exclude them from approving
    expect(opened.request).toEqual(dto); // exactly what was asked; the caller's idempotency key is not part of it
    expect(opened.summary).toMatchObject({ to: 'DST', asset: 'SOL', amount: '10000000000', keyName: 'Treasury', blockchain: 'solana' });
    expect(run).toHaveBeenCalledTimes(1); // the attempt that was refused, and no other
  });

  it('refuses before asking anyone when the Travel Rule information is incomplete', async () => {
    const { svc, approvals } = build({ run: needsApproval(), travelThrows: true });
    await expect(svc.submit(customer, 'k1', 'solana', dto as never, APPROVER)).rejects.toBeInstanceOf(UnprocessableEntityException);
    expect(approvals.openTransferRequest).not.toHaveBeenCalled();
  });

  it('does not park when the failure is not an approval requirement', async () => {
    const { svc, approvals } = build({ run: jest.fn().mockRejectedValue(new Error('signer is down')) });
    await expect(svc.submit(customer, 'k1', 'solana', dto as never, APPROVER)).rejects.toThrow('signer is down');
    expect(approvals.openTransferRequest).not.toHaveBeenCalled();
  });

  describe('after the approvers decide', () => {
    async function parked() {
      const run = jest.fn().mockRejectedValueOnce(new ApprovalRequiredException(['big'], 'r')).mockResolvedValue({ signature: 'SolSig', broadcast: true });
      const ctx = build({ run });
      await ctx.svc.submit(customer, 'k1', 'solana', dto as never, APPROVER);
      return { ...ctx, run };
    }
    const decided = (status: string) => ({ approvalId: 'ap-1', status } as never);

    it('waits while the approval is still pending', async () => {
      const { svc, run } = await parked();
      expect(await svc.onDecision('cust-1', decided('pending'))).toEqual({ status: 'awaiting_approval' });
      expect(run).toHaveBeenCalledTimes(1);
    });

    it('executes once the approval reaches quorum, as approved, with the approval\'s own idempotency key', async () => {
      const { svc, run, approvals } = await parked();
      const out = await svc.onDecision('cust-1', decided('approved'));
      expect(out).toMatchObject({ status: 'completed', result: { signature: 'SolSig' } });
      const [, , req, opts] = run.mock.calls[1];
      expect(req).toMatchObject({ ...dto, idempotencyKey: 'approval:ap-1' });
      expect(opts).toEqual({ approvalGranted: true });
      expect(approvals.rows.get('ap-1').status).toBe('completed');
    });

    it('announces the lifecycle to the customer\'s webhooks: held, then completed', async () => {
      const { svc, events } = await parked();
      await svc.onDecision('cust-1', decided('approved'));
      expect(events.map((e) => e[1])).toEqual(['transfer.pending_approval', 'transfer.completed']);
      expect(events[0][2]).toMatchObject({ approval_id: 'ap-1', kind: 'solana', asset: 'SOL', required_approvals: 2 });
      expect(events[1][2]).toMatchObject({ approval_id: 'ap-1', result: { signature: 'SolSig' } });
    });

    it('announces a rejection, and a failure with its reason', async () => {
      const a = await parked();
      await a.svc.onDecision('cust-1', decided('rejected'));
      expect(a.events.map((e) => e[1])).toEqual(['transfer.pending_approval', 'transfer.rejected']);

      const run = jest.fn().mockRejectedValueOnce(new ApprovalRequiredException(['big'], 'r')).mockRejectedValue(new Error('node down'));
      const b = build({ run });
      await b.svc.submit(customer, 'k1', 'solana', dto as never, APPROVER);
      await b.svc.onDecision('cust-1', decided('approved'));
      expect(b.events.map((e) => e[1])).toEqual(['transfer.pending_approval', 'transfer.failed']);
      expect(b.events[1][2]).toMatchObject({ error: 'node down' });
    });

    it('sends the transfer once even if the decision is delivered twice', async () => {
      const { svc, run } = await parked();
      await svc.onDecision('cust-1', decided('approved'));
      const again = await svc.onDecision('cust-1', decided('approved'));
      expect(again!.status).toBe('completed');
      expect(run).toHaveBeenCalledTimes(2); // the refused attempt and ONE execution
    });

    it('sends the transfer once when two deliveries race', async () => {
      const { svc, run } = await parked();
      await Promise.all([svc.onDecision('cust-1', decided('approved')), svc.onDecision('cust-1', decided('approved'))]);
      expect(run).toHaveBeenCalledTimes(2);
    });

    it('never signs a rejected or expired transfer', async () => {
      const a = await parked();
      expect(await a.svc.onDecision('cust-1', decided('rejected'))).toEqual({ status: 'rejected' });
      const b = await parked();
      expect(await b.svc.onDecision('cust-1', decided('expired'))).toEqual({ status: 'expired' });
      expect(a.run).toHaveBeenCalledTimes(1);
      expect(b.run).toHaveBeenCalledTimes(1);
    });

    it('records an execution failure without undoing the approval, and a retry completes it', async () => {
      const run = jest.fn()
        .mockRejectedValueOnce(new ApprovalRequiredException(['big'], 'r'))
        .mockRejectedValueOnce(new Error('the chain signer is unreachable'))
        .mockResolvedValue({ signature: 'SolSig', broadcast: true });
      const { svc, approvals } = build({ run });
      await svc.submit(customer, 'k1', 'solana', dto as never, APPROVER);

      const failed = await svc.onDecision('cust-1', decided('approved'));
      expect(failed).toEqual({ status: 'failed', error: 'the chain signer is unreachable' });

      expect(await svc.retry('cust-1', 'ap-1')).toMatchObject({ status: 'completed' });
      expect(approvals.rows.get('ap-1').status).toBe('completed');
    });

    it('a retry of a transfer that did not fail does nothing', async () => {
      const { svc, run } = await parked();
      await svc.onDecision('cust-1', decided('approved'));
      expect((await svc.retry('cust-1', 'ap-1')).status).toBe('completed');
      expect(run).toHaveBeenCalledTimes(2);
    });
  });

  describe('EVM', () => {
    const provider = () => ({
      getTransactionCount: jest.fn().mockResolvedValue(7),
      getFeeData: jest.fn().mockResolvedValue({ maxFeePerGas: 30n, maxPriorityFeePerGas: 2n, gasPrice: 25n }),
      estimateGas: jest.fn().mockResolvedValue(21000n),
      broadcastTransaction: jest.fn().mockResolvedValue({ hash: '0xhash' }),
    });
    const evm = { chainId: 11155111, destination: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8', amount: '42500000000000000000' };

    it('reads the nonce and fees when it runs, signs, and broadcasts', async () => {
      const p = provider();
      const { svc, keys } = build({ rpc: { configured: () => true, provider: () => p } });
      const out = await svc.submit(customer, 'k1', 'evm', evm as never, APPROVER);
      const signed = keys.signTransaction.mock.calls[0][2];
      expect(signed).toMatchObject({ nonce: 7, gasLimit: 25200, maxFeePerGas: '30', maxPriorityFeePerGas: '2', chainId: 11155111 });
      expect(p.broadcastTransaction).toHaveBeenCalledWith('0xraw');
      expect(out).toMatchObject({ status: 'completed', result: { transaction_hash: '0xhash', broadcast: true } });
    });

    it('keeps the nonce and fees it chose, so a retry signs the same transaction', async () => {
      const p = provider();
      const approvals = fakeApprovals();
      const { svc, keys } = build({ approvals, rpc: { configured: () => true, provider: () => p } });
      keys.signTransaction
        .mockRejectedValueOnce(new ApprovalRequiredException(['big'], 'r'))
        .mockRejectedValueOnce(new Error('broadcast failed'))
        .mockResolvedValue({ raw_transaction: '0xraw' });
      await svc.submit(customer, 'k1', 'evm', evm as never, APPROVER);
      await svc.onDecision('cust-1', { approvalId: 'ap-1', status: 'approved' } as never);
      expect(approvals.rows.get('ap-1').prepared).toMatchObject({ nonce: 7 });

      p.getTransactionCount.mockResolvedValue(8); // the account moved on in the meantime
      await svc.retry('cust-1', 'ap-1');
      const last = keys.signTransaction.mock.calls.at(-1)![2];
      expect(last.nonce).toBe(7);
    });

    it('says so when the chain has no endpoint, rather than guessing a nonce', async () => {
      const { svc } = build({ rpc: { configured: () => false } });
      await expect(svc.submit(customer, 'k1', 'evm', evm as never, APPROVER)).rejects.toThrow(/no JSON-RPC endpoint/);
    });
  });
});
