-- Transfers that wait for approval before they are signed.
--
-- Settlements reach the approval queue through a Temporal workflow. Transfers
-- from a threshold key on Bitcoin, Solana, Cosmos and EVM chains are signed by
-- the gateway directly, so they have no workflow to park in. This table is
-- where such a transfer waits: the request as the person made it, linked to
-- its approval request, until the approvers decide.
--
-- The approval request itself (who may decide, how many, the append-only
-- decisions, segregation of duties) is migration 023's and is not repeated
-- here. This table adds only what is needed to act on the outcome.

CREATE TABLE IF NOT EXISTS pending_transfers (
  approval_id  UUID PRIMARY KEY REFERENCES approval_requests(approval_id),
  customer_id  UUID NOT NULL REFERENCES customers(customer_id),
  key_id       UUID NOT NULL,
  kind         VARCHAR(20) NOT NULL CHECK (kind IN ('bitcoin', 'solana', 'cosmos', 'evm')),
  -- The transfer as asked for. Never changes: what the approvers approved is
  -- exactly what gets executed.
  request      JSONB NOT NULL,
  -- Execution details chosen when it first ran (an EVM nonce and fees), kept
  -- so a retry after a failure signs the same transaction instead of a
  -- different one under the same idempotency key.
  prepared     JSONB,
  status       VARCHAR(20) NOT NULL DEFAULT 'awaiting_approval'
                 CHECK (status IN ('awaiting_approval', 'executing', 'completed', 'failed', 'rejected', 'expired')),
  result       JSONB,
  error        TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pending_transfers_customer_status
  ON pending_transfers(customer_id, status, created_at DESC);

-- The request is fixed at creation, and status only moves along the lifecycle:
-- waiting -> executing -> completed | failed, with failed -> executing for a
-- retry, and waiting -> rejected | expired. "Executing" is also the claim: the
-- UPDATE that sets it succeeds for exactly one caller, which is what stops a
-- repeated decision delivery from sending the transfer twice.
CREATE OR REPLACE FUNCTION pending_transfer_guard() RETURNS trigger AS $$
BEGIN
  IF NEW.customer_id IS DISTINCT FROM OLD.customer_id
     OR NEW.key_id IS DISTINCT FROM OLD.key_id
     OR NEW.kind IS DISTINCT FROM OLD.kind
     OR NEW.request IS DISTINCT FROM OLD.request
     OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'a pending transfer''s terms cannot change after it opens' USING ERRCODE = 'OFB04';
  END IF;
  IF NEW.status IS DISTINCT FROM OLD.status AND NOT (
       (OLD.status = 'awaiting_approval' AND NEW.status IN ('executing', 'rejected', 'expired'))
    OR (OLD.status = 'executing' AND NEW.status IN ('completed', 'failed'))
    OR (OLD.status = 'failed' AND NEW.status = 'executing')
  ) THEN
    RAISE EXCEPTION 'a transfer cannot go from % to %', OLD.status, NEW.status USING ERRCODE = 'OFB03';
  END IF;
  -- Executing needs the approval to have reached quorum: the status of the
  -- request is the proof, and it is itself guarded by migration 023.
  IF NEW.status = 'executing' AND OLD.status = 'awaiting_approval' THEN
    IF (SELECT status FROM approval_requests WHERE approval_id = NEW.approval_id) <> 'approved' THEN
      RAISE EXCEPTION 'cannot execute a transfer whose approval is not approved' USING ERRCODE = 'OFB01';
    END IF;
  END IF;
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS pending_transfer_guard ON pending_transfers;
CREATE TRIGGER pending_transfer_guard BEFORE UPDATE ON pending_transfers
  FOR EACH ROW EXECUTE FUNCTION pending_transfer_guard();

DROP TRIGGER IF EXISTS pending_transfer_no_delete ON pending_transfers;
CREATE TRIGGER pending_transfer_no_delete BEFORE DELETE ON pending_transfers
  FOR EACH ROW EXECUTE FUNCTION approval_decision_immutable();

ALTER TABLE pending_transfers ENABLE ROW LEVEL SECURITY;
ALTER TABLE pending_transfers FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON pending_transfers;
CREATE POLICY tenant_isolation ON pending_transfers
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

GRANT SELECT, INSERT, UPDATE ON pending_transfers TO app_admin;
