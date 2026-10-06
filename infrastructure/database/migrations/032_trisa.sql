-- TRISA: Travel Rule information exchanged directly with other virtual-asset service
-- providers over mutual-TLS gRPC, in sealed envelopes only the recipient can open.
--
-- Counterparties are whom this organisation is willing to send customer information to.
-- Sending personal data to the wrong party is a data breach, so a counterparty is
-- trusted only by a second person: whoever adds one cannot trust it (the same
-- separation of duties as approving a transfer).
CREATE TABLE IF NOT EXISTS trisa_counterparties (
  counterparty_id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id            UUID NOT NULL REFERENCES customers(customer_id),
  name                   TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  lei                    TEXT CHECK (lei IS NULL OR lei ~ '^[A-Z0-9]{20}$'),
  -- host:port of the counterparty's TRISA endpoint.
  endpoint               TEXT NOT NULL CHECK (length(endpoint) BETWEEN 3 AND 255),
  -- The common name of the certificate the counterparty presents on mutual TLS.
  common_name            TEXT,
  -- Their RSA sealing key (PKIX PEM) and its TRISA public-key signature.
  sealing_public_key_pem TEXT,
  sealing_key_signature  TEXT,
  status                 VARCHAR(10) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'trusted', 'revoked')),
  added_by               UUID NOT NULL REFERENCES users(id),
  added_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  trusted_by             UUID REFERENCES users(id),
  trusted_at             TIMESTAMPTZ,
  UNIQUE (customer_id, endpoint)
);

-- Trust needs a second person, and what was trusted cannot then be swapped: a changed
-- endpoint or key is a different counterparty, which has to be trusted afresh.
CREATE OR REPLACE FUNCTION trisa_counterparties_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'counterparties are never deleted; revoke them' USING ERRCODE = 'OFB07';
  END IF;
  IF OLD.status = 'revoked' AND NEW.status IS DISTINCT FROM 'revoked' THEN
    RAISE EXCEPTION 'a revoked counterparty stays revoked' USING ERRCODE = 'OFB07';
  END IF;
  IF NEW.status = 'trusted' AND OLD.status IS DISTINCT FROM 'trusted' THEN
    IF NEW.trusted_by IS NULL OR NEW.trusted_by = NEW.added_by THEN
      RAISE EXCEPTION 'a counterparty must be trusted by someone other than the person who added it' USING ERRCODE = 'OFB07';
    END IF;
    IF NEW.sealing_public_key_pem IS NULL THEN
      RAISE EXCEPTION 'a counterparty without a sealing key cannot be trusted' USING ERRCODE = 'OFB07';
    END IF;
  END IF;
  IF OLD.status = 'trusted' AND NEW.status = 'trusted' AND (
       NEW.endpoint IS DISTINCT FROM OLD.endpoint
    OR NEW.common_name IS DISTINCT FROM OLD.common_name
    OR NEW.sealing_public_key_pem IS DISTINCT FROM OLD.sealing_public_key_pem
    OR NEW.lei IS DISTINCT FROM OLD.lei) THEN
    RAISE EXCEPTION 'a trusted counterparty cannot be changed; revoke it and add a new one' USING ERRCODE = 'OFB07';
  END IF;
  IF NEW.customer_id IS DISTINCT FROM OLD.customer_id OR NEW.added_by IS DISTINCT FROM OLD.added_by THEN
    RAISE EXCEPTION 'who added a counterparty, and for whom, cannot change' USING ERRCODE = 'OFB07';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trisa_counterparties_guard ON trisa_counterparties;
CREATE TRIGGER trisa_counterparties_guard BEFORE UPDATE OR DELETE ON trisa_counterparties
  FOR EACH ROW EXECUTE FUNCTION trisa_counterparties_guard();

-- Travel Rule information received from another provider about a transfer to one of our
-- customers' addresses. customer_id is null when no organisation owns the address; such
-- rows are visible to no tenant.
CREATE TABLE IF NOT EXISTS trisa_inbound (
  inbound_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id         UUID REFERENCES customers(customer_id),
  envelope_id         TEXT NOT NULL,
  peer_common_name    TEXT,
  peer_fingerprint    TEXT NOT NULL,
  originating_vasp    TEXT,
  originator_account  TEXT,
  beneficiary_account TEXT,
  identity            JSONB NOT NULL,
  transaction         JSONB,
  status              VARCHAR(10) NOT NULL CHECK (status IN ('accepted', 'rejected')),
  reject_reason       TEXT,
  received_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- The same envelope delivered twice from the same peer is one record.
  UNIQUE (envelope_id, peer_fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_trisa_inbound_customer ON trisa_inbound (customer_id, received_at DESC);

-- Received information is evidence. It is never edited or deleted.
CREATE OR REPLACE FUNCTION trisa_inbound_guard() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'received Travel Rule information is never edited or deleted' USING ERRCODE = 'OFB07';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trisa_inbound_guard ON trisa_inbound;
CREATE TRIGGER trisa_inbound_guard BEFORE UPDATE OR DELETE ON trisa_inbound
  FOR EACH ROW EXECUTE FUNCTION trisa_inbound_guard();

ALTER TABLE trisa_counterparties ENABLE ROW LEVEL SECURITY;
ALTER TABLE trisa_counterparties FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON trisa_counterparties;
CREATE POLICY tenant_isolation ON trisa_counterparties
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

ALTER TABLE trisa_inbound ENABLE ROW LEVEL SECURITY;
ALTER TABLE trisa_inbound FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON trisa_inbound;
CREATE POLICY tenant_isolation ON trisa_inbound
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

GRANT SELECT, INSERT, UPDATE ON trisa_counterparties TO app_admin;
GRANT SELECT, INSERT ON trisa_inbound TO app_admin;
GRANT SELECT ON key_pairs TO app_admin;
