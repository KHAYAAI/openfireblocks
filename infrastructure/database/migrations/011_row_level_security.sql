-- Enforces PostgreSQL row-level security on every customer-scoped table.
--
-- Why this was previously a no-op even though policies could have existed:
-- the `app` role that every service connects as was a full Postgres
-- SUPERUSER. Superusers always bypass row security, full stop -- it does
-- not matter whether ENABLE/FORCE ROW LEVEL SECURITY is set or how a
-- policy is written, a superuser session ignores it entirely (see
-- "Notes" in the CREATE POLICY docs). That's the actual reason the audit
-- checklist listed RLS as "scaffolded, not enforced": there was no
-- privilege boundary left for a policy to enforce. This migration fixes
-- that root cause, then adds the policies themselves.
--
-- Session-variable convention: application code sets
--   SET LOCAL app.current_customer_id = '<uuid>';
-- as the first statement of the transaction that serves one customer's
-- request. current_customer_id() below reads it back; if it was never
-- set, every policy comparison evaluates to NULL (falsy), so the default
-- posture is "see nothing" -- fail closed, not fail open.
--
-- Two roles, two different jobs:
--   app        -- customer-facing request path (api-gateway and friends).
--                 RLS-restricted to whatever customer_id the request set.
--   app_admin  -- back-office/cross-tenant jobs that have a legitimate
--                 reason to see every customer's rows in one query
--                 (services/compliance's platform-wide reports and
--                 audit/incident/metrics tooling; a future billing usage
--                 rollup job). Has BYPASSRLS. This is a deliberately
--                 narrow, named exception, not a way to route around RLS
--                 by habit -- anything customer-facing must go through
--                 `app` with the session variable set.

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app') THEN
    ALTER ROLE app NOSUPERUSER;
  END IF;
END
$$;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_admin') THEN
    CREATE ROLE app_admin LOGIN PASSWORD 'dev-only' BYPASSRLS;
  END IF;
  GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO app_admin;
  GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO app_admin;
  ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO app_admin;
END
$$;

CREATE OR REPLACE FUNCTION current_customer_id() RETURNS uuid
LANGUAGE sql STABLE AS $$
  SELECT NULLIF(current_setting('app.current_customer_id', true), '')::uuid
$$;

-- Directly customer_id-scoped tables: enable, force (so even the table
-- owner is bound by it -- `app` owns these tables since it ran the
-- migrations), and a single USING+WITH CHECK policy per table.
DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'customers', 'key_pairs', 'dkg_ceremonies', 'signing_requests',
    'audit_logs', 'policies', 'compliance_checks', 'sessions',
    'subscriptions', 'invoices', 'usage_metrics', 'webhooks',
    'integrations', 'user_customer_roles'
  ] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
    -- customers.customer_id is its own tenant key; every other table in
    -- this list has a customer_id column that references it.
    IF t = 'customers' THEN
      EXECUTE format(
        'CREATE POLICY tenant_isolation ON %I USING (customer_id = current_customer_id()) WITH CHECK (customer_id = current_customer_id())',
        t
      );
    ELSE
      EXECUTE format(
        'CREATE POLICY tenant_isolation ON %I USING (customer_id = current_customer_id()) WITH CHECK (customer_id = current_customer_id())',
        t
      );
    END IF;
  END LOOP;
END
$$;

-- compliance_reports.customer_id is nullable (NULL = platform-wide
-- report). A per-customer session may only see its own reports; a NULL
-- (platform-wide) report is not visible to any single customer's
-- session, only to app_admin.
ALTER TABLE compliance_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE compliance_reports FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON compliance_reports;
CREATE POLICY tenant_isolation ON compliance_reports
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

-- settlements.customer_id is nullable in the same sense; same treatment.
ALTER TABLE settlements ENABLE ROW LEVEL SECURITY;
ALTER TABLE settlements FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON settlements;
CREATE POLICY tenant_isolation ON settlements
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

-- Indirectly-scoped tables: no customer_id column of their own, scoped via
-- a join to a table that does. The subquery re-evaluates that parent
-- table's own RLS policy using the same session's current_customer_id(),
-- so this can't be used to see another customer's parent rows either.
ALTER TABLE dkg_rounds ENABLE ROW LEVEL SECURITY;
ALTER TABLE dkg_rounds FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON dkg_rounds;
CREATE POLICY tenant_isolation ON dkg_rounds
  USING (ceremony_id IN (SELECT ceremony_id FROM dkg_ceremonies));

ALTER TABLE key_shares ENABLE ROW LEVEL SECURITY;
ALTER TABLE key_shares FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON key_shares;
CREATE POLICY tenant_isolation ON key_shares
  USING (key_id IN (SELECT key_id FROM key_pairs));

ALTER TABLE balances ENABLE ROW LEVEL SECURITY;
ALTER TABLE balances FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON balances;
CREATE POLICY tenant_isolation ON balances
  USING (key_id IN (SELECT key_id FROM key_pairs));

ALTER TABLE webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_deliveries FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON webhook_deliveries;
CREATE POLICY tenant_isolation ON webhook_deliveries
  USING (webhook_id IN (SELECT webhook_id FROM webhooks));

ALTER TABLE integration_webhook_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE integration_webhook_logs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON integration_webhook_logs;
CREATE POLICY tenant_isolation ON integration_webhook_logs
  USING (integration_id IN (SELECT integration_id FROM integrations));

-- Not customer-scoped, intentionally no RLS:
--   users, mfa_challenges          -- keyed by user identity, not tenant;
--                                     a user can span multiple customers
--                                     via user_customer_roles. Restricting
--                                     a user to their own row is a
--                                     different, narrower concern than
--                                     multi-tenant isolation and is left
--                                     for a future pass.
--   plans                          -- public subscription catalog, every
--                                     customer is meant to see every plan.
--   health_checks                  -- internal ops data, not tenant data.
--   security_audits, audit_findings, audit_checklists, security_incidents,
--   incident_response_plans, compliance_metrics, compliance_alerts
--                                   -- platform-wide SOC2 evidence
--                                     (migration 010); no customer_id
--                                     column, inherently cross-tenant by
--                                     design, access controlled at the
--                                     application layer (compliance
--                                     service, staff-only).

-- Views over RLS-protected tables must be security_invoker so they check
-- RLS against the querying session, not the view owner (the default
-- behavior, which would silently bypass everything above).
ALTER VIEW active_keys SET (security_invoker = on);
ALTER VIEW pending_ceremonies SET (security_invoker = on);
ALTER VIEW completed_signings SET (security_invoker = on);
