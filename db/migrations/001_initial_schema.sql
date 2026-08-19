-- Create extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Create roles table
CREATE TABLE roles (
  role_id VARCHAR(50) PRIMARY KEY,
  name VARCHAR(100) NOT NULL UNIQUE,
  description TEXT,
  permissions JSONB NOT NULL DEFAULT '[]'::JSONB,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO roles (role_id, name, permissions) VALUES
('admin', 'Administrator', '["users:*", "keys:*", "signings:*", "policies:*", "compliance:*", "audit:*", "webhooks:*", "billing:*"]'),
('billing_admin', 'Billing Admin', '["users:read", "billing:*", "reports:read", "audit:read"]'),
('user', 'User', '["keys:*", "signings:*", "policies:read", "webhooks:*"]'),
('viewer', 'Viewer', '["keys:read", "signings:read", "reports:read"]');

-- Create users table
CREATE TABLE users (
  user_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  email VARCHAR(255) NOT NULL UNIQUE,
  password_hash VARCHAR(255),
  full_name VARCHAR(255) NOT NULL,
  organization VARCHAR(255) NOT NULL,
  role VARCHAR(50) NOT NULL REFERENCES roles(role_id),
  status VARCHAR(50) NOT NULL DEFAULT 'active', -- active, inactive, suspended
  two_fa_enabled BOOLEAN DEFAULT FALSE,
  two_fa_secret VARCHAR(255),
  last_login_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (status IN ('active', 'inactive', 'suspended'))
);

CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_organization ON users(organization);
CREATE INDEX idx_users_status ON users(status);

-- Create sessions table
CREATE TABLE sessions (
  session_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id UUID NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
  ip_address INET,
  user_agent TEXT,
  device_info TEXT,
  is_active BOOLEAN DEFAULT TRUE,
  last_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP + INTERVAL '24 hours'
);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX idx_sessions_is_active ON sessions(is_active);

-- Create API keys table
CREATE TABLE api_keys (
  key_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id UUID NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
  name VARCHAR(255) NOT NULL,
  key_hash VARCHAR(255) NOT NULL,
  key_prefix VARCHAR(16) NOT NULL UNIQUE, -- First 16 chars for lookup
  scope VARCHAR(50) NOT NULL DEFAULT 'read', -- read, write, admin
  last_chars VARCHAR(8), -- Last 8 chars for display
  last_used_at TIMESTAMP,
  expires_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX idx_api_keys_key_prefix ON api_keys(key_prefix);
CREATE INDEX idx_api_keys_expires_at ON api_keys(expires_at);

-- Create MFA sessions table
CREATE TABLE mfa_sessions (
  session_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id UUID NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
  token VARCHAR(255) NOT NULL UNIQUE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP + INTERVAL '10 minutes'
);

CREATE INDEX idx_mfa_sessions_user_id ON mfa_sessions(user_id);
CREATE INDEX idx_mfa_sessions_token ON mfa_sessions(token);

-- Create password resets table
CREATE TABLE password_resets (
  reset_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id UUID NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
  email VARCHAR(255) NOT NULL,
  token VARCHAR(255) NOT NULL UNIQUE,
  is_used BOOLEAN DEFAULT FALSE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP + INTERVAL '1 hour'
);

CREATE INDEX idx_password_resets_user_id ON password_resets(user_id);
CREATE INDEX idx_password_resets_email ON password_resets(email);

-- Create customers table (multi-tenant)
CREATE TABLE customers (
  customer_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  owner_user_id UUID NOT NULL REFERENCES users(user_id),
  name VARCHAR(255) NOT NULL,
  kyc_level VARCHAR(50) NOT NULL DEFAULT 'individual', -- individual, business, institutional
  kyc_status VARCHAR(50) NOT NULL DEFAULT 'pending', -- pending, approved, rejected
  kyc_verified_at TIMESTAMP,
  compliance_score INT DEFAULT 0, -- 0-100
  risk_level VARCHAR(50) DEFAULT 'medium', -- low, medium, high
  daily_spending_limit BIGINT,
  two_fa_required BOOLEAN DEFAULT TRUE,
  webhook_signing_enabled BOOLEAN DEFAULT TRUE,
  webhook_secret VARCHAR(255),
  metadata JSONB,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_customers_owner_user_id ON customers(owner_user_id);
CREATE INDEX idx_customers_kyc_status ON customers(kyc_status);
CREATE INDEX idx_customers_compliance_score ON customers(compliance_score);
CREATE INDEX idx_customers_risk_level ON customers(risk_level);

-- Create customer users mapping (multi-tenant access)
CREATE TABLE customer_users (
  customer_user_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
  role VARCHAR(50) NOT NULL REFERENCES roles(role_id),
  permissions JSONB NOT NULL DEFAULT '[]'::JSONB,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(customer_id, user_id)
);

CREATE INDEX idx_customer_users_customer_id ON customer_users(customer_id);
CREATE INDEX idx_customer_users_user_id ON customer_users(user_id);

-- Create keys table
CREATE TABLE keys (
  key_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  name VARCHAR(255) NOT NULL,
  blockchain VARCHAR(50) NOT NULL, -- bitcoin, ethereum, solana, polygon
  threshold INT NOT NULL DEFAULT 4,
  total_parties INT NOT NULL DEFAULT 7,
  status VARCHAR(50) NOT NULL DEFAULT 'pending', -- pending, active, rotating, compromised, retired
  public_key VARCHAR(255),
  key_hash VARCHAR(255),
  dkg_ceremony_id UUID,
  created_by_user_id UUID REFERENCES users(user_id),
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  rotated_at TIMESTAMP,
  retired_at TIMESTAMP
);

CREATE INDEX idx_keys_customer_id ON keys(customer_id);
CREATE INDEX idx_keys_blockchain ON keys(blockchain);
CREATE INDEX idx_keys_status ON keys(status);
CREATE INDEX idx_keys_dkg_ceremony_id ON keys(dkg_ceremony_id);

-- Create signings table
CREATE TABLE signings (
  signing_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  key_id UUID NOT NULL REFERENCES keys(key_id),
  blockchain VARCHAR(50) NOT NULL,
  transaction_hash VARCHAR(255),
  status VARCHAR(50) NOT NULL DEFAULT 'pending', -- pending, signing, signed, settled, confirmed, failed
  signing_threshold INT,
  signatures_collected INT DEFAULT 0,
  amount VARCHAR(255),
  destination_address VARCHAR(255),
  source_address VARCHAR(255),
  gas_price VARCHAR(255),
  gas_used VARCHAR(255),
  nonce INT,
  confirmation_count INT,
  error_message TEXT,
  metadata JSONB,
  initiated_by_user_id UUID REFERENCES users(user_id),
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  signed_at TIMESTAMP,
  settled_at TIMESTAMP,
  confirmed_at TIMESTAMP
);

CREATE INDEX idx_signings_customer_id ON signings(customer_id);
CREATE INDEX idx_signings_key_id ON signings(key_id);
CREATE INDEX idx_signings_status ON signings(status);
CREATE INDEX idx_signings_created_at ON signings(created_at);
CREATE INDEX idx_signings_transaction_hash ON signings(transaction_hash);

-- Create signing approvals table (for policy-based approval)
CREATE TABLE signing_approvals (
  approval_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  signing_id UUID NOT NULL REFERENCES signings(signing_id) ON DELETE CASCADE,
  approver_user_id UUID NOT NULL REFERENCES users(user_id),
  status VARCHAR(50) NOT NULL DEFAULT 'pending', -- pending, approved, rejected
  reason TEXT,
  approved_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_signing_approvals_signing_id ON signing_approvals(signing_id);
CREATE INDEX idx_signing_approvals_approver_user_id ON signing_approvals(approver_user_id);
CREATE INDEX idx_signing_approvals_status ON signing_approvals(status);

-- Create policies table
CREATE TABLE policies (
  policy_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  name VARCHAR(255) NOT NULL,
  description TEXT,
  rule_type VARCHAR(50) NOT NULL, -- amount_limit, whitelist, time_based, blockchain, frequency
  rule_config JSONB NOT NULL,
  approval_config JSONB, -- {"required": true, "approver_count": 2, "timeout_hours": 24}
  enabled BOOLEAN DEFAULT TRUE,
  priority INT DEFAULT 0,
  created_by_user_id UUID REFERENCES users(user_id),
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_policies_customer_id ON policies(customer_id);
CREATE INDEX idx_policies_enabled ON policies(enabled);
CREATE INDEX idx_policies_rule_type ON policies(rule_type);

-- Create webhooks table
CREATE TABLE webhooks (
  webhook_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  url VARCHAR(2048) NOT NULL,
  secret VARCHAR(255) NOT NULL,
  events TEXT[] NOT NULL, -- Array of event types
  is_active BOOLEAN DEFAULT TRUE,
  max_retries INT DEFAULT 3,
  backoff_seconds INT DEFAULT 60,
  exponential_backoff BOOLEAN DEFAULT TRUE,
  custom_headers JSONB,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_webhooks_customer_id ON webhooks(customer_id);
CREATE INDEX idx_webhooks_is_active ON webhooks(is_active);

-- Create webhook deliveries table
CREATE TABLE webhook_deliveries (
  delivery_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  webhook_id UUID NOT NULL REFERENCES webhooks(webhook_id) ON DELETE CASCADE,
  event_id UUID NOT NULL,
  event_type VARCHAR(100) NOT NULL,
  attempt INT NOT NULL DEFAULT 1,
  status_code INT,
  response_time_ms INT,
  success BOOLEAN DEFAULT FALSE,
  error_message TEXT,
  next_retry_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_webhook_deliveries_webhook_id ON webhook_deliveries(webhook_id);
CREATE INDEX idx_webhook_deliveries_event_id ON webhook_deliveries(event_id);
CREATE INDEX idx_webhook_deliveries_success ON webhook_deliveries(success);

-- Create audit logs table (immutable, replicated)
CREATE TABLE audit_logs (
  log_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID REFERENCES customers(customer_id) ON DELETE SET NULL,
  user_id UUID REFERENCES users(user_id) ON DELETE SET NULL,
  action VARCHAR(100) NOT NULL,
  actor VARCHAR(255) NOT NULL,
  resource_id VARCHAR(255),
  resource_type VARCHAR(100),
  changes JSONB,
  ip_address INET,
  user_agent TEXT,
  status VARCHAR(50) NOT NULL DEFAULT 'success', -- success, failure
  error_details TEXT,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_audit_logs_customer_id ON audit_logs(customer_id);
CREATE INDEX idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_action ON audit_logs(action);
CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at);
CREATE INDEX idx_audit_logs_resource_type ON audit_logs(resource_type);

-- Create compliance checks table
CREATE TABLE compliance_checks (
  check_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  check_type VARCHAR(50) NOT NULL, -- kyc, aml, sanctions, pep, ongoing
  status VARCHAR(50) NOT NULL DEFAULT 'pending', -- pending, passed, failed, review
  result_data JSONB,
  risk_level VARCHAR(50), -- low, medium, high
  checked_at TIMESTAMP,
  next_check_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_compliance_checks_customer_id ON compliance_checks(customer_id);
CREATE INDEX idx_compliance_checks_check_type ON compliance_checks(check_type);
CREATE INDEX idx_compliance_checks_status ON compliance_checks(status);

-- Create compliance reports table
CREATE TABLE compliance_reports (
  report_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID REFERENCES customers(customer_id) ON DELETE SET NULL,
  report_type VARCHAR(50) NOT NULL, -- audit, compliance, risk, kyc
  period VARCHAR(50) NOT NULL, -- monthly, quarterly, yearly
  start_date DATE NOT NULL,
  end_date DATE NOT NULL,
  status VARCHAR(50) NOT NULL DEFAULT 'generating', -- generating, completed, failed
  data JSONB,
  file_url VARCHAR(2048),
  generated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMP
);

CREATE INDEX idx_compliance_reports_customer_id ON compliance_reports(customer_id);
CREATE INDEX idx_compliance_reports_report_type ON compliance_reports(report_type);
CREATE INDEX idx_compliance_reports_status ON compliance_reports(status);

-- Create incidents table
CREATE TABLE incidents (
  incident_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID REFERENCES customers(customer_id) ON DELETE SET NULL,
  type VARCHAR(100) NOT NULL,
  severity VARCHAR(50) NOT NULL, -- low, medium, high, critical
  description TEXT,
  status VARCHAR(50) NOT NULL DEFAULT 'open', -- open, investigating, resolved
  root_cause TEXT,
  resolution TEXT,
  detected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  resolved_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_incidents_customer_id ON incidents(customer_id);
CREATE INDEX idx_incidents_severity ON incidents(severity);
CREATE INDEX idx_incidents_status ON incidents(status);

-- Create API usage stats table
CREATE TABLE api_usage_stats (
  stat_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  api_key_id UUID REFERENCES api_keys(key_id) ON DELETE SET NULL,
  endpoint VARCHAR(255),
  method VARCHAR(10),
  status_code INT,
  response_time_ms INT,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_api_usage_stats_customer_id ON api_usage_stats(customer_id);
CREATE INDEX idx_api_usage_stats_api_key_id ON api_usage_stats(api_key_id);
CREATE INDEX idx_api_usage_stats_created_at ON api_usage_stats(created_at);

-- Create rate limit tracking table
CREATE TABLE rate_limits (
  limit_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  limit_type VARCHAR(50) NOT NULL, -- api, signing, daily_spend
  window_start TIMESTAMP NOT NULL,
  window_end TIMESTAMP NOT NULL,
  count INT NOT NULL DEFAULT 0,
  limit_value INT NOT NULL,
  status VARCHAR(50) NOT NULL DEFAULT 'active', -- active, exceeded
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_rate_limits_customer_id ON rate_limits(customer_id);
CREATE INDEX idx_rate_limits_limit_type ON rate_limits(limit_type);
CREATE INDEX idx_rate_limits_window_start ON rate_limits(window_start);

-- Create DKG ceremonies table
CREATE TABLE dkg_ceremonies (
  ceremony_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  key_id UUID REFERENCES keys(key_id),
  threshold INT NOT NULL,
  total_parties INT NOT NULL,
  status VARCHAR(50) NOT NULL DEFAULT 'pending', -- pending, preparing, executing, confirming, completed, failed
  result JSONB,
  error_message TEXT,
  scheduled_for TIMESTAMP,
  started_at TIMESTAMP,
  completed_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_dkg_ceremonies_customer_id ON dkg_ceremonies(customer_id);
CREATE INDEX idx_dkg_ceremonies_key_id ON dkg_ceremonies(key_id);
CREATE INDEX idx_dkg_ceremonies_status ON dkg_ceremonies(status);

-- Row-level security policies
ALTER TABLE customers ENABLE ROW LEVEL SECURITY;
ALTER TABLE signings ENABLE ROW LEVEL SECURITY;
ALTER TABLE keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhooks ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE compliance_checks ENABLE ROW LEVEL SECURITY;

-- RLS policies (example for customers - admin bypass via service role)
CREATE POLICY customers_isolation ON customers
  FOR ALL
  USING (customer_id IN (
    SELECT customer_id FROM customer_users WHERE user_id = current_user_id
  ));

-- Create function for audit trigger
CREATE OR REPLACE FUNCTION audit_log_trigger()
RETURNS TRIGGER AS $$
BEGIN
  INSERT INTO audit_logs (action, actor, resource_id, resource_type, changes, status, created_at)
  VALUES (TG_ARGV[0], current_user, NEW.id::text, TG_TABLE_NAME, row_to_json(NEW), 'success', CURRENT_TIMESTAMP);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create updated_at trigger
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = CURRENT_TIMESTAMP;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply triggers
CREATE TRIGGER users_updated_at_trigger
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER customers_updated_at_trigger
BEFORE UPDATE ON customers
FOR EACH ROW
EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER policies_updated_at_trigger
BEFORE UPDATE ON policies
FOR EACH ROW
EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER webhooks_updated_at_trigger
BEFORE UPDATE ON webhooks
FOR EACH ROW
EXECUTE FUNCTION update_updated_at();

-- Grants for public role (remove after setup)
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO public;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO public;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO public;
