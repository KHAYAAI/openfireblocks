import { Test } from '@nestjs/testing';
import { HttpService } from '@nestjs/axios';
import { ForbiddenException } from '@nestjs/common';
import { of } from 'rxjs';
import { SignService } from './sign.service';
import { PostgresService } from '../database/postgres.service';
import { AuditService } from '../database/audit.service';
import { EthereumService } from '../blockchain/ethereum.service';
import { PolicyService } from '../policies/policy.service';
import { RiskService } from '../risk/risk.service';
import { BillingService } from '../billing/billing.service';
import { MetricsService } from '../monitoring/metrics.service';
import { Customer } from '../customers/customer.service';
import { SignRequestDto } from './dto/sign-request.dto';

// Unit tests for the Phase 1 sign orchestration. External collaborators (MPC
// signer, PostgreSQL, Ethereum RPC, policy service) are mocked, so this runs
// without infra and asserts policy/audit/persist/broadcast wiring.
describe('SignService', () => {
  let audit: { logEvent: jest.Mock };
  let postgres: { saveTransaction: jest.Mock; updateStatus: jest.Mock };
  let policy: { evaluate: jest.Mock };
  let risk: { checkAndRecord: jest.Mock };
  let billing: { recordSigned: jest.Mock; recordBroadcast: jest.Mock };

  const mpcResponse = {
    data: {
      requestId: 'mpc-req',
      signedTx: '0xsigned',
      txHash: '0xhash',
      from: '0xfrom',
      status: 'signed',
      auditLogId: 1,
    },
  };

  const customer: Customer = {
    id: 1,
    customer_id: 'demo',
    email: 'demo@x.io',
    api_key: 'k',
    status: 'active',
    tier: 'pro',
    policies: {},
  };

  const validReq: SignRequestDto = {
    chainId: 11155111,
    to: '0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045',
    data: '0x',
    value: '0',
    gasLimit: 21000,
    gasPrice: '20000000000',
    nonce: 0,
  };

  const approve = () => ({
    approved: true,
    denials: [],
    requiresApproval: false,
    reason: 'ok',
  });

  async function build(ethereum: Partial<EthereumService>) {
    audit = { logEvent: jest.fn().mockResolvedValue(1) };
    postgres = {
      saveTransaction: jest.fn().mockResolvedValue(undefined),
      updateStatus: jest.fn().mockResolvedValue(undefined),
    };
    policy = { evaluate: jest.fn().mockResolvedValue(approve()) };
    risk = {
      checkAndRecord: jest
        .fn()
        .mockResolvedValue({ allowed: true, count: 1, limit: 100 }),
    };
    billing = {
      recordSigned: jest.fn().mockResolvedValue(undefined),
      recordBroadcast: jest.fn().mockResolvedValue(undefined),
    };

    const moduleRef = await Test.createTestingModule({
      providers: [
        SignService,
        MetricsService,
        { provide: HttpService, useValue: { post: jest.fn(() => of(mpcResponse)) } },
        { provide: PostgresService, useValue: postgres },
        { provide: AuditService, useValue: audit },
        { provide: EthereumService, useValue: ethereum },
        { provide: PolicyService, useValue: policy },
        { provide: RiskService, useValue: risk },
        { provide: BillingService, useValue: billing },
      ],
    }).compile();

    return moduleRef.get(SignService);
  }

  it('signs, persists and audits without broadcasting when RPC is disabled', async () => {
    const service = await build({ canBroadcast: false });
    const result = await service.sign(customer, validReq);

    expect(result.status).toBe('signed');
    expect(result.broadcasted).toBe(false);
    expect(postgres.saveTransaction).toHaveBeenCalledTimes(1);
    expect(postgres.saveTransaction.mock.calls[0][0].customerId).toBe('demo');
    expect(billing.recordSigned).toHaveBeenCalledWith('demo');

    const auditedTypes = audit.logEvent.mock.calls.map((c) => c[0].type);
    expect(auditedTypes).toContain('SIGN_REQUEST_RECEIVED');
    expect(auditedTypes).toContain('SIGN_SUCCESS');
  });

  it('broadcasts and updates status when RPC is enabled', async () => {
    const service = await build({
      canBroadcast: true,
      broadcastTransaction: jest.fn().mockResolvedValue('0xbroadcasthash'),
    });
    const result = await service.sign(customer, validReq);

    expect(result.status).toBe('broadcasted');
    expect(result.txHash).toBe('0xbroadcasthash');
    expect(postgres.updateStatus).toHaveBeenCalledWith(
      expect.any(String),
      'broadcasted',
      '0xbroadcasthash',
    );
  });

  it('denies and does not sign when policy rejects', async () => {
    const service = await build({ canBroadcast: false });
    policy.evaluate.mockResolvedValueOnce({
      approved: false,
      denials: ['Amount exceeds global limit'],
      requiresApproval: false,
      reason: '1 violation',
    });

    await expect(service.sign(customer, validReq)).rejects.toBeInstanceOf(
      ForbiddenException,
    );
    expect(postgres.saveTransaction).not.toHaveBeenCalled();
    const auditedTypes = audit.logEvent.mock.calls.map((c) => c[0].type);
    expect(auditedTypes).toContain('POLICY_DENIED');
  });

  it('denies and does not sign when velocity limit is exceeded', async () => {
    const service = await build({ canBroadcast: false });
    risk.checkAndRecord.mockResolvedValueOnce({
      allowed: false,
      count: 101,
      limit: 100,
      reason: 'velocity limit exceeded',
    });

    await expect(service.sign(customer, validReq)).rejects.toBeInstanceOf(
      ForbiddenException,
    );
    expect(postgres.saveTransaction).not.toHaveBeenCalled();
    const auditedTypes = audit.logEvent.mock.calls.map((c) => c[0].type);
    expect(auditedTypes).toContain('RISK_DENIED');
  });
});
