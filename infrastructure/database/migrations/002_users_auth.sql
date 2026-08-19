-- Human dashboard users (distinct from `customers`, which are API-key tenants).
-- A user authenticates with email/password + optional TOTP MFA to get a JWT,
-- then acts within the scope of one or more customers via user_customer_roles.

CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  email VARCHAR(255) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,
  full_name VARCHAR(255) NOT NULL,
  role VARCHAR(50) NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'billing_admin', 'user', 'viewer')),
  status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'suspended')),
  mfa_secret VARCHAR(255),
  mfa_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  failed_login_count INTEGER NOT NULL DEFAULT 0,
  locked_until TIMESTAMP,
  last_login_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_status ON users(status);

-- Short-lived, single-use token issued after password verification when MFA
-- is enabled; the client exchanges it plus a valid TOTP code for a JWT.
-- Storing only a hash means a leaked table row cannot be replayed.
CREATE TABLE mfa_challenges (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  challenge_hash BYTEA NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  consumed_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_mfa_challenges_user ON mfa_challenges(user_id);
CREATE INDEX idx_mfa_challenges_expires ON mfa_challenges(expires_at);

-- Grants a user a role scoped to one tenant (a user may work across several
-- customers, e.g. an OpenFireblocks support engineer, or a customer may have
-- several human operators).
CREATE TABLE user_customer_roles (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  customer_id UUID NOT NULL REFERENCES customers(customer_id) ON DELETE CASCADE,
  role VARCHAR(50) NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'billing_admin', 'user', 'viewer')),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  PRIMARY KEY (user_id, customer_id)
);

CREATE INDEX idx_user_customer_roles_customer ON user_customer_roles(customer_id);

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_set_updated_at
BEFORE UPDATE ON users
FOR EACH ROW EXECUTE FUNCTION set_updated_at();
