import {
  BadRequestException,
  ConflictException,
  ForbiddenException,
  NotFoundException,
  ServiceUnavailableException,
} from '@nestjs/common';
import { Transaction, Wallet, getAddress } from 'ethers';
import { KeysService } from './keys.service';
import { PostgresService } from '../database/postgres.service';
import { PolicyService } from '../policies/policy.service';
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
      getKey: jest.fn().mockResolvedValue(null),
      getCompletedCeremonyForKey: jest.fn().mockResolvedValue(null),
    } as unknown as PostgresService;
    const temporal = {
      start: temporalStart,
      signWithThreshold: jest.fn(),
    } as unknown as KeysTemporalService;
    const policy = {
      evaluate: jest.fn().mockResolvedValue({
        approved: true,
        denials: [],
        requiresApproval: false,
        reason: 'ok',
      }),
    } as unknown as PolicyService;
    return {
      service: new KeysService(postgres, temporal, policy),
      postgres,
      temporal,
      policy,
    };
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

// Before this endpoint existed there was no route that could use a key
// created by POST /keys: POST /sign goes to mpc-signer (the separate
// single-key path) and ThresholdSigningWorkflow could only be started from
// inside Temporal.
describe('KeysService.signWithKey', () => {
  const customer: Customer = {
    customer_id: 'cust-1',
    name: 'demo',
    email: 'demo@x.io',
    status: 'active',
    tier: 'pro',
    policies: {},
  };

  const signReq = {
    message: 'a'.repeat(64),
    to: '0x1111111111111111111111111111111111111111',
    value: '1000',
    chainId: 11155111,
  };

  const activeKey = {
    key_id: 'key-1',
    status: 'active',
    threshold: 2,
    total_parties: 3,
    blockchain: 'ethereum',
    address: '0xabc',
  };
  const ceremony = { ceremony_id: 'cer-1', threshold: 2, total_parties: 3 };

  function build(overrides: {
    key?: unknown;
    ceremony?: unknown;
    approved?: boolean;
    sign?: jest.Mock;
  }) {
    const postgres = {
      getKey: jest.fn().mockResolvedValue(
        overrides.key === undefined ? activeKey : overrides.key,
      ),
      getCompletedCeremonyForKey: jest.fn().mockResolvedValue(
        overrides.ceremony === undefined ? ceremony : overrides.ceremony,
      ),
    } as unknown as PostgresService;
    const temporal = {
      signWithThreshold:
        overrides.sign ??
        jest.fn().mockResolvedValue({ status: 'completed', signature: 'ff'.repeat(65) }),
    } as unknown as KeysTemporalService;
    const policy = {
      evaluate: jest.fn().mockResolvedValue({
        approved: overrides.approved !== false,
        denials: overrides.approved === false ? ['amount_limit'] : [],
        requiresApproval: false,
        reason: 'x',
      }),
    } as unknown as PolicyService;
    return { service: new KeysService(postgres, temporal, policy), postgres, temporal, policy };
  }

  it('signs with threshold parties, not all of them', async () => {
    const sign = jest
      .fn()
      .mockResolvedValue({ status: 'completed', signature: 'ff'.repeat(65) });
    const { service } = build({ sign });

    const out = await service.signWithKey(customer, 'key-1', signReq);

    // 2 of 3, not 3 of 3: signing with the whole committee would turn a
    // 2-of-3 key into a 3-of-3 one and lose the fault tolerance that is
    // the entire point of a threshold.
    expect(sign).toHaveBeenCalledWith(
      expect.objectContaining({
        ceremonyId: 'cer-1',
        partyIds: [1, 2],
        partyEndpoints: ['http://party-1:7000', 'http://party-2:7000'],
      }),
    );
    expect(out.signature).toBe('ff'.repeat(65));
    expect(out.parties).toEqual([1, 2]);
  });

  // policy-service takes chainId as an int and rejects the blockchain
  // name with 400, so passing key.blockchain here denied every request.
  it('passes the numeric chainId to policy, not the blockchain name', async () => {
    const { service, policy } = build({});
    await service.signWithKey(customer, 'key-1', signReq);
    expect(policy.evaluate).toHaveBeenCalledWith(
      expect.objectContaining({ chainId: 11155111 }),
    );
  });

  it('denies when policy denies, before starting any ceremony', async () => {
    const sign = jest.fn();
    const { service } = build({ approved: false, sign });

    await expect(service.signWithKey(customer, 'key-1', signReq)).rejects.toBeInstanceOf(
      ForbiddenException,
    );
    expect(sign).not.toHaveBeenCalled();
  });

  it('404s for a key the customer does not have', async () => {
    const { service } = build({ key: null });
    await expect(service.signWithKey(customer, 'key-1', signReq)).rejects.toBeInstanceOf(
      NotFoundException,
    );
  });

  it('refuses to sign with a key that is not active', async () => {
    const { service } = build({ key: { ...activeKey, status: 'pending_dkg' } });
    await expect(service.signWithKey(customer, 'key-1', signReq)).rejects.toBeInstanceOf(
      ConflictException,
    );
  });

  it('refuses when no completed ceremony produced the shares', async () => {
    const { service } = build({ ceremony: null });
    await expect(service.signWithKey(customer, 'key-1', signReq)).rejects.toBeInstanceOf(
      ConflictException,
    );
  });

  // ThresholdSigningWorkflow reports a failed ceremony as a RESULT with
  // status:"failed", not as a thrown error. A caller that only checked for
  // an exception would return 200 with no signature.
  it('treats a failed signing result as a failure, not a success', async () => {
    const sign = jest
      .fn()
      .mockResolvedValue({ status: 'failed', error: 'party 2 unreachable' });
    const { service } = build({ sign });

    await expect(service.signWithKey(customer, 'key-1', signReq)).rejects.toThrow(
      /party 2 unreachable/,
    );
  });

  it('treats a completed result with no signature as a failure', async () => {
    const sign = jest.fn().mockResolvedValue({ status: 'completed' });
    const { service } = build({ sign });

    await expect(service.signWithKey(customer, 'key-1', signReq)).rejects.toBeInstanceOf(
      ServiceUnavailableException,
    );
  });
});

