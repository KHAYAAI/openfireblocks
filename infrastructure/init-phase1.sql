-- OpenFireblocks Phase 1 schema: multi-tenancy.
-- Runs after init-db.sql (postgres executes initdb scripts in filename order).
-- Idempotent so re-runs are safe.

-- ---------------------------------------------------------------------------
-- Customers (tenants). Each holds an API key and a tier that policies key off.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS customers (
  id          SERIAL PRIMARY KEY,
  customer_id VARCHAR(255) UNIQUE NOT NULL,   -- external/stable id (UUID or slug)
  email       VARCHAR(255) NOT NULL,
  api_key     VARCHAR(255) UNIQUE NOT NULL,   -- bearer credential (hashed in prod; see note below)
  status      VARCHAR(50)  NOT NULL DEFAULT 'active', -- active | suspended | deleted
  tier        VARCHAR(50)  NOT NULL DEFAULT 'free',   -- free | pro | enterprise
  policies    JSONB        NOT NULL DEFAULT '{}'::jsonb, -- per-tenant policy overrides (whitelist, limits)
  created_at  TIMESTAMPTZ  DEFAULT NOW(),
  updated_at  TIMESTAMPTZ  DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_customers_api_key     ON customers(api_key);
CREATE INDEX IF NOT EXISTS idx_customers_customer_id ON customers(customer_id);

-- ---------------------------------------------------------------------------
-- Tenant scoping: carry customer_id on audit + transaction rows.
-- ---------------------------------------------------------------------------
ALTER TABLE audit.events
  ADD COLUMN IF NOT EXISTS customer_id VARCHAR(255);
ALTER TABLE signing.transactions
  ADD COLUMN IF NOT EXISTS customer_id VARCHAR(255);

CREATE INDEX IF NOT EXISTS idx_audit_customer_id ON audit.events(customer_id);
CREATE INDEX IF NOT EXISTS idx_tx_customer_id    ON signing.transactions(customer_id);

-- ---------------------------------------------------------------------------
-- Row-level security scaffolding.
--
-- The application enforces tenant isolation by always filtering on customer_id
-- (see api-gateway). RLS below is an optional defence-in-depth layer: enable it
-- and connect with a non-owner role that sets `app.current_customer` per
-- transaction (SET LOCAL app.current_customer = '<id>'). Left disabled by
-- default so the bootstrap/admin role can seed data.
-- ---------------------------------------------------------------------------
-- ALTER TABLE signing.transactions ENABLE ROW LEVEL SECURITY;
-- CREATE POLICY tenant_isolation_tx ON signing.transactions
--   USING (customer_id = current_setting('app.current_customer', true));
--
-- ALTER TABLE audit.events ENABLE ROW LEVEL SECURITY;
-- CREATE POLICY tenant_isolation_audit ON audit.events
--   USING (customer_id = current_setting('app.current_customer', true));

-- ---------------------------------------------------------------------------
-- Seed a demo tenant for local development. Remove in production.
-- API keys are stored as SHA-256 hashes; the plaintext key for this row is
-- "dev-demo-key" (sha256 below). Authenticate with: Authorization: Bearer dev-demo-key
-- ---------------------------------------------------------------------------
INSERT INTO customers (customer_id, email, api_key, status, tier)
VALUES (
  'demo',
  'demo@openfireblocks.local',
  '6cbae51c7775b973f845b3fb4b333495890ecc9c57a9c3b3d662a3200d3227e1',
  'active',
  'pro'
)
ON CONFLICT (customer_id) DO NOTHING;
