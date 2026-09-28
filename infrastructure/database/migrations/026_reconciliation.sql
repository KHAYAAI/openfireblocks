-- Reconciliation runs.
--
-- The platform's ledger says what it signed. The chain says what happened.
-- A customer's own books say what they think happened. A reconciliation
-- run compares them and records every disagreement -- a "break" -- with
-- the evidence. The most serious break is a transaction from one of the
-- organisation's addresses that the platform never signed: it means a
-- key is being used somewhere else.
--
-- Runs are immutable once written: a reconciliation is evidence of what
-- was known at a time, and one that can be edited afterwards is not.

CREATE TABLE IF NOT EXISTS reconciliation_runs (
  run_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id  UUID NOT NULL REFERENCES customers(customer_id),
  kind         VARCHAR(10) NOT NULL CHECK (kind IN ('chain', 'statement')),
  chain_id     INT,
  started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  finished_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  -- Counts per classification, and whether anything needs a person.
  summary      JSONB NOT NULL,
  -- One entry per disagreement, each with its evidence.
  breaks       JSONB NOT NULL,
  requested_by VARCHAR(255) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_runs_customer ON reconciliation_runs(customer_id, started_at DESC);

CREATE OR REPLACE FUNCTION reconciliation_runs_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'reconciliation runs are evidence and cannot be changed or deleted' USING ERRCODE = 'OFB04';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS reconciliation_runs_immutable ON reconciliation_runs;
CREATE TRIGGER reconciliation_runs_immutable BEFORE UPDATE OR DELETE ON reconciliation_runs
  FOR EACH ROW EXECUTE FUNCTION reconciliation_runs_immutable();

ALTER TABLE reconciliation_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE reconciliation_runs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON reconciliation_runs;
CREATE POLICY tenant_isolation ON reconciliation_runs
  USING (customer_id = current_customer_id()) WITH CHECK (customer_id = current_customer_id());

GRANT SELECT ON reconciliation_runs TO app_admin;
