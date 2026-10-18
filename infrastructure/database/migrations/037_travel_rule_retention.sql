-- Retention for Travel Rule information.
--
-- Travel Rule records are kept as evidence and the guards refuse any edit or
-- delete. They must not be kept forever either: POPIA s14 and GDPR art. 5(1)(e)
-- limit retention to what the purpose and the law need, and the FIC Act (s22,
-- s23) sets that at five years. So a delete is allowed in exactly one case:
--
--   * a retention purge is running in this transaction
--     (set_config('ofb.retention_purge', 'on', true)), and
--   * the row is older than five years. The floor is in the trigger, so no
--     purge, however it is called, can remove younger records.
--
-- purge_travel_rule() is the only intended caller. It runs as app_admin
-- (BYPASSRLS), deletes what is past the organisation's retention period, and
-- writes one row per table to retention_purges, which is itself never edited
-- or deleted. Records the platform still needs (an outbound transfer whose
-- transmission is pending or failed) are kept regardless of age.

CREATE TABLE IF NOT EXISTS retention_purges (
  purge_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  table_name    TEXT NOT NULL,
  customer_id   UUID,
  cutoff        TIMESTAMPTZ NOT NULL,
  rows_deleted  INTEGER NOT NULL CHECK (rows_deleted >= 0),
  dry_run       BOOLEAN NOT NULL,
  run_by        TEXT NOT NULL DEFAULT current_user,
  reason        TEXT NOT NULL CHECK (length(reason) BETWEEN 3 AND 500),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_retention_purges_created ON retention_purges (created_at DESC);

CREATE OR REPLACE FUNCTION retention_purges_guard() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'the retention log is never edited or deleted' USING ERRCODE = 'OFB08';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS retention_purges_guard ON retention_purges;
CREATE TRIGGER retention_purges_guard BEFORE UPDATE OR DELETE ON retention_purges
  FOR EACH ROW EXECUTE FUNCTION retention_purges_guard();
GRANT SELECT, INSERT ON retention_purges TO app_admin;

-- The legal floor, in one place.
CREATE OR REPLACE FUNCTION travel_rule_retention_floor() RETURNS interval AS $$
  SELECT interval '5 years';
$$ LANGUAGE sql IMMUTABLE;

-- travel_rule_records: deletes only inside a purge, only past the floor.
CREATE OR REPLACE FUNCTION travel_rule_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF current_setting('ofb.retention_purge', true) = 'on'
       AND OLD.created_at < now() - travel_rule_retention_floor() THEN
      RETURN OLD;
    END IF;
    RAISE EXCEPTION 'travel rule records are retained, not deleted (only a retention purge may remove records older than %)',
      travel_rule_retention_floor() USING ERRCODE = 'OFB04';
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
  IF OLD.tx_hash IS NOT NULL AND NEW.tx_hash IS DISTINCT FROM OLD.tx_hash THEN
    RAISE EXCEPTION 'the transaction hash cannot be changed once recorded' USING ERRCODE = 'OFB04';
  END IF;
  IF OLD.transmission_status IN ('transmitted', 'not_applicable_unhosted')
     AND NEW.transmission_status IS DISTINCT FROM OLD.transmission_status THEN
    RAISE EXCEPTION 'transmission status % is final', OLD.transmission_status USING ERRCODE = 'OFB03';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- trisa_inbound: same rule; edits are still never allowed.
CREATE OR REPLACE FUNCTION trisa_inbound_guard() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE'
     AND current_setting('ofb.retention_purge', true) = 'on'
     AND OLD.received_at < now() - travel_rule_retention_floor() THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'received Travel Rule information is never edited or deleted (only a retention purge may remove records older than %)',
    travel_rule_retention_floor() USING ERRCODE = 'OFB07';
END;
$$ LANGUAGE plpgsql;

GRANT DELETE ON travel_rule_records TO app_admin;
GRANT SELECT, DELETE ON trisa_inbound TO app_admin;

-- The purge. p_retain must be at least the floor; a longer period (a customer's
-- own policy, a legal hold) is allowed. p_customer limits it to one organisation.
-- p_dry_run counts without deleting, and is logged too.
CREATE OR REPLACE FUNCTION purge_travel_rule(p_retain interval, p_reason text,
                                             p_customer uuid DEFAULT NULL, p_dry_run boolean DEFAULT true)
RETURNS TABLE (table_name text, rows_deleted integer, cutoff timestamptz) AS $$
DECLARE
  v_cutoff timestamptz := now() - p_retain;
  v_n integer;
BEGIN
  IF p_retain < travel_rule_retention_floor() THEN
    RAISE EXCEPTION 'retention of % is shorter than the legal minimum of %', p_retain, travel_rule_retention_floor()
      USING ERRCODE = 'OFB08';
  END IF;
  PERFORM set_config('ofb.retention_purge', 'on', true);

  IF p_dry_run THEN
    SELECT count(*) INTO v_n FROM travel_rule_records r
     WHERE r.created_at < v_cutoff
       AND r.transmission_status IN ('transmitted', 'not_applicable_unhosted')
       AND (p_customer IS NULL OR r.customer_id = p_customer);
  ELSE
    DELETE FROM travel_rule_records r
     WHERE r.created_at < v_cutoff
       AND r.transmission_status IN ('transmitted', 'not_applicable_unhosted')
       AND (p_customer IS NULL OR r.customer_id = p_customer);
    GET DIAGNOSTICS v_n = ROW_COUNT;
  END IF;
  INSERT INTO retention_purges (table_name, customer_id, cutoff, rows_deleted, dry_run, reason)
    VALUES ('travel_rule_records', p_customer, v_cutoff, v_n, p_dry_run, p_reason);
  table_name := 'travel_rule_records'; rows_deleted := v_n; cutoff := v_cutoff; RETURN NEXT;

  IF p_dry_run THEN
    SELECT count(*) INTO v_n FROM trisa_inbound i
     WHERE i.received_at < v_cutoff AND (p_customer IS NULL OR i.customer_id = p_customer);
  ELSE
    DELETE FROM trisa_inbound i
     WHERE i.received_at < v_cutoff AND (p_customer IS NULL OR i.customer_id = p_customer);
    GET DIAGNOSTICS v_n = ROW_COUNT;
  END IF;
  INSERT INTO retention_purges (table_name, customer_id, cutoff, rows_deleted, dry_run, reason)
    VALUES ('trisa_inbound', p_customer, v_cutoff, v_n, p_dry_run, p_reason);
  table_name := 'trisa_inbound'; rows_deleted := v_n; cutoff := v_cutoff; RETURN NEXT;

  PERFORM set_config('ofb.retention_purge', 'off', true);
END;
$$ LANGUAGE plpgsql;

REVOKE ALL ON FUNCTION purge_travel_rule(interval, text, uuid, boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION purge_travel_rule(interval, text, uuid, boolean) TO app_admin;
