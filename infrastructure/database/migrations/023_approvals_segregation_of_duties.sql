-- Approvals with segregation of duties.
--
-- Until now a transfer the policy engine marked "requires approval" waited
-- for one signal carrying one boolean, sent by whoever held the tenant's
-- API key. That is an approval step in name only: the same key that
-- started a transfer could approve it, there was no record of which person
-- approved, and one approval was always enough. Dual control -- the thing
-- a bank's control function actually signs off -- was not expressible.
--
-- This migration makes the database itself enforce the rules, so they hold
-- no matter which code path writes a decision:
--
--   1. The person who initiated a transfer cannot approve it.
--   2. Only a named person holding the approver or admin role for that
--      tenant can decide at all.
--   3. Each person decides once. Decisions are append-only.
--   4. A transfer is approved only when the required number of *distinct*
--      people have approved it; one rejection rejects it.
--   5. Nothing can be decided after the request expires or closes.
--   6. The number of approvals a request needs is fixed when it opens.
--      Lowering the tenant's policy afterwards does not lower the bar for
--      a transfer already waiting.
--
-- The gateway checks the same things first, to give a useful error, and
-- the settlement workflow counts approvals again before it signs. Three
-- layers, because this is the control that decides whether money moves.

-- ---------------------------------------------------------------------
-- Roles
-- ---------------------------------------------------------------------
--
--   admin          manages members and the approval policy; may approve
--   approver       may approve or reject transfers; may not initiate
--   operator       may initiate transfers; may not approve
--   auditor        read-only, including every decision and who made it
--   viewer         read-only
--   billing_admin  billing only
--   user           legacy; treated as operator
ALTER TABLE user_customer_roles DROP CONSTRAINT IF EXISTS user_customer_roles_role_check;
ALTER TABLE user_customer_roles ADD CONSTRAINT user_customer_roles_role_check
  CHECK (role IN ('admin', 'approver', 'operator', 'auditor', 'viewer', 'billing_admin', 'user'));

