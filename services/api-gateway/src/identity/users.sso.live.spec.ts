import { Pool } from 'pg';
import { UsersService } from './users.service';

// Verifies SSO user provisioning against a REAL Postgres schema -- the part
// unit tests with a mocked pool cannot see: migration 015's nullable
// password_hash, its auth_provider CHECK, and the
// users_credential_matches_provider_check constraint that forbids a
// half-provisioned row. Skips (does not fail) without a reachable database,
// matching the convention used elsewhere in this repo.
const DSN =
  process.env.DATABASE_ADMIN_URL ??
  'postgres://app_admin:dev-only@localhost:5432/openfireblocks?sslmode=disable';

describe('UsersService SSO provisioning (live Postgres)', () => {
  let pool: Pool | null = null;
  let users: UsersService | null = null;
  const createdIds: string[] = [];

  beforeAll(async () => {
    const candidate = new Pool({ connectionString: DSN, connectionTimeoutMillis: 2000 });
    try {
      await candidate.query('SELECT 1');
      pool = candidate;
      users = new UsersService(candidate);
    } catch (err) {
      // eslint-disable-next-line no-console
      console.warn(`skipping live SSO user tests -- postgres not reachable: ${(err as Error).message}`);
      await candidate.end().catch(() => undefined);
    }
  }, 15000);

  afterAll(async () => {
    if (pool && createdIds.length) {
      await pool
        .query('DELETE FROM users WHERE id = ANY($1::uuid[])', [createdIds])
        .catch(() => undefined);
    }
    await pool?.end().catch(() => undefined);
  });

  it('provisions a passwordless SSO user and finds it by WorkOS id', async () => {
    if (!users) return;
    const email = `sso-live-${Date.now()}@corp.example`;
    const workosUserId = `workos_user_live_${Date.now()}`;

    const created = await users.createSsoUser({
      email,
      fullName: 'SSO Live User',
      workosUserId,
      workosOrganizationId: 'org_live',
    });
    createdIds.push(created.id);

    expect(created.password_hash).toBeNull();
    expect(created.auth_provider).toBe('workos_sso');
    expect(created.workos_user_id).toBe(workosUserId);

    const found = await users.findByWorkosUserId(workosUserId);
    expect(found?.id).toBe(created.id);

    // A passwordless account must never authenticate by password.
    await expect(users.verifyPassword(created, 'anything')).resolves.toBe(false);
    await expect(users.verifyPassword(created, '')).resolves.toBe(false);
  }, 20000);

  it('links a WorkOS identity onto an existing password account', async () => {
    if (!users || !pool) return;
    const email = `link-live-${Date.now()}@corp.example`;
    const created = await users.create({
      email,
      password: 'correct horse battery staple',
      fullName: 'Password User',
    });
    createdIds.push(created.id);
    expect(created.password_hash).toBeTruthy();

    const workosUserId = `workos_user_link_${Date.now()}`;
    const linked = await users.linkWorkosIdentity(created.id, {
      workosUserId,
      workosOrganizationId: 'org_live',
    });

    expect(linked.workos_user_id).toBe(workosUserId);
    // Linking adds a second way in; it must not remove the password.
    expect(linked.password_hash).toBe(created.password_hash);
    expect(linked.auth_provider).toBe('password');
  }, 20000);

  it('rejects a half-provisioned SSO row at the database level', async () => {
    if (!pool) return;
    // auth_provider says SSO but there is no workos_user_id -- the CHECK
    // constraint from migration 015 must refuse this outright.
    await expect(
      pool.query(
        `INSERT INTO users (email, full_name, auth_provider) VALUES ($1, 'Bad Row', 'workos_sso')`,
        [`bad-row-${Date.now()}@corp.example`],
      ),
    ).rejects.toThrow(/users_credential_matches_provider_check/);
  }, 20000);
});
