import { Connection, WorkflowClient } from '@temporalio/client';
import { Pool } from 'pg';
import { keccak256, recoverAddress, toUtf8Bytes } from 'ethers';
import { CustomerService } from '../customers/customer.service';
import { PostgresService } from '../database/postgres.service';
import { KeysTemporalService } from './keys-temporal.service';
import { KeysService } from './keys.service';

// Live end-to-end test of the whole customer-facing key-provisioning path:
// a real customer authenticating against real Postgres, POST /keys' service
// layer starting a real Temporal workflow, a real 2-of-3 tss-lib DKG across
// three real mpc-party processes, key_pairs/dkg_ceremonies actually
// transitioning, and finally the resulting key producing a real threshold
// signature that recovers to its own stored address.
//
// This is the test that would have caught (a) createKey's old
// `// TODO: Trigger DKG ceremony workflow` doing nothing at all, and (b)
// CustomerService querying columns that don't exist against the real
// schema -- neither of which any unit test could see.
//
// Skips (does not fail) unless every real dependency is actually up, the
// same convention services/backup/integration_test.go and
// temporal-worker's key_rotation_db_test.go follow:
//
//   postgres, plus:
//   temporal server start-dev --port 7233
//   PARTY_ID=1 PORT=7201 ./mpc-party &  (and 2/7202, 3/7203)
//   TEMPORAL_HOSTPORT=localhost:7233 DATABASE_URL=...app_admin... ./temporal-worker &
//   npx jest src/keys/keys.provisioning.live.spec.ts

const ADMIN_DSN =
  process.env.DATABASE_ADMIN_URL ??
  'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const TENANT_DSN =
  process.env.DATABASE_URL ??
  'postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable';
const TEMPORAL_ADDRESS = process.env.TEMPORAL_HOSTPORT ?? 'localhost:7233';
const PARTY_PORTS = [7201, 7202, 7203];

async function dependenciesUp(): Promise<string | null> {
  const admin = new Pool({ connectionString: ADMIN_DSN, connectionTimeoutMillis: 2000 });
  try {
    await admin.query('SELECT 1');
  } catch (err) {
    return `postgres not reachable: ${(err as Error).message}`;
  } finally {
    await admin.end().catch(() => undefined);
  }

  try {
    const connection = await Connection.connect({ address: TEMPORAL_ADDRESS });
    await connection.close();
  } catch (err) {
    return `temporal not reachable: ${(err as Error).message}`;
  }

  for (const port of PARTY_PORTS) {
    try {
      const res = await fetch(`http://localhost:${port}/health`);
      if (!res.ok) return `mpc-party on ${port} unhealthy: ${res.status}`;
    } catch (err) {
      return `mpc-party on ${port} not reachable: ${(err as Error).message}`;
    }
  }
  return null;
}

describe('key provisioning (live: real Postgres + Temporal + 3 mpc-party processes)', () => {
  let skipReason: string | null = null;

  beforeAll(async () => {
    skipReason = await dependenciesUp();
    if (skipReason) {
      // eslint-disable-next-line no-console
      console.warn(`skipping live provisioning test -- ${skipReason}`);
    }
  }, 30000);

  it('provisions a real threshold key through createKey and signs with it', async () => {
    if (skipReason) return;

    process.env.DATABASE_ADMIN_URL = ADMIN_DSN;
    process.env.DATABASE_URL = TENANT_DSN;
    process.env.TEMPORAL_HOSTPORT = TEMPORAL_ADDRESS;
    process.env.MPC_PARTY_ENDPOINT_TEMPLATE = 'http://localhost:720{id}';

    const customers = new CustomerService();
    const tenantPool = new Pool({ connectionString: TENANT_DSN });
    const adminPool = new Pool({ connectionString: ADMIN_DSN });
    const postgres = new PostgresService(tenantPool as never);
    const temporal = new KeysTemporalService();
    const keys = new KeysService(postgres, temporal);

    const customer = await customers.createCustomer({
      email: `live-provisioning-${Date.now()}@openfireblocks.test`,
      tier: 'pro',
    });

    try {
      const created = await keys.createKey(customer as never, {
        name: `live-provisioning-${Date.now()}`,
        blockchain: 'ethereum',
        threshold: 2, // 2-of-3
        total_parties: 3,
      });
      expect(created.status).toBe('pending_dkg');
      expect(created.ceremony_id).toBeTruthy();

      // The workflow runs a real DKG; poll until key_pairs reflects it.
      const deadline = Date.now() + 150000;
      let key: { status: string; address: string; public_key: string } | undefined;
      let ceremony: { status: string; error_message: string | null; completed_at: Date | null } | undefined;
      while (Date.now() < deadline) {
        key = (
          await adminPool.query(
            'SELECT status, address, public_key FROM key_pairs WHERE key_id = $1',
            [created.id],
          )
        ).rows[0];
        ceremony = (
          await adminPool.query(
            'SELECT status, error_message, completed_at FROM dkg_ceremonies WHERE ceremony_id = $1',
            [created.ceremony_id],
          )
        ).rows[0];
        if (key?.status === 'active') break;
        expect(ceremony?.status).not.toBe('failed');
        await new Promise((r) => setTimeout(r, 2000));
      }

      expect(key?.status).toBe('active');
      expect(key?.address).toMatch(/^0x[0-9a-fA-F]{40}$/);
      expect(ceremony?.status).toBe('completed');
      expect(ceremony?.completed_at).toBeTruthy();

      // The provisioned key must actually be usable, not just recorded:
      // sign with it and confirm the signature recovers to its address.
      const connection = await Connection.connect({ address: TEMPORAL_ADDRESS });
      const client = new WorkflowClient({ connection, namespace: 'default' });
      const messageHash = keccak256(toUtf8Bytes(`live provisioning test ${created.id}`));

      const handle = await client.start('ThresholdSigningWorkflow', {
        taskQueue: 'transaction-settlement',
        workflowId: `live-provisioning-sign-${created.id}`,
        args: [
          {
            ceremonyId: created.ceremony_id,
            message: messageHash.slice(2),
            partyIds: [1, 2], // threshold+1 of the 3 that ran the DKG
            partyEndpoints: ['http://localhost:7201', 'http://localhost:7202'],
            chainId: 'ethereum',
          },
        ],
      });
      const signResult = (await handle.result()) as { status: string; signature: string; error?: string };
      await connection.close();

      expect(signResult.status).toBe('completed');
      const sig = signResult.signature;
      const recovered = recoverAddress(messageHash, {
        r: '0x' + sig.slice(0, 64),
        s: '0x' + sig.slice(64, 128),
        v: parseInt(sig.slice(128, 130), 16) + 27,
      });
      expect(recovered.toLowerCase()).toBe(key!.address.toLowerCase());
    } finally {
      await adminPool
        .query('DELETE FROM customers WHERE customer_id = $1', [customer.customer_id])
        .catch(() => undefined);
      await temporal.onModuleDestroy().catch(() => undefined);
      await tenantPool.end().catch(() => undefined);
      await adminPool.end().catch(() => undefined);
    }
  }, 200000);
});
