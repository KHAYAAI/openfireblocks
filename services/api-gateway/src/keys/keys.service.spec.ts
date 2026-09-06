import { ServiceUnavailableException } from '@nestjs/common';
import { KeysService } from './keys.service';
import { PostgresService } from '../database/postgres.service';
import { KeysTemporalService } from './keys-temporal.service';
import { Customer } from '../customers/customer.service';
import { CreateKeyRequest } from './dto/create-key.dto';

// createKey used to have a `// TODO: Trigger DKG ceremony workflow` and
// never called Temporal at all -- these tests exercise the real
// orchestration that replaced it: party-endpoint derivation, the
// threshold -> tss-lib K conversion, ceremony bookkeeping, and the
// compensating "mark it failed" path when Temporal itself is unreachable.
describe('KeysService.createKey', () => {
  const customer: Customer = {
    customer_id: 'cust-1',
    name: 'demo',
    email: 'demo@x.io',
    status: 'active',
    tier: 'pro',
    policies: {},
  };

  const req: CreateKeyRequest = {
    name: 'my-key',
    blockchain: 'ethereum',
    threshold: 2, // 2-of-3
    total_parties: 3,
  };

  function build(temporalStart: jest.Mock) {
    const postgres = {
      createKey: jest.fn().mockResolvedValue(undefined),
      createCeremony: jest.fn().mockResolvedValue(undefined),
      setCeremonyFailed: jest.fn().mockResolvedValue(undefined),
    } as unknown as PostgresService;
    const temporal = { start: temporalStart } as unknown as KeysTemporalService;
    return { service: new KeysService(postgres, temporal), postgres, temporal };
  }

  afterEach(() => {
    delete process.env.MPC_PARTY_ENDPOINT_TEMPLATE;
  });

  it('derives 1-indexed party endpoints from the default template and converts threshold to tss-lib K', async () => {
    const start = jest.fn().mockResolvedValue({ workflowId: 'wf-1' });
    const { service, postgres } = build(start);

    const result = await service.createKey(customer, req);

    expect(start).toHaveBeenCalledWith(
      expect.objectContaining({
        customerId: 'cust-1',
        chainId: 'ethereum',
        n: 3,
        k: 1, // threshold(2) - 1 -- 2-of-3 means tss-lib threshold 1
        partyIds: [1, 2, 3],
        partyEndpoints: ['http://party-1:7000', 'http://party-2:7000', 'http://party-3:7000'],
      }),
    );
    expect(postgres.createKey).toHaveBeenCalled();
    expect(postgres.createCeremony).toHaveBeenCalledWith(
      expect.objectContaining({ customerId: 'cust-1', threshold: 2, totalParties: 3 }),
    );
    expect(result.status).toBe('pending_dkg');
    expect(result.ceremony_id).toBeTruthy();
  });

  it('honors MPC_PARTY_ENDPOINT_TEMPLATE overrides', async () => {
    process.env.MPC_PARTY_ENDPOINT_TEMPLATE = 'http://mpc-party-{id}.mpc.svc.cluster.local:9000';
    const start = jest.fn().mockResolvedValue({ workflowId: 'wf-1' });
    const { service } = build(start);

    await service.createKey(customer, req);

    expect(start).toHaveBeenCalledWith(
      expect.objectContaining({
        partyEndpoints: [
          'http://mpc-party-1.mpc.svc.cluster.local:9000',
          'http://mpc-party-2.mpc.svc.cluster.local:9000',
          'http://mpc-party-3.mpc.svc.cluster.local:9000',
        ],
      }),
    );
  });

  it('marks the ceremony failed and rethrows when Temporal is unreachable, instead of leaving it stuck initiated', async () => {
    const start = jest
      .fn()
      .mockRejectedValue(new ServiceUnavailableException('Temporal connection failed: ECONNREFUSED'));
    const { service, postgres } = build(start);

    await expect(service.createKey(customer, req)).rejects.toBeInstanceOf(ServiceUnavailableException);

    expect(postgres.setCeremonyFailed).toHaveBeenCalledWith(
      expect.any(String),
      'cust-1',
      expect.stringContaining('Temporal connection failed'),
    );
  });
});
