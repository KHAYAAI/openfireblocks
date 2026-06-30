-- OpenFireblocks Phase 0 database schema.
-- Loaded automatically by the postgres container on first start
-- (mounted into /docker-entrypoint-initdb.d). Idempotent so re-runs are safe.

CREATE SCHEMA IF NOT EXISTS audit;
CREATE SCHEMA IF NOT EXISTS signing;

-- ---------------------------------------------------------------------------
-- Audit trail: one row per lifecycle event of a request.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit.events (
  id            BIGSERIAL PRIMARY KEY,
  event_type    VARCHAR(50)  NOT NULL,        -- KEY_GENERATED, SIGN_REQUEST_RECEIVED, SIGN_SUCCESS, BROADCAST_SUCCESS, ...
  timestamp     TIMESTAMPTZ  DEFAULT NOW(),
  actor         VARCHAR(255),                 -- 'system' in Phase 0; customer id in Phase 1
  request_id    UUID,
  message       TEXT,                         -- free-form / JSON metadata
  signature     VARCHAR(1024),                -- hex-encoded signature
  hash          VARCHAR(255),                 -- transaction hash
  chain         VARCHAR(50),                  -- 'ethereum'
  status        VARCHAR(50),                  -- 'pending', 'signed', 'broadcasted', 'failed'
  error_message TEXT,
  created_at    TIMESTAMPTZ  DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_timestamp  ON audit.events(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_request_id ON audit.events(request_id);
CREATE INDEX IF NOT EXISTS idx_audit_status     ON audit.events(status);

-- ---------------------------------------------------------------------------
-- Signing key metadata (Phase 0: a single shared key).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS signing.keys (
  id               SERIAL PRIMARY KEY,
  key_id           VARCHAR(255) UNIQUE NOT NULL, -- UUID
  key_type         VARCHAR(50),                  -- 'ECDSA', 'EdDSA'
  curve            VARCHAR(50),                  -- 'secp256k1', 'ed25519'
  public_key       VARCHAR(1024),                -- hex-encoded public key
  vault_reference  VARCHAR(255),                 -- HashiCorp Vault secret id (Phase 1+)
  created_at       TIMESTAMPTZ DEFAULT NOW(),
  updated_at       TIMESTAMPTZ DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- Transaction metadata mirrored from the signing pipeline.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS signing.transactions (
  id                 BIGSERIAL PRIMARY KEY,
  request_id         UUID UNIQUE NOT NULL,
  chain              VARCHAR(50),                 -- 'ethereum'
  to_address         VARCHAR(255) NOT NULL,
  amount             VARCHAR(100),                -- wei as a base-10 string
  data               VARCHAR(10000),              -- ABI-encoded call data
  gas_limit          VARCHAR(50),
  gas_price          VARCHAR(100),
  nonce              INTEGER,
  status             VARCHAR(50),                 -- 'pending','signed','broadcasted','confirmed','failed'
  signed_tx          VARCHAR(10000),              -- raw signed transaction
  tx_hash            VARCHAR(255),
  receipt            JSON,                        -- full receipt once confirmed
  confirmation_count INTEGER DEFAULT 0,
  created_at         TIMESTAMPTZ DEFAULT NOW(),
  updated_at         TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tx_request_id ON signing.transactions(request_id);
CREATE INDEX IF NOT EXISTS idx_tx_status     ON signing.transactions(status);
CREATE INDEX IF NOT EXISTS idx_tx_hash       ON signing.transactions(tx_hash);
