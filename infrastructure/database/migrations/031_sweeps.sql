-- Deposit sweeps: move what has accumulated on a deposit key to a treasury
-- address, on a rule an administrator set.
--
-- A sweep is not a privileged path. Each run is submitted as an ordinary
-- transfer, so the organisation's freeze, address whitelist, spending policy
-- and approval quorum apply to it exactly as they do to a person's transfer.
CREATE TABLE IF NOT EXISTS sweep_rules (
  rule_id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id       UUID NOT NULL REFERENCES customers(customer_id),
  name              TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
  key_id            UUID NOT NULL,
  blockchain        VARCHAR(20) NOT NULL CHECK (blockchain IN ('ethereum', 'polygon', 'solana', 'cosmos')),
  chain_id          INTEGER,
  destination       TEXT NOT NULL CHECK (length(destination) BETWEEN 8 AND 128),
  -- Base units. A run sweeps (balance - reserve) when that is at least min_amount.
  min_amount        NUMERIC(78,0) NOT NULL CHECK (min_amount > 0),
  reserve           NUMERIC(78,0) NOT NULL DEFAULT 0 CHECK (reserve >= 0),
  -- Travel Rule details sent with every run, for a sweep to an address the
  -- Travel Rule applies to. Optional: a sweep to the organisation's own
  -- treasury below the threshold needs none.
  travel_rule       JSONB,
  interval_seconds  INTEGER NOT NULL DEFAULT 3600 CHECK (interval_seconds BETWEEN 60 AND 604800),
  enabled           BOOLEAN NOT NULL DEFAULT true,
  created_by        UUID NOT NULL REFERENCES users(id),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- Claimed by the scheduler with a conditional update, so replicas cannot
  -- both run the same rule in the same interval.
  last_claimed_at   TIMESTAMPTZ,
  deleted_at        TIMESTAMPTZ,
  CHECK (blockchain NOT IN ('ethereum', 'polygon') OR chain_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_sweep_rules_customer ON sweep_rules (customer_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS sweep_runs (
  run_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  rule_id      UUID NOT NULL REFERENCES sweep_rules(rule_id),
  customer_id  UUID NOT NULL REFERENCES customers(customer_id),
  trigger      VARCHAR(10) NOT NULL CHECK (trigger IN ('manual', 'schedule')),
  status       VARCHAR(20) NOT NULL
                 CHECK (status IN ('skipped', 'completed', 'pending_approval', 'refused', 'failed')),
  balance      NUMERIC(78,0),
  amount       NUMERIC(78,0),
  reason       TEXT,
  approval_id  UUID,
  result       JSONB,
  error        TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sweep_runs_rule ON sweep_runs (rule_id, created_at DESC);

-- Where a rule sends money and from what is fixed once created. Pointing an
-- existing rule at a different address would be the quiet way to redirect a
-- standing sweep, so the only edits are switching it off and deleting it; a
-- different destination is a new rule, which is a new, visible, audited act.
CREATE OR REPLACE FUNCTION sweep_rules_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'sweep rules are never deleted; delete marks them' USING ERRCODE = 'OFB06';
  END IF;
  IF NEW.customer_id IS DISTINCT FROM OLD.customer_id
     OR NEW.key_id IS DISTINCT FROM OLD.key_id
     OR NEW.blockchain IS DISTINCT FROM OLD.blockchain
     OR NEW.chain_id IS DISTINCT FROM OLD.chain_id
     OR NEW.destination IS DISTINCT FROM OLD.destination
     OR NEW.min_amount IS DISTINCT FROM OLD.min_amount
     OR NEW.reserve IS DISTINCT FROM OLD.reserve
     OR NEW.travel_rule IS DISTINCT FROM OLD.travel_rule
     OR NEW.created_by IS DISTINCT FROM OLD.created_by THEN
    RAISE EXCEPTION 'a sweep rule cannot be edited; switch it off and create a new one' USING ERRCODE = 'OFB06';
  END IF;
  IF OLD.deleted_at IS NOT NULL THEN
    RAISE EXCEPTION 'a deleted sweep rule stays deleted' USING ERRCODE = 'OFB06';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS sweep_rules_guard ON sweep_rules;
CREATE TRIGGER sweep_rules_guard BEFORE UPDATE OR DELETE ON sweep_rules
  FOR EACH ROW EXECUTE FUNCTION sweep_rules_guard();

ALTER TABLE sweep_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE sweep_rules FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON sweep_rules;
CREATE POLICY tenant_isolation ON sweep_rules
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

ALTER TABLE sweep_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE sweep_runs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON sweep_runs;
CREATE POLICY tenant_isolation ON sweep_runs
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

GRANT SELECT, INSERT, UPDATE ON sweep_rules TO app_admin;
GRANT SELECT, INSERT, UPDATE ON sweep_runs TO app_admin;
