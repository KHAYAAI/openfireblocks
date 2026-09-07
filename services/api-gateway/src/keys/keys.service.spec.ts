import { ConflictException, ServiceUnavailableException } from '@nestjs/common';
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

  function build(temporalStart: jest.Mock, createKey?: jest.Mock) {
    const postgres = {
      createKey: createKey ?? jest.fn().mockResolvedValue(undefined),
      createCeremony: jest.fn().mockResolvedValue(undefined),
      setCeremonyFailed: jest.fn().mockResolvedValue(undefined),
      setKeyFailed: jest.fn().mockResolvedValue(undefined),
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

  // Found on a real cluster: only the ceremony was marked failed. The
  // key_pairs row stayed at 'pending_dkg' with no workflow behind it, so it
  // read as perpetually provisioning and (before migration 016) its name
  // was consumed forever.
  it('also marks the key itself failed, not just the ceremony', async () => {
    const start = jest
      .fn()
      .mockRejectedValue(new ServiceUnavailableException('Temporal connection failed: ECONNREFUSED'));
    const { service, postgres } = build(start);

    await expect(service.createKey(customer, req)).rejects.toBeInstanceOf(ServiceUnavailableException);

    expect(postgres.setKeyFailed).toHaveBeenCalledWith(expect.any(String), 'cust-1');
  });

  // A duplicate key name is a client error. It used to escape as a raw 500
  // carrying the Postgres constraint text.
  it('reports a duplicate key name as 409, not a 500', async () => {
    const duplicate = Object.assign(
      new Error('duplicate key value violates unique constraint "key_pairs_customer_name_live_key"'),
      { code: '23505' },
    );
    const start = jest.fn().mockResolvedValue({ workflowId: 'wf-1' });
    const { service, temporal } = build(start, jest.fn().mockRejectedValue(duplicate));

    await expect(service.createKey(customer, req)).rejects.toBeInstanceOf(ConflictException);
    // and it must not have started a ceremony for a key it could not create
    expect(temporal.start).not.toHaveBeenCalled();
  });

  // Any other database failure is still a server error -- the 23505 branch
  // must not swallow unrelated problems.
  it('does not convert non-unique-violation database errors into 409', async () => {
    const other = Object.assign(new Error('connection terminated'), { code: '08006' });
    const start = jest.fn().mockResolvedValue({ workflowId: 'wf-1' });
    const { service } = build(start, jest.fn().mockRejectedValue(other));

    await expect(service.createKey(customer, req)).rejects.toThrow('connection terminated');
  });
});
