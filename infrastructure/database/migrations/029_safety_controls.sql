-- Organisation safety controls: an emergency freeze and an address whitelist.
--
-- Freeze: while set, nothing the organisation owns is signed, from any path
-- (console, API key, agent, settlement decisions). Anyone who can decide on
-- transfers may freeze (stopping is always safe); only an admin may lift it.
--
-- Whitelist: when enforced, a transfer may only go to an address on the list,
-- and a newly added address is not usable until its cooling-off period has
-- passed, so an attacker who briefly controls an admin session cannot add an
-- address and drain to it in the same minute.
CREATE TABLE IF NOT EXISTS org_controls (
  customer_id                 UUID PRIMARY KEY REFERENCES customers(customer_id),
  frozen                      BOOLEAN NOT NULL DEFAULT false,
  frozen_reason               TEXT,
  frozen_by                   UUID REFERENCES users(id),
  frozen_at                   TIMESTAMPTZ,
  whitelist_enforced          BOOLEAN NOT NULL DEFAULT false,
  whitelist_cooldown_minutes  INTEGER NOT NULL DEFAULT 0
                                CHECK (whitelist_cooldown_minutes BETWEEN 0 AND 10080),
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS address_whitelist (
  entry_id     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id  UUID NOT NULL REFERENCES customers(customer_id),
  blockchain   VARCHAR(20) NOT NULL
                 CHECK (blockchain IN ('bitcoin', 'ethereum', 'polygon', 'solana', 'cosmos')),
  address      TEXT NOT NULL CHECK (length(address) BETWEEN 8 AND 128),
  label        TEXT,
  added_by     UUID NOT NULL REFERENCES users(id),
  added_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- Usable from here on. Set by the service from the cooling-off period in
  -- force when the entry was added; never moved earlier afterwards.
  active_from  TIMESTAMPTZ NOT NULL,
  removed_at   TIMESTAMPTZ,
  removed_by   UUID REFERENCES users(id)
);

-- One live entry per address; a removed one can be added again as a new row.
CREATE UNIQUE INDEX IF NOT EXISTS idx_address_whitelist_live
  ON address_whitelist (customer_id, blockchain, address) WHERE removed_at IS NULL;

-- An entry's address and activation time never change: the only edit is
-- removing it. Entries are never deleted, so the history of what was ever
-- allowed stays.
CREATE OR REPLACE FUNCTION address_whitelist_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'whitelist entries are never deleted; remove them instead' USING ERRCODE = 'OFB05';
  END IF;
  IF NEW.customer_id IS DISTINCT FROM OLD.customer_id
     OR NEW.blockchain IS DISTINCT FROM OLD.blockchain
     OR NEW.address IS DISTINCT FROM OLD.address
     OR NEW.added_by IS DISTINCT FROM OLD.added_by
     OR NEW.added_at IS DISTINCT FROM OLD.added_at
     OR NEW.active_from IS DISTINCT FROM OLD.active_from THEN
    RAISE EXCEPTION 'a whitelist entry cannot be changed, only removed' USING ERRCODE = 'OFB05';
  END IF;
  IF OLD.removed_at IS NOT NULL THEN
    RAISE EXCEPTION 'a removed whitelist entry stays removed' USING ERRCODE = 'OFB05';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS address_whitelist_guard ON address_whitelist;
CREATE TRIGGER address_whitelist_guard BEFORE UPDATE OR DELETE ON address_whitelist
  FOR EACH ROW EXECUTE FUNCTION address_whitelist_guard();

ALTER TABLE org_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_controls FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON org_controls;
CREATE POLICY tenant_isolation ON org_controls
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

ALTER TABLE address_whitelist ENABLE ROW LEVEL SECURITY;
ALTER TABLE address_whitelist FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON address_whitelist;
CREATE POLICY tenant_isolation ON address_whitelist
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

GRANT SELECT, INSERT, UPDATE ON org_controls TO app_admin;
GRANT SELECT, INSERT, UPDATE ON address_whitelist TO app_admin;
