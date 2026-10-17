-- OpenFireblocks Initial Schema
-- Supports multi-tenant threshold cryptography and transaction signing

-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Customers table (multi-tenant support)
CREATE TABLE customers (
  customer_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name VARCHAR(255) NOT NULL,
  api_key_hash BYTEA NOT NULL UNIQUE,
  status VARCHAR(50) DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'suspended')),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_customers_status ON customers(status);

-- Key pairs table (threshold keys)
CREATE TABLE key_pairs (
  key_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id),
  name VARCHAR(255) NOT NULL,
  blockchain VARCHAR(50) NOT NULL,
  threshold INTEGER NOT NULL,
  total_parties INTEGER NOT NULL,
  address VARCHAR(255),
  public_key TEXT,
  status VARCHAR(50) DEFAULT 'pending_dkg' CHECK (status IN ('pending_dkg', 'active', 'inactive', 'compromised')),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
  UNIQUE (customer_id, name)
);

CREATE INDEX idx_key_pairs_customer ON key_pairs(customer_id);
CREATE INDEX idx_key_pairs_status ON key_pairs(status);
CREATE INDEX idx_key_pairs_blockchain ON key_pairs(blockchain);

-- DKG ceremonies table (distributed key generation)
CREATE TABLE dkg_ceremonies (
  ceremony_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  key_id UUID NOT NULL REFERENCES key_pairs(key_id),
  customer_id UUID NOT NULL REFERENCES customers(customer_id),
  threshold INTEGER NOT NULL,
  total_parties INTEGER NOT NULL,
  status VARCHAR(50) DEFAULT 'initiated' CHECK (
    status IN ('initiated', 'in_progress', 'completed', 'failed')
  ),
  workflow_id VARCHAR(255),
  current_round INTEGER DEFAULT 0,
  error_message TEXT,
  started_at TIMESTAMP NOT NULL DEFAULT NOW(),
  completed_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ceremonies_key ON dkg_ceremonies(key_id);
CREATE INDEX idx_ceremonies_customer ON dkg_ceremonies(customer_id);
CREATE INDEX idx_ceremonies_status ON dkg_ceremonies(status);

-- DKG round data table
CREATE TABLE dkg_rounds (
  round_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  ceremony_id UUID NOT NULL REFERENCES dkg_ceremonies(ceremony_id),
  round_number INTEGER NOT NULL,
  party_id INTEGER NOT NULL,
  commitments BYTEA,
  proof BYTEA,
  round_hash VARCHAR(128),
  status VARCHAR(50) DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'completed', 'failed')),
  started_at TIMESTAMP NOT NULL DEFAULT NOW(),
  completed_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  UNIQUE (ceremony_id, round_number, party_id)
);

CREATE INDEX idx_rounds_ceremony ON dkg_rounds(ceremony_id);

-- Key shares table (encrypted shares in Vault)
CREATE TABLE key_shares (
  share_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  key_id UUID NOT NULL REFERENCES key_pairs(key_id),
  party_id INTEGER NOT NULL,
  vault_path VARCHAR(255) NOT NULL,
  status VARCHAR(50) DEFAULT 'pending' CHECK (status IN ('pending', 'sealed', 'unsealed', 'compromised')),
  backed_up_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
  UNIQUE (key_id, party_id)
);

CREATE INDEX idx_shares_key ON key_shares(key_id);

-- Signing requests table
CREATE TABLE signing_requests (
  request_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id),
  key_id UUID NOT NULL REFERENCES key_pairs(key_id),
  transaction_hash VARCHAR(128) NOT NULL,
  transaction_data BYTEA NOT NULL,
  blockchain VARCHAR(50) NOT NULL,
  idempotency_key VARCHAR(255),
  status VARCHAR(50) DEFAULT 'pending' CHECK (
    status IN ('pending', 'in_progress', 'completed', 'failed')
  ),
  signature BYTEA,
  signed_transaction BYTEA,
  workflow_id VARCHAR(255),
  error_message TEXT,
  latency_ms INTEGER,
  started_at TIMESTAMP NOT NULL DEFAULT NOW(),
  completed_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
  UNIQUE (customer_id, idempotency_key)
);

CREATE INDEX idx_signing_requests_customer ON signing_requests(customer_id);
CREATE INDEX idx_signing_requests_key ON signing_requests(key_id);
CREATE INDEX idx_signing_requests_status ON signing_requests(status);
CREATE INDEX idx_signing_requests_idempotency ON signing_requests(idempotency_key);

-- Audit log table
CREATE TABLE audit_logs (
  log_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id),
  entity_type VARCHAR(50) NOT NULL,
  entity_id VARCHAR(255) NOT NULL,
  action VARCHAR(50) NOT NULL,
  details JSONB,
  ip_address VARCHAR(45),
  user_agent TEXT,
  status VARCHAR(50),
  created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_logs_customer ON audit_logs(customer_id);
CREATE INDEX idx_audit_logs_entity ON audit_logs(entity_type, entity_id);
CREATE INDEX idx_audit_logs_action ON audit_logs(action);
CREATE INDEX idx_audit_logs_timestamp ON audit_logs(created_at);

-- Policy table (signing policies)
CREATE TABLE policies (
  policy_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id),
  name VARCHAR(255) NOT NULL,
  description TEXT,
  rules JSONB NOT NULL,
  status VARCHAR(50) DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
  UNIQUE (customer_id, name)
);

CREATE INDEX idx_policies_customer ON policies(customer_id);

-- Compliance check results table
CREATE TABLE compliance_checks (
  check_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id),
  request_id UUID REFERENCES signing_requests(request_id),
  check_type VARCHAR(50) NOT NULL,
  status VARCHAR(50) NOT NULL,
  details JSONB,
  created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_compliance_checks_customer ON compliance_checks(customer_id);
CREATE INDEX idx_compliance_checks_request ON compliance_checks(request_id);

-- Health check data
CREATE TABLE health_checks (
  check_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  service_name VARCHAR(255) NOT NULL,
  status VARCHAR(50) NOT NULL,
  latency_ms INTEGER,
  details JSONB,
  created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_health_checks_service ON health_checks(service_name);
CREATE INDEX idx_health_checks_timestamp ON health_checks(created_at);

-- Session management
CREATE TABLE sessions (
  session_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id UUID NOT NULL REFERENCES customers(customer_id),
  token_hash BYTEA NOT NULL UNIQUE,
  expires_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sessions_customer ON sessions(customer_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- Liquidity and balance tracking
CREATE TABLE balances (
  balance_id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  key_id UUID NOT NULL REFERENCES key_pairs(key_id),
  blockchain VARCHAR(50) NOT NULL,
  balance VARCHAR(255),
  last_updated TIMESTAMP NOT NULL DEFAULT NOW(),
  created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_balances_key ON balances(key_id);
CREATE INDEX idx_balances_blockchain ON balances(blockchain);

-- Views for common queries
CREATE VIEW active_keys AS
SELECT * FROM key_pairs WHERE status = 'active';

CREATE VIEW pending_ceremonies AS
SELECT * FROM dkg_ceremonies WHERE status IN ('initiated', 'in_progress');

CREATE VIEW completed_signings AS
SELECT * FROM signing_requests WHERE status = 'completed';
