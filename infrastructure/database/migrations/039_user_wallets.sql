-- Per-user wallets: wallet-as-a-service for a customer's own end users.
--
-- A customer (a fintech, an exchange, a payroll provider) holds funds for its
-- users and wants each user's funds at that user's own addresses. A user wallet
-- is a set of threshold keys, one per chain, under the customer, labelled
-- with the customer's own reference for the user.
--
-- Each key is a real threshold key with its own key ceremony, not an address
-- derived from a shared key: the platform's signing parties do no address
-- derivation, and adding it would change the reviewed cryptography. The cost
-- is one key ceremony per user per chain, which is why wallets are created
-- per chain on request rather than for every chain at once.
--
-- The platform holds no personal information about the end user. external_ref
-- is the customer's own identifier (an account number, a UUID); the customer
-- is told not to put names, emails or ID numbers in it.

CREATE TABLE IF NOT EXISTS user_wallets (
  wallet_id     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id   UUID NOT NULL REFERENCES customers(customer_id),
  external_ref  TEXT NOT NULL CHECK (external_ref ~ '^[A-Za-z0-9._:-]{1,128}$'),
  label         TEXT CHECK (label IS NULL OR length(label) <= 120),
  status        VARCHAR(10) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'frozen')),
  status_reason TEXT CHECK (status_reason IS NULL OR length(status_reason) <= 500),
  status_changed_at TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (customer_id, external_ref)
);
CREATE INDEX IF NOT EXISTS idx_user_wallets_customer ON user_wallets (customer_id, created_at DESC);

-- One key per chain per wallet; a key belongs to at most one wallet.
CREATE TABLE IF NOT EXISTS user_wallet_keys (
  wallet_id   UUID NOT NULL REFERENCES user_wallets(wallet_id),
  customer_id UUID NOT NULL REFERENCES customers(customer_id),
  blockchain  VARCHAR(50) NOT NULL,
  key_id      UUID NOT NULL UNIQUE REFERENCES key_pairs(key_id),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (wallet_id, blockchain)
);

-- Who a wallet belongs to never changes, and nothing is deleted: a wallet that
-- held funds is part of the record. Only the status (and its reason) and the
-- label may change.
CREATE OR REPLACE FUNCTION user_wallets_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'user wallets are never deleted; freeze them' USING ERRCODE = 'OFB10';
  END IF;
  IF NEW.wallet_id IS DISTINCT FROM OLD.wallet_id
     OR NEW.customer_id IS DISTINCT FROM OLD.customer_id
     OR NEW.external_ref IS DISTINCT FROM OLD.external_ref
     OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'a user wallet''s owner and reference cannot be changed' USING ERRCODE = 'OFB10';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS user_wallets_guard ON user_wallets;
CREATE TRIGGER user_wallets_guard BEFORE UPDATE OR DELETE ON user_wallets
  FOR EACH ROW EXECUTE FUNCTION user_wallets_guard();

CREATE OR REPLACE FUNCTION user_wallet_keys_guard() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'a wallet''s keys are fixed once linked' USING ERRCODE = 'OFB10';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS user_wallet_keys_guard ON user_wallet_keys;
CREATE TRIGGER user_wallet_keys_guard BEFORE UPDATE OR DELETE ON user_wallet_keys
  FOR EACH ROW EXECUTE FUNCTION user_wallet_keys_guard();

-- A key may only be linked to a wallet of the same customer.
CREATE OR REPLACE FUNCTION user_wallet_keys_same_owner() RETURNS trigger AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM user_wallets w WHERE w.wallet_id = NEW.wallet_id AND w.customer_id = NEW.customer_id)
     OR NOT EXISTS (SELECT 1 FROM key_pairs k WHERE k.key_id = NEW.key_id AND k.customer_id = NEW.customer_id
                    AND k.blockchain = NEW.blockchain) THEN
    RAISE EXCEPTION 'the wallet and the key must belong to the same organisation, and the key to the stated chain'
      USING ERRCODE = 'OFB10';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS user_wallet_keys_same_owner ON user_wallet_keys;
CREATE TRIGGER user_wallet_keys_same_owner BEFORE INSERT ON user_wallet_keys
  FOR EACH ROW EXECUTE FUNCTION user_wallet_keys_same_owner();

ALTER TABLE user_wallets ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_wallets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON user_wallets;
CREATE POLICY tenant_isolation ON user_wallets
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

ALTER TABLE user_wallet_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_wallet_keys FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON user_wallet_keys;
CREATE POLICY tenant_isolation ON user_wallet_keys
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

GRANT SELECT ON user_wallets, user_wallet_keys TO app_admin;
