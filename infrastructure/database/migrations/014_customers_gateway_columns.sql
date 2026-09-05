-- Reconciles services/api-gateway/src/customers/customer.service.ts with the
-- real, currently-applied schema.
--
-- CustomerService was written against infrastructure/init-phase1.sql's
-- older "Phase 0/1" customers table (id serial, email, api_key varchar,
-- tier, policies jsonb) -- a schema this migrations/ directory never
-- actually carried forward. 001_initial_schema.sql's real customers table
-- has none of those columns (name/api_key_hash bytea/kyc_status instead).
-- Found while wiring api-gateway's /keys endpoint to Temporal: as written,
-- CustomerService.getByApiKey queries columns that don't exist against
-- this real schema, so ApiKeyGuard -- and therefore every authenticated
-- endpoint in the gateway -- could never succeed against a real database.
--
-- Additive only. api_key_hash (already real, already unique, already
-- unused by every other service -- only api-gateway resolves identity
-- from a bearer key) is reused rather than duplicated with a second
-- api_key column; email/tier/policies are genuinely new.
ALTER TABLE customers
  ADD COLUMN email VARCHAR(255),
  ADD COLUMN tier VARCHAR(50) NOT NULL DEFAULT 'free',
  ADD COLUMN policies JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Partial (not NOT NULL): existing rows seeded before this migration have
-- no email; the application layer (CreateCustomerDto) requires one for
-- every new customer created from here on.
CREATE UNIQUE INDEX idx_customers_email ON customers(email) WHERE email IS NOT NULL;
