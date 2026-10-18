-- Multi-custodian orchestration: assets held at other custodians, governed here.
--
-- An organisation may hold assets with several custodians (this platform's own
-- threshold keys, a bank custodian, an exchange). This registers the others so
-- the organisation sees one balance sheet and one set of controls: every transfer
-- from another custodian is held for the same approval quorum, freeze and address
-- whitelist as a transfer from this platform's own keys.
CREATE TABLE IF NOT EXISTS custodians (
  custodian_id  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id   UUID NOT NULL REFERENCES customers(customer_id),
  name          TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
  -- Only 'rest' (the connector contract in docs/deployment/MULTI-CUSTODIAN.md) today.
  type          VARCHAR(10) NOT NULL DEFAULT 'rest' CHECK (type IN ('rest')),
  base_url      TEXT NOT NULL CHECK (base_url ~ '^https?://'),
  -- The NAME of the environment variable holding the connector's bearer token. The
  -- token itself is never stored here, and the name is constrained so a stored
  -- value cannot make the gateway send some other secret to a connector.
  token_env     TEXT NOT NULL CHECK (token_env ~ '^CUSTODY_TOKEN_[A-Z0-9_]{1,60}$'),
  enabled       BOOLEAN NOT NULL DEFAULT true,
  added_by      UUID NOT NULL REFERENCES users(id),
  added_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (customer_id, name)
);

-- Where a connector is and which credential it uses cannot be changed after the
-- fact: pointing an existing custodian at another URL would redirect transfers
-- that were approved against the old one. Switch it off and add a new one.
CREATE OR REPLACE FUNCTION custodians_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'custodians are never deleted; switch them off' USING ERRCODE = 'OFB08';
  END IF;
  IF NEW.customer_id IS DISTINCT FROM OLD.customer_id OR NEW.base_url IS DISTINCT FROM OLD.base_url
     OR NEW.token_env IS DISTINCT FROM OLD.token_env OR NEW.type IS DISTINCT FROM OLD.type
     OR NEW.added_by IS DISTINCT FROM OLD.added_by THEN
    RAISE EXCEPTION 'a custodian''s connection cannot be changed; switch it off and add a new one' USING ERRCODE = 'OFB08';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS custodians_guard ON custodians;
CREATE TRIGGER custodians_guard BEFORE UPDATE OR DELETE ON custodians
  FOR EACH ROW EXECUTE FUNCTION custodians_guard();

-- Which account a transfer should come from, so an operator names an asset and an
-- amount instead of picking an account by hand: the first rule (lowest priority
-- number) whose asset matches and whose limit covers the amount.
CREATE TABLE IF NOT EXISTS custody_routes (
  route_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id   UUID NOT NULL REFERENCES customers(customer_id),
  asset         TEXT NOT NULL CHECK (asset ~ '^[A-Za-z0-9._-]{1,32}$'),
  max_amount    NUMERIC(78,0) CHECK (max_amount IS NULL OR max_amount > 0),
  custodian_id  UUID NOT NULL REFERENCES custodians(custodian_id),
  account_id    TEXT NOT NULL CHECK (length(account_id) BETWEEN 1 AND 200),
  priority      INTEGER NOT NULL DEFAULT 100 CHECK (priority BETWEEN 1 AND 10000),
  created_by    UUID NOT NULL REFERENCES users(id),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  removed_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_custody_routes_customer ON custody_routes (customer_id, asset) WHERE removed_at IS NULL;

-- Transfers from another custodian wait in the same table as the platform's own.
ALTER TABLE pending_transfers DROP CONSTRAINT IF EXISTS pending_transfers_kind_check;
ALTER TABLE pending_transfers ADD CONSTRAINT pending_transfers_kind_check
  CHECK (kind IN ('bitcoin', 'solana', 'cosmos', 'evm', 'custodian'));

ALTER TABLE custodians ENABLE ROW LEVEL SECURITY;
ALTER TABLE custodians FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON custodians;
CREATE POLICY tenant_isolation ON custodians
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

ALTER TABLE custody_routes ENABLE ROW LEVEL SECURITY;
ALTER TABLE custody_routes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON custody_routes;
CREATE POLICY tenant_isolation ON custody_routes
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

GRANT SELECT, INSERT, UPDATE ON custodians TO app_admin;
GRANT SELECT, INSERT, UPDATE ON custody_routes TO app_admin;
