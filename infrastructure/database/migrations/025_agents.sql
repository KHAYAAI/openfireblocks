-- AI agents with budgets.
--
-- An agent -- a procurement bot, a treasury rebalancer, an AI assistant
-- -- can be given the ability to pay, and only that: from one key, in
-- named tokens, to named recipients if the organisation chooses, within a
-- per-transfer limit and a rolling 24-hour budget. It gets its own
-- credential, stored here and not in customers, so an agent key cannot
-- authenticate anywhere an organisation's API key can. What it can do is
-- one route: send a transfer, which still passes policy, the Travel Rule
-- and signing like any other.
--
-- The agent proposes; the platform decides. Intelligence and financial
-- authority stay separate.

CREATE TABLE IF NOT EXISTS agents (
  agent_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id   UUID NOT NULL REFERENCES customers(customer_id),
  name          VARCHAR(120) NOT NULL,
  -- The one threshold key this agent may spend from.
  key_id        UUID NOT NULL,
  api_key_hash  BYTEA NOT NULL UNIQUE,
  -- Token symbols the agent may send. Budgets are in rand, so only tokens
  -- the platform can value (rand- or dollar-pegged) can be listed usefully;
  -- the gateway refuses the rest at spend time.
  allowed_tokens     TEXT[] NOT NULL CHECK (cardinality(allowed_tokens) > 0),
  -- Lower-cased addresses. NULL means any recipient policy allows.
  allowed_recipients TEXT[],
  per_transfer_limit_zar NUMERIC(24, 2) NOT NULL CHECK (per_transfer_limit_zar > 0),
  daily_limit_zar        NUMERIC(24, 2) NOT NULL CHECK (daily_limit_zar > 0),
  status        VARCHAR(10) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
  expires_at    TIMESTAMPTZ NOT NULL,
  created_by    UUID NOT NULL REFERENCES users(id),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  revoked_by    UUID REFERENCES users(id),
  revoked_at    TIMESTAMPTZ,
  CHECK (per_transfer_limit_zar <= daily_limit_zar),
  CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS idx_agents_customer ON agents(customer_id, status);

-- Every attempt, including the refused ones. This is the agent's track
-- record: what it asked for, what it was allowed, and why it was refused.
CREATE TABLE IF NOT EXISTS agent_spend (
  spend_id     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id     UUID NOT NULL REFERENCES agents(agent_id),
  customer_id  UUID NOT NULL REFERENCES customers(customer_id),
  request_id   VARCHAR(255) NOT NULL,
  token        VARCHAR(32) NOT NULL,
  recipient    VARCHAR(128) NOT NULL,
  amount       VARCHAR(80) NOT NULL,
  value_zar    NUMERIC(24, 2),
  -- reserved: counted against the budget while the transfer is signed.
  -- spent: signed. released: signing failed, budget returned.
  -- refused: never reserved; reason says why.
  status       VARCHAR(10) NOT NULL CHECK (status IN ('reserved', 'spent', 'released', 'refused')),
  reason       TEXT,
  purpose      TEXT,
  tx_hash      VARCHAR(80),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  settled_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_spend_window ON agent_spend(agent_id, created_at DESC);

-- Spend records move reserved -> spent | released, and nothing else
-- changes. A record of what an agent did is not something to edit.
CREATE OR REPLACE FUNCTION agent_spend_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'agent spend records are retained' USING ERRCODE = 'OFB04';
  END IF;
  IF (NEW.agent_id, NEW.customer_id, NEW.request_id, NEW.token, NEW.recipient, NEW.amount,
      NEW.value_zar, NEW.purpose, NEW.created_at)
     IS DISTINCT FROM
     (OLD.agent_id, OLD.customer_id, OLD.request_id, OLD.token, OLD.recipient, OLD.amount,
      OLD.value_zar, OLD.purpose, OLD.created_at) THEN
    RAISE EXCEPTION 'an agent spend record cannot be changed' USING ERRCODE = 'OFB04';
  END IF;
  IF NOT (OLD.status = 'reserved' AND NEW.status IN ('spent', 'released')) AND NEW.status IS DISTINCT FROM OLD.status THEN
    RAISE EXCEPTION 'agent spend cannot move from % to %', OLD.status, NEW.status USING ERRCODE = 'OFB03';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS agent_spend_guard ON agent_spend;
CREATE TRIGGER agent_spend_guard BEFORE UPDATE OR DELETE ON agent_spend
  FOR EACH ROW EXECUTE FUNCTION agent_spend_guard();

-- A revoked agent stays revoked, and its limits cannot be raised while
-- it holds a live credential -- create a new agent for new terms, so the
-- record shows who granted what, when.
CREATE OR REPLACE FUNCTION agents_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'agents are revoked, not deleted' USING ERRCODE = 'OFB04';
  END IF;
  IF OLD.status = 'revoked' AND NEW.status <> 'revoked' THEN
    RAISE EXCEPTION 'a revoked agent cannot be reactivated' USING ERRCODE = 'OFB03';
  END IF;
  IF (NEW.customer_id, NEW.key_id, NEW.api_key_hash, NEW.allowed_tokens, NEW.allowed_recipients,
      NEW.per_transfer_limit_zar, NEW.daily_limit_zar, NEW.expires_at, NEW.created_by, NEW.created_at)
     IS DISTINCT FROM
     (OLD.customer_id, OLD.key_id, OLD.api_key_hash, OLD.allowed_tokens, OLD.allowed_recipients,
      OLD.per_transfer_limit_zar, OLD.daily_limit_zar, OLD.expires_at, OLD.created_by, OLD.created_at) THEN
    RAISE EXCEPTION 'an agent''s terms cannot be changed; revoke it and create another' USING ERRCODE = 'OFB04';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS agents_guard ON agents;
CREATE TRIGGER agents_guard BEFORE UPDATE OR DELETE ON agents
  FOR EACH ROW EXECUTE FUNCTION agents_guard();

ALTER TABLE agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE agents FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON agents;
CREATE POLICY tenant_isolation ON agents
  USING (customer_id = current_customer_id()) WITH CHECK (customer_id = current_customer_id());

ALTER TABLE agent_spend ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_spend FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON agent_spend;
CREATE POLICY tenant_isolation ON agent_spend
  USING (customer_id = current_customer_id()) WITH CHECK (customer_id = current_customer_id());

-- app_admin resolves an agent from its key before any tenant is known,
-- exactly as it does for customers.
GRANT SELECT ON agents TO app_admin;
GRANT SELECT ON agent_spend TO app_admin;
