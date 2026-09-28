-- Travel Rule records.
--
-- FIC Directive 9 (South Africa) and FATF Recommendation 16: a transfer
-- at or above the threshold must carry the originator's and the
-- beneficiary's information, in a form the receiving provider can read
-- (IVMS101), and both sides must keep it. Until now the platform knew the
-- threshold existed (services/compliance) and did nothing about it.
--
-- One row per outbound transfer that required Travel Rule information.
-- The information itself is immutable once recorded: what was declared at
-- the time of the transfer is what an inspector will ask for, and a record
-- that can be edited afterwards is not a record. Only the transmission
-- fields move, and only forwards.

CREATE TABLE IF NOT EXISTS travel_rule_records (
  record_id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id      UUID NOT NULL REFERENCES customers(customer_id),
  request_id       VARCHAR(255) NOT NULL,
  chain_id         INT NOT NULL,
  tx_hash          VARCHAR(80),
  asset            VARCHAR(32) NOT NULL,
  -- Base units, as a string: the exact amount, never a float.
  amount           NUMERIC(78, 0) NOT NULL,
  asset_decimals   INT NOT NULL,
  originator_address  VARCHAR(128) NOT NULL,
  beneficiary_address VARCHAR(128) NOT NULL,
  -- What the platform took the transfer to be worth, and how. NULL when
  -- it could not tell (a native asset with no price source): such a
  -- transfer is treated as over the threshold, which is why it is here.
  value_zar        NUMERIC(24, 2),
  valuation        VARCHAR(40) NOT NULL,
  threshold_zar    NUMERIC(24, 2) NOT NULL,
  -- The IVMS101 payload: originator, beneficiary, and the providers.
  ivms101          JSONB NOT NULL,
  -- Unhosted: the beneficiary controls the wallet themselves, so there is
  -- no provider to transmit to. The information is still collected and
  -- kept, which is what the directive requires in that case.
  beneficiary_unhosted BOOLEAN NOT NULL,
  transmission_status VARCHAR(24) NOT NULL
    CHECK (transmission_status IN ('awaiting_transmission', 'transmitted', 'failed', 'not_applicable_unhosted')),
  transmission_reference TEXT,
  transmission_error     TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  transmitted_at   TIMESTAMPTZ,
  UNIQUE (customer_id, request_id)
);

CREATE INDEX IF NOT EXISTS idx_travel_rule_customer_status
  ON travel_rule_records(customer_id, transmission_status, created_at DESC);

CREATE OR REPLACE FUNCTION travel_rule_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'travel rule records are retained, not deleted' USING ERRCODE = 'OFB04';
  END IF;
  IF (NEW.customer_id, NEW.request_id, NEW.chain_id, NEW.asset, NEW.amount, NEW.asset_decimals,
      NEW.originator_address, NEW.beneficiary_address, NEW.value_zar, NEW.valuation,
      NEW.threshold_zar, NEW.ivms101, NEW.beneficiary_unhosted, NEW.created_at)
     IS DISTINCT FROM
     (OLD.customer_id, OLD.request_id, OLD.chain_id, OLD.asset, OLD.amount, OLD.asset_decimals,
      OLD.originator_address, OLD.beneficiary_address, OLD.value_zar, OLD.valuation,
      OLD.threshold_zar, OLD.ivms101, OLD.beneficiary_unhosted, OLD.created_at) THEN
    RAISE EXCEPTION 'the information recorded for a transfer cannot be changed afterwards' USING ERRCODE = 'OFB04';
  END IF;
  -- The transaction hash may be filled in once, after signing.
  IF OLD.tx_hash IS NOT NULL AND NEW.tx_hash IS DISTINCT FROM OLD.tx_hash THEN
    RAISE EXCEPTION 'the transaction hash cannot be changed once recorded' USING ERRCODE = 'OFB04';
  END IF;
  -- Transmitted and unhosted are final.
  IF OLD.transmission_status IN ('transmitted', 'not_applicable_unhosted')
     AND NEW.transmission_status IS DISTINCT FROM OLD.transmission_status THEN
    RAISE EXCEPTION 'transmission status % is final', OLD.transmission_status USING ERRCODE = 'OFB03';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS travel_rule_guard ON travel_rule_records;
CREATE TRIGGER travel_rule_guard BEFORE UPDATE OR DELETE ON travel_rule_records
  FOR EACH ROW EXECUTE FUNCTION travel_rule_guard();

ALTER TABLE travel_rule_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE travel_rule_records FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON travel_rule_records;
CREATE POLICY tenant_isolation ON travel_rule_records
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

GRANT SELECT, INSERT, UPDATE ON travel_rule_records TO app_admin;
