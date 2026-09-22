-- Creates two tables that services/api-gateway's PostgresService and
-- AuditService have been writing to (signing.transactions, audit.events)
-- since whichever earlier session wrote those files, but that no migration
-- ever created. AuditService.logEvent() catches its own query error and
-- returns null "so auditing never blocks the signing path" -- which meant
-- every single call (SIGN_REQUEST_RECEIVED, POLICY_DENIED, RISK_DENIED,
-- SIGN_SUCCESS, BROADCAST_SUCCESS/FAILED, SIGN_FAILED) has been silently
-- failing against a table that doesn't exist. The audit checklist's
-- "Dual audit trail (PostgreSQL + immudb)" line was marked done on the
-- strength of code that looked right, not on a query that was ever run
-- against a real schema -- exactly the gap this migration closes.

CREATE SCHEMA IF NOT EXISTS signing;

CREATE TABLE signing.transactions (
  id           BIGSERIAL PRIMARY KEY,
  request_id   UUID NOT NULL UNIQUE,
  customer_id  UUID NOT NULL REFERENCES customers(customer_id),
  chain        VARCHAR(50) NOT NULL,
  to_address   VARCHAR(255) NOT NULL,
  amount       VARCHAR(255) NOT NULL,
  data         TEXT,
  gas_limit    BIGINT,
  gas_price    VARCHAR(255),
  nonce        BIGINT,
  signed_tx    TEXT,
  tx_hash      VARCHAR(255),
  status       VARCHAR(50) NOT NULL,
  created_at   TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_signing_transactions_customer ON signing.transactions(customer_id);
CREATE INDEX idx_signing_transactions_status ON signing.transactions(status);

CREATE SCHEMA IF NOT EXISTS audit;

CREATE TABLE audit.events (
  id            BIGSERIAL PRIMARY KEY,
  event_type    VARCHAR(100) NOT NULL,
  actor         VARCHAR(255) NOT NULL DEFAULT 'system',
  customer_id   UUID REFERENCES customers(customer_id), -- NULL for system-actor events
  request_id    UUID NOT NULL,
  message       TEXT,
  signature     VARCHAR(500),
  hash          VARCHAR(255),
  chain         VARCHAR(50),
  status        VARCHAR(50) NOT NULL,
  error_message TEXT,
  timestamp     TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_events_customer ON audit.events(customer_id);
CREATE INDEX idx_audit_events_request ON audit.events(request_id);
CREATE INDEX idx_audit_events_type ON audit.events(event_type);
CREATE INDEX idx_audit_events_timestamp ON audit.events(timestamp);

-- Same row-level security posture as migration 011: enforced (not just
-- enabled -- app no longer bypasses it, see 011's header comment), fail
-- closed with no session variable set, tenant-scoped when
-- app.current_customer_id is set for the transaction.
ALTER TABLE signing.transactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE signing.transactions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON signing.transactions
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

-- audit.events.customer_id is nullable (system-actor events have none);
-- same treatment as compliance_reports/settlements in migration 011 -- a
-- NULL-customer_id row is visible only to app_admin, not any one tenant.
ALTER TABLE audit.events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit.events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit.events
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

GRANT USAGE ON SCHEMA signing TO app, app_admin;
GRANT USAGE ON SCHEMA audit TO app, app_admin;
GRANT ALL PRIVILEGES ON signing.transactions TO app, app_admin;
GRANT ALL PRIVILEGES ON audit.events TO app, app_admin;
GRANT ALL PRIVILEGES ON signing.transactions_id_seq TO app, app_admin;
GRANT ALL PRIVILEGES ON audit.events_id_seq TO app, app_admin;