// The reason this endpoint exists: unlike signWithKey, the digest that gets
// signed is computed by this service from the same fields it gave the policy
// engine, so there is no caller-supplied digest that can disagree with the
// declared intent.
describe('KeysService.signTransaction', () => {
  const customer: Customer = {
    customer_id: 'cust-1',
    name: 'demo',
    email: 'demo@x.io',
    status: 'active',
    tier: 'pro',
    policies: {},
  };

  // A throwaway key used only to produce real signatures. Threshold and
  // single-key signatures are indistinguishable at this layer by
  // construction, which is the point of threshold ECDSA.
  const wallet = new Wallet(
    '0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d',
  );

  const txReq = {
    to: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8',
    value: '1000000000000000000',
    gasLimit: 21000,
    nonce: 7,
    chainId: 11155111,
    gasPrice: '20000000000',
  };

  const activeKey = {
    key_id: 'key-1',
    status: 'active',
    threshold: 2,
    total_parties: 3,
    blockchain: 'ethereum',
    address: wallet.address,
  };
  const ceremony = { ceremony_id: 'cer-1', threshold: 2, total_parties: 3 };

  // Signs whatever digest the service asks for, the way a real ceremony
  // does: 65 bytes [R||S||V] with V as a 0/1 recovery byte.
  function signingCeremony(signer: { signingKey: { sign: (d: string) => { r: string; s: string; yParity: number } } } = wallet) {
    return jest.fn().mockImplementation(async ({ message }: { message: string }) => {
      const sig = signer.signingKey.sign('0x' + message);
      return {
        status: 'completed',
        signature:
          sig.r.slice(2) + sig.s.slice(2) + (sig.yParity === 0 ? '00' : '01'),
      };
    });
  }

  function build(overrides: { key?: unknown; approved?: boolean; sign?: jest.Mock } = {}) {
    const postgres = {
      getKey: jest
        .fn()
        .mockResolvedValue(overrides.key === undefined ? activeKey : overrides.key),
      getCompletedCeremonyForKey: jest.fn().mockResolvedValue(ceremony),
    } as unknown as PostgresService;
    const temporal = {
      signWithThreshold: overrides.sign ?? signingCeremony(),
    } as unknown as KeysTemporalService;
    const policy = {
      evaluate: jest.fn().mockResolvedValue({
        approved: overrides.approved !== false,
        denials: overrides.approved === false ? ['amount_limit'] : [],
        requiresApproval: false,
        reason: 'x',
      }),
    } as unknown as PolicyService;
    return { service: new KeysService(postgres, temporal, policy), postgres, temporal, policy };
  }

  it('returns a broadcastable transaction whose sender is the key address', async () => {
    const { service } = build();

    const out = await service.signTransaction(customer, 'key-1', txReq);

    expect(out.from).toBe(getAddress(wallet.address));
    const parsed = Transaction.from(out.raw_transaction);
    expect(parsed.to).toBe(getAddress(txReq.to));
    expect(parsed.value).toBe(BigInt(txReq.value));
    expect(parsed.nonce).toBe(txReq.nonce);
    expect(parsed.chainId).toBe(BigInt(txReq.chainId));
    expect(parsed.from).toBe(getAddress(wallet.address));
  });

  // The guarantee the endpoint exists to provide: what policy evaluated and
  // what got hashed are the same values.
  it('signs the digest of exactly the fields policy evaluated', async () => {
    const sign = signingCeremony();
    const { service, policy } = build({ sign });

    const out = await service.signTransaction(customer, 'key-1', txReq);

    expect(policy.evaluate).toHaveBeenCalledWith(
      expect.objectContaining({
        to: txReq.to,
        value: txReq.value,
        chainId: txReq.chainId,
      }),
    );
    // The ceremony was asked to sign the transaction's own signing hash,
    // and that hash is the one reported back.
    const signedDigest = sign.mock.calls[0][0].message;
    expect(signedDigest).toBe(out.signing_hash);
    const reference = Transaction.from({
      type: 0,
      to: txReq.to,
      value: BigInt(txReq.value),
      data: '0x',
      gasLimit: txReq.gasLimit,
      gasPrice: BigInt(txReq.gasPrice),
      nonce: txReq.nonce,
      chainId: txReq.chainId,
    });
    expect('0x' + signedDigest).toBe(reference.unsignedHash);
  });

  it('denies before building a ceremony when policy denies', async () => {
    const sign = jest.fn();
    const { service } = build({ approved: false, sign });

    await expect(service.signTransaction(customer, 'key-1', txReq)).rejects.toBeInstanceOf(
      ForbiddenException,
    );
    expect(sign).not.toHaveBeenCalled();
  });

  // A malformed request should not consume a policy evaluation or a signing
  // ceremony.
  it('rejects a malformed fee combination as 400, before policy', async () => {
    const sign = jest.fn();
    const { service, policy } = build({ sign });

    await expect(
      service.signTransaction(customer, 'key-1', {
        ...txReq,
        maxFeePerGas: '1',
        maxPriorityFeePerGas: '1',
      }),
    ).rejects.toBeInstanceOf(BadRequestException);
    expect(policy.evaluate).not.toHaveBeenCalled();
    expect(sign).not.toHaveBeenCalled();
  });

  it('404s for a key the customer does not have', async () => {
    const { service } = build({ key: null });
    await expect(service.signTransaction(customer, 'key-1', txReq)).rejects.toBeInstanceOf(
      NotFoundException,
    );
  });

  it('refuses to sign with a key that is not active', async () => {
    const { service } = build({ key: { ...activeKey, status: 'pending_dkg' } });
    await expect(service.signTransaction(customer, 'key-1', txReq)).rejects.toBeInstanceOf(
      ConflictException,
    );
  });

  // The check that matters most: a ceremony returning a well-formed
  // signature from the WRONG key must not yield a transaction. Handing that
  // back would give the customer valid bytes spending from an address they
  // do not control.
  it('refuses when the ceremony signs with a different key', async () => {
    const { service } = build({ sign: signingCeremony(Wallet.createRandom()) });

    await expect(service.signTransaction(customer, 'key-1', txReq)).rejects.toThrow(
      /refusing to return a transaction signed by the wrong key/,
    );
  });

  it('treats a failed signing result as a failure, not a success', async () => {
    const sign = jest.fn().mockResolvedValue({ status: 'failed', error: 'party 2 unreachable' });
    const { service } = build({ sign });

    await expect(service.signTransaction(customer, 'key-1', txReq)).rejects.toThrow(
      /party 2 unreachable/,
    );
  });

  it('signs EIP-1559 transactions too', async () => {
    const { service } = build();
    const out = await service.signTransaction(customer, 'key-1', {
      to: txReq.to,
      value: txReq.value,
      gasLimit: txReq.gasLimit,
      nonce: txReq.nonce,
      chainId: txReq.chainId,
      maxFeePerGas: '30000000000',
      maxPriorityFeePerGas: '1000000000',
    });
    const parsed = Transaction.from(out.raw_transaction);
    expect(parsed.type).toBe(2);
    expect(parsed.from).toBe(getAddress(wallet.address));
  });
});