-- ---------------------------------------------------------------------
-- The tenant's policy
-- ---------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS approval_policies (
  customer_id        UUID PRIMARY KEY REFERENCES customers(customer_id) ON DELETE CASCADE,
  -- Two by default: dual control. One is allowed, because a small team may
  -- genuinely have one approver, but it is a choice someone has to make
  -- and it is recorded against their name in updated_by.
  required_approvals INT NOT NULL DEFAULT 2 CHECK (required_approvals BETWEEN 1 AND 10),
  window_minutes     INT NOT NULL DEFAULT 60 CHECK (window_minutes BETWEEN 5 AND 10080),
  updated_by         UUID REFERENCES users(id),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------
-- Requests and decisions
-- ---------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS approval_requests (
  approval_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- No ON DELETE CASCADE, here or on decisions: the record of who approved
  -- which transfer outlives the tenant. Deleting a customer with approval
  -- history fails, which is the correct outcome for an audit record.
  customer_id          UUID NOT NULL REFERENCES customers(customer_id),
  -- One approval per settlement workflow. The worker opens it with
  -- ON CONFLICT DO NOTHING, so an activity retry cannot open a second.
  workflow_id          VARCHAR(255) NOT NULL UNIQUE,
  -- NULL when a machine (an API key) initiated the transfer: there is then
  -- no person to exclude, and every approver counts.
  initiated_by_user_id UUID REFERENCES users(id),
  initiated_by_label   VARCHAR(255) NOT NULL,
  required_approvals   INT NOT NULL CHECK (required_approvals BETWEEN 1 AND 10),
  status               VARCHAR(20) NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'approved', 'rejected', 'expired')),
  -- What is being approved: destination, amount, chain, and the policy
  -- reasons that asked for approval. An approver who cannot see what they
  -- are approving is not exercising control.
  summary              JSONB NOT NULL,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at           TIMESTAMPTZ NOT NULL,
  decided_at           TIMESTAMPTZ,
  CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS idx_approval_requests_customer_status
  ON approval_requests(customer_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS approval_decisions (
  approval_id UUID NOT NULL REFERENCES approval_requests(approval_id),
  customer_id UUID NOT NULL REFERENCES customers(customer_id),
  user_id     UUID NOT NULL REFERENCES users(id),
  decision    VARCHAR(10) NOT NULL CHECK (decision IN ('approve', 'reject')),
  reason      TEXT,
  -- How the approver proved who they were at the moment of deciding:
  -- 'totp' (a fresh one-time code), or 'sso' (the identity provider).
  -- Recorded so an auditor can see it, not only trust that it happened.
  step_up     VARCHAR(20) NOT NULL CHECK (step_up IN ('totp', 'sso')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (approval_id, user_id)
);

-- ---------------------------------------------------------------------
-- The rules, enforced here
-- ---------------------------------------------------------------------
--
-- Errors carry their own SQLSTATE so the gateway can map them to the
-- right HTTP status without parsing message text:
--   OFB01  segregation of duties (403)
--   OFB02  not authorised to decide (403)
--   OFB03  request is closed or expired (409)
--   OFB04  immutable record (409)

CREATE OR REPLACE FUNCTION approval_decision_guard() RETURNS trigger AS $$
DECLARE
  req  approval_requests%ROWTYPE;
  role_held VARCHAR(50);
BEGIN
  -- Lock the request so two approvers deciding at the same instant are
  -- serialised: the second sees the first's effect on status.
  SELECT * INTO req FROM approval_requests WHERE approval_id = NEW.approval_id FOR UPDATE;
  IF NOT FOUND OR req.customer_id <> NEW.customer_id THEN
    RAISE EXCEPTION 'approval request % not found', NEW.approval_id USING ERRCODE = 'OFB03';
  END IF;
  IF req.status <> 'pending' THEN
    RAISE EXCEPTION 'approval request is already %', req.status USING ERRCODE = 'OFB03';
  END IF;
  IF NOW() >= req.expires_at THEN
    RAISE EXCEPTION 'approval request expired at %', req.expires_at USING ERRCODE = 'OFB03';
  END IF;
  IF req.initiated_by_user_id IS NOT NULL AND req.initiated_by_user_id = NEW.user_id THEN
    RAISE EXCEPTION 'segregation of duties: the person who initiated a transfer cannot approve or reject it'
      USING ERRCODE = 'OFB01';
  END IF;

  SELECT role INTO role_held FROM user_customer_roles
   WHERE user_id = NEW.user_id AND customer_id = NEW.customer_id;
  IF role_held IS NULL OR role_held NOT IN ('approver', 'admin') THEN
    RAISE EXCEPTION 'only an approver or admin of this organisation can decide on a transfer'
      USING ERRCODE = 'OFB02';
  END IF;

  PERFORM 1 FROM users WHERE id = NEW.user_id AND status = 'active';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the deciding user is not active' USING ERRCODE = 'OFB02';
  END IF;

  NEW.created_at := NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION approval_decision_apply() RETURNS trigger AS $$
DECLARE
  approvals INT;
  needed    INT;
BEGIN
  IF NEW.decision = 'reject' THEN
    UPDATE approval_requests SET status = 'rejected', decided_at = NOW()
     WHERE approval_id = NEW.approval_id;
    RETURN NULL;
  END IF;

  SELECT COUNT(DISTINCT user_id) INTO approvals FROM approval_decisions
   WHERE approval_id = NEW.approval_id AND decision = 'approve';
  SELECT required_approvals INTO needed FROM approval_requests WHERE approval_id = NEW.approval_id;
  IF approvals >= needed THEN
    UPDATE approval_requests SET status = 'approved', decided_at = NOW()
     WHERE approval_id = NEW.approval_id;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Enforced by trigger rather than by withholding privileges, because
-- `app` owns these tables (see migration 011) and an owner's privileges
-- cannot be revoked in any way that sticks.
CREATE OR REPLACE FUNCTION approval_decision_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'approval records are append-only' USING ERRCODE = 'OFB04';
END;
$$ LANGUAGE plpgsql;

-- A request's terms cannot change after it opens, and its status only
-- moves out of pending, never back and never sideways.
CREATE OR REPLACE FUNCTION approval_request_guard() RETURNS trigger AS $$
BEGIN
  IF NEW.customer_id IS DISTINCT FROM OLD.customer_id
     OR NEW.workflow_id IS DISTINCT FROM OLD.workflow_id
     OR NEW.initiated_by_user_id IS DISTINCT FROM OLD.initiated_by_user_id
     OR NEW.required_approvals IS DISTINCT FROM OLD.required_approvals
     OR NEW.summary IS DISTINCT FROM OLD.summary
     OR NEW.created_at IS DISTINCT FROM OLD.created_at
     OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
    RAISE EXCEPTION 'an approval request''s terms cannot change after it opens' USING ERRCODE = 'OFB04';
  END IF;
  IF NEW.status IS DISTINCT FROM OLD.status AND OLD.status <> 'pending' THEN
    RAISE EXCEPTION 'approval request is already %', OLD.status USING ERRCODE = 'OFB03';
  END IF;
  -- Approved only if the decisions say so. A direct UPDATE setting
  -- status = 'approved' without the approvals is the bypass this refuses.
  IF NEW.status = 'approved' AND OLD.status = 'pending' THEN
    IF (SELECT COUNT(DISTINCT user_id) FROM approval_decisions
         WHERE approval_id = NEW.approval_id AND decision = 'approve') < NEW.required_approvals THEN
      RAISE EXCEPTION 'cannot mark approved: fewer than % distinct approvals', NEW.required_approvals
        USING ERRCODE = 'OFB01';
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS approval_decision_guard ON approval_decisions;
CREATE TRIGGER approval_decision_guard BEFORE INSERT ON approval_decisions
  FOR EACH ROW EXECUTE FUNCTION approval_decision_guard();

DROP TRIGGER IF EXISTS approval_decision_apply ON approval_decisions;
CREATE TRIGGER approval_decision_apply AFTER INSERT ON approval_decisions
  FOR EACH ROW EXECUTE FUNCTION approval_decision_apply();

DROP TRIGGER IF EXISTS approval_decision_immutable ON approval_decisions;
CREATE TRIGGER approval_decision_immutable BEFORE UPDATE OR DELETE ON approval_decisions
  FOR EACH ROW EXECUTE FUNCTION approval_decision_immutable();

DROP TRIGGER IF EXISTS approval_request_no_delete ON approval_requests;
CREATE TRIGGER approval_request_no_delete BEFORE DELETE ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION approval_decision_immutable();

DROP TRIGGER IF EXISTS approval_request_guard ON approval_requests;
CREATE TRIGGER approval_request_guard BEFORE UPDATE ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION approval_request_guard();

-- ---------------------------------------------------------------------
-- Tenant isolation, shaped exactly like migration 011's
-- ---------------------------------------------------------------------

ALTER TABLE approval_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_policies FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON approval_policies;
CREATE POLICY tenant_isolation ON approval_policies
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

ALTER TABLE approval_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_requests FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON approval_requests;
CREATE POLICY tenant_isolation ON approval_requests
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

ALTER TABLE approval_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_decisions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON approval_decisions;
CREATE POLICY tenant_isolation ON approval_decisions
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

GRANT SELECT, INSERT, UPDATE ON approval_policies, approval_requests TO app_admin;
GRANT SELECT, INSERT ON approval_decisions TO app_admin;
