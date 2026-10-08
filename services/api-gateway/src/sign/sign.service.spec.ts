import { Test } from '@nestjs/testing';
import { HttpService } from '@nestjs/axios';
import { BadRequestException, ForbiddenException } from '@nestjs/common';
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
import { TokenRegistryService } from '../tokens/token-registry.service';
import { encodeTransfer } from '../keys/erc20';

// Unit tests for the Phase 1 sign orchestration. External collaborators (MPC
// signer, PostgreSQL, Ethereum RPC, policy service) are mocked, so this runs
// without infra and asserts policy/audit/persist/broadcast wiring.
describe('SignService', () => {
  let audit: { logEvent: jest.Mock };
  let postgres: { recordTransfer: jest.Mock; updateStatus: jest.Mock };
  let policy: { evaluate: jest.Mock };
  let risk: { checkAndRecord: jest.Mock };
  let billing: { recordSigned: jest.Mock; recordBroadcast: jest.Mock };
  let tokens: { byContract: jest.Mock };

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
    customer_id: 'demo',
    name: 'demo',
    email: 'demo@x.io',
    status: 'active',
    tier: 'pro',
    policies: {},
    raw_digest_signing_enabled: true, arbitrary_contract_calls_enabled: false,
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
      recordTransfer: jest.fn().mockResolvedValue(undefined),
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
    tokens = { byContract: jest.fn().mockResolvedValue(null) };

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
        { provide: TokenRegistryService, useValue: tokens },
      ],
    }).compile();

    return moduleRef.get(SignService);
  }

  it('signs, persists and audits without broadcasting when RPC is disabled', async () => {
    const service = await build({ canBroadcast: false });
    const result = await service.sign(customer, validReq);

    expect(result.status).toBe('signed');
    expect(result.broadcasted).toBe(false);
    expect(result.asset).toBe('NATIVE');
    expect(postgres.recordTransfer).toHaveBeenCalledTimes(1);
    expect(postgres.recordTransfer.mock.calls[0][0].customerId).toBe('demo');
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
      'demo',
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
    expect(postgres.recordTransfer).not.toHaveBeenCalled();
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
    expect(postgres.recordTransfer).not.toHaveBeenCalled();
    const auditedTypes = audit.logEvent.mock.calls.map((c) => c[0].type);
    expect(auditedTypes).toContain('RISK_DENIED');
  });

  // Regression coverage for MISC-01: POST /sign used to forward calldata to
  // mpc-signer while evaluating policy only on the envelope's to/value, so
  // an ERC-20 transfer() hidden in `data` (to=token contract, value="0")
  // was signed without the destination whitelist or amount limit ever
  // seeing the real recipient or amount.
  describe('calldata governance', () => {
    const tokenContract = '0x1000000000000000000000000000000000000001';
    const recipient = '0x2000000000000000000000000000000000000002';

    it('evaluates policy against the decoded ERC-20 recipient and amount, not the envelope', async () => {
      const service = await build({ canBroadcast: false });
      tokens.byContract.mockResolvedValue({
        symbol: 'USDC',
        decimals: 6,
        pegCurrency: 'USD',
        contractAddress: tokenContract,
        status: 'verified',
      });

      const req: SignRequestDto = {
        ...validReq,
        to: tokenContract,
        value: '0',
        data: encodeTransfer(recipient, '1000000'),
      };
      const result = await service.sign(customer, req);

      expect(policy.evaluate).toHaveBeenCalledWith(
        expect.objectContaining({
          to: recipient,
          asset: 'USDC',
          assetAmount: '1000000',
          assetDecimals: 6,
          pegCurrency: 'USD',
        }),
      );
      expect(result.asset).toBe('USDC');
      expect(result.recipient).toBe(recipient);
      expect(result.amount).toBe('1000000');
      expect(postgres.recordTransfer.mock.calls[0][0]).toMatchObject({
        assetSymbol: 'USDC',
        effectiveTo: recipient,
        effectiveAmount: '1000000',
      });
    });

    it('refuses calldata it cannot decode when arbitrary contract calls are disabled', async () => {
      const service = await build({ canBroadcast: false });
      const req: SignRequestDto = {
        ...validReq,
        to: tokenContract,
        value: '0',
        data: '0xdeadbeef',
      };

      await expect(service.sign(customer, req)).rejects.toBeInstanceOf(BadRequestException);
      expect(policy.evaluate).not.toHaveBeenCalled();
      expect(postgres.recordTransfer).not.toHaveBeenCalled();
      const auditedTypes = audit.logEvent.mock.calls.map((c) => c[0].type);
      expect(auditedTypes).toContain('SIGN_FAILED');
    });

    it('refuses an unregistered token contract even though the calldata decodes', async () => {
      const service = await build({ canBroadcast: false });
      tokens.byContract.mockResolvedValue(null);
      const req: SignRequestDto = {
        ...validReq,
        to: tokenContract,
        value: '0',
        data: encodeTransfer(recipient, '1000000'),
      };

      await expect(service.sign(customer, req)).rejects.toBeInstanceOf(BadRequestException);
      expect(policy.evaluate).not.toHaveBeenCalled();
    });
  });
});
