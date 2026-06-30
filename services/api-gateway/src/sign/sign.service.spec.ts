import { Test } from '@nestjs/testing';
import { HttpService } from '@nestjs/axios';
import { of } from 'rxjs';
import { SignService } from './sign.service';
import { PostgresService } from '../database/postgres.service';
import { AuditService } from '../database/audit.service';
import { EthereumService } from '../blockchain/ethereum.service';
import { SignRequestDto } from './dto/sign-request.dto';

// Unit tests for the sign orchestration. All external collaborators (MPC
// signer, PostgreSQL, Ethereum RPC) are mocked, so this runs without infra and
// asserts the audit/persist/broadcast wiring rather than real cryptography.
describe('SignService', () => {
  let service: SignService;
  let audit: { logEvent: jest.Mock };
  let postgres: { saveTransaction: jest.Mock; updateStatus: jest.Mock };

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

  const validReq: SignRequestDto = {
    chainId: 11155111,
    to: '0x742d35Cc6634C0532925a3b844Bc9e7595f42bE',
    data: '0x',
    value: '0',
    gasLimit: 21000,
    gasPrice: '20000000000',
    nonce: 0,
  };

  beforeEach(async () => {
    audit = { logEvent: jest.fn().mockResolvedValue(1) };
    postgres = {
      saveTransaction: jest.fn().mockResolvedValue(undefined),
      updateStatus: jest.fn().mockResolvedValue(undefined),
    };

    const moduleRef = await Test.createTestingModule({
      providers: [
        SignService,
        { provide: HttpService, useValue: { post: jest.fn(() => of(mpcResponse)) } },
        { provide: PostgresService, useValue: postgres },
        { provide: AuditService, useValue: audit },
        // Broadcasting disabled: exercises the sign-only path.
        { provide: EthereumService, useValue: { canBroadcast: false } },
      ],
    }).compile();

    service = moduleRef.get(SignService);
  });

  it('signs, persists and audits without broadcasting when RPC is disabled', async () => {
    const result = await service.sign(validReq);

    expect(result.status).toBe('signed');
    expect(result.broadcasted).toBe(false);
    expect(result.signedTx).toBe('0xsigned');
    expect(postgres.saveTransaction).toHaveBeenCalledTimes(1);

    const auditedTypes = audit.logEvent.mock.calls.map((c) => c[0].type);
    expect(auditedTypes).toContain('SIGN_REQUEST_RECEIVED');
    expect(auditedTypes).toContain('SIGN_SUCCESS');
  });

  it('broadcasts and updates status when RPC is enabled', async () => {
    const ethereum = {
      canBroadcast: true,
      broadcastTransaction: jest.fn().mockResolvedValue('0xbroadcasthash'),
    };

    const moduleRef = await Test.createTestingModule({
      providers: [
        SignService,
        { provide: HttpService, useValue: { post: jest.fn(() => of(mpcResponse)) } },
        { provide: PostgresService, useValue: postgres },
        { provide: AuditService, useValue: audit },
        { provide: EthereumService, useValue: ethereum },
      ],
    }).compile();

    const result = await moduleRef.get(SignService).sign(validReq);

    expect(result.status).toBe('broadcasted');
    expect(result.broadcasted).toBe(true);
    expect(result.txHash).toBe('0xbroadcasthash');
    expect(ethereum.broadcastTransaction).toHaveBeenCalledWith('0xsigned');
    expect(postgres.updateStatus).toHaveBeenCalledWith(
      expect.any(String),
      'broadcasted',
      '0xbroadcasthash',
    );
  });
});
