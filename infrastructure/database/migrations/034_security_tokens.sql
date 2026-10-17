-- Tokenisation: permissioned (security) tokens issued from an organisation's threshold key.
--
-- The chain is the source of truth for who holds what, who is admitted and who is frozen;
-- these tables hold only what the chain cannot: which token belongs to which organisation,
-- who the holders are in the issuer's own books (and the reference to the KYC the issuer
-- did on them -- never the documents), and a record of every administrative act that was
-- asked for.
CREATE TABLE IF NOT EXISTS security_tokens (
  token_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id       UUID NOT NULL REFERENCES customers(customer_id),
  chain_id          INTEGER NOT NULL CHECK (chain_id > 0),
  contract_address  TEXT NOT NULL CHECK (contract_address ~ '^0x[0-9a-f]{40}$'),
  -- Read from the contract when it was registered, never supplied by the caller.
  name              TEXT NOT NULL,
  symbol            TEXT NOT NULL,
  decimals          INTEGER NOT NULL CHECK (decimals BETWEEN 0 AND 36),
  supply_cap        NUMERIC(78,0) NOT NULL CHECK (supply_cap > 0),
  -- The threshold key that owns the contract: every administrative act is signed by it.
  issuer_key_id     UUID NOT NULL,
  created_by        UUID NOT NULL REFERENCES users(id),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (chain_id, contract_address)
);

CREATE TABLE IF NOT EXISTS security_token_holders (
  holder_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  token_id        UUID NOT NULL REFERENCES security_tokens(token_id),
  customer_id     UUID NOT NULL REFERENCES customers(customer_id),
  display_name    TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 200),
  wallet_address  TEXT NOT NULL CHECK (wallet_address ~ '^0x[0-9a-f]{40}$'),
  -- Where the issuer's own KYC/AML file for this holder is. Required before the holder
  -- can be admitted on chain: a holder nobody has verified is not admitted.
  kyc_reference   TEXT NOT NULL CHECK (length(kyc_reference) BETWEEN 1 AND 500),
  created_by      UUID NOT NULL REFERENCES users(id),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (token_id, wallet_address)
);

-- A register of every administrative act asked for, linked to its approval. What happened
-- to each (waiting, approved and sent, rejected, failed) is read from the approval.
CREATE TABLE IF NOT EXISTS security_token_ops (
  op_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  token_id      UUID NOT NULL REFERENCES security_tokens(token_id),
  customer_id   UUID NOT NULL REFERENCES customers(customer_id),
  op            VARCHAR(20) NOT NULL CHECK (op IN ('admit', 'remove', 'freeze', 'unfreeze', 'pause', 'unpause', 'mint', 'burn', 'force_transfer', 'propose_owner')),
  params        JSONB NOT NULL,
  description   TEXT NOT NULL,
  approval_id   UUID NOT NULL,
  requested_by  UUID REFERENCES users(id),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_security_token_ops_token ON security_token_ops (token_id, created_at DESC);

-- What a token is, and what an act asked for, never changes; the register is evidence.
CREATE OR REPLACE FUNCTION security_tokens_fixed() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'security token records are never edited or deleted' USING ERRCODE = 'OFB09';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS security_tokens_fixed ON security_tokens;
CREATE TRIGGER security_tokens_fixed BEFORE UPDATE OR DELETE ON security_tokens FOR EACH ROW EXECUTE FUNCTION security_tokens_fixed();
DROP TRIGGER IF EXISTS security_token_ops_fixed ON security_token_ops;
CREATE TRIGGER security_token_ops_fixed BEFORE UPDATE OR DELETE ON security_token_ops FOR EACH ROW EXECUTE FUNCTION security_tokens_fixed();
DROP TRIGGER IF EXISTS security_token_holders_fixed ON security_token_holders;
CREATE TRIGGER security_token_holders_fixed BEFORE UPDATE OR DELETE ON security_token_holders FOR EACH ROW EXECUTE FUNCTION security_tokens_fixed();

ALTER TABLE security_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_tokens FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON security_tokens;
CREATE POLICY tenant_isolation ON security_tokens USING (customer_id = current_customer_id()) WITH CHECK (customer_id = current_customer_id());

ALTER TABLE security_token_holders ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_token_holders FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON security_token_holders;
CREATE POLICY tenant_isolation ON security_token_holders USING (customer_id = current_customer_id()) WITH CHECK (customer_id = current_customer_id());

ALTER TABLE security_token_ops ENABLE ROW LEVEL SECURITY;
ALTER TABLE security_token_ops FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON security_token_ops;
CREATE POLICY tenant_isolation ON security_token_ops USING (customer_id = current_customer_id()) WITH CHECK (customer_id = current_customer_id());

GRANT SELECT, INSERT ON security_tokens TO app_admin;
GRANT SELECT, INSERT ON security_token_holders TO app_admin;
GRANT SELECT, INSERT ON security_token_ops TO app_admin;
