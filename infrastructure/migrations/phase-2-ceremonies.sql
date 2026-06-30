-- Phase 2: MPC Threshold Signing & Multi-Chain Support
-- Database schema for ceremony management and multi-chain transactions

-- Ceremonies table: stores DKG ceremony metadata
CREATE TABLE IF NOT EXISTS ceremonies (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  chain_id VARCHAR NOT NULL,  -- "ethereum", "bitcoin", "solana", "cosmos-hub"
  n INTEGER NOT NULL,  -- total parties
  k INTEGER NOT NULL,  -- threshold (k+1 signatures needed)
  status VARCHAR NOT NULL DEFAULT 'pending',  -- pending, in_progress, round1-7, completed, failed
  threshold_address VARCHAR,  -- resulting shared public address
  threshold_public_key VARCHAR,  -- shared public key (hex)
  created_at TIMESTAMP DEFAULT NOW(),
  started_at TIMESTAMP,
  completed_at TIMESTAMP,
  failed_at TIMESTAMP,
  error_message TEXT,
  metadata JSONB,  -- DKG params, commitment info
  CONSTRAINT ceremony_status CHECK (status IN ('pending', 'in_progress', 'round1', 'round2', 'round3', 'round4', 'round5', 'round6', 'round7', 'completed', 'failed')),
  CONSTRAINT ceremony_threshold CHECK (k >= 1 AND n >= k + 1)
);

CREATE INDEX idx_ceremonies_customer_id ON ceremonies(customer_id);
CREATE INDEX idx_ceremonies_status ON ceremonies(status);
CREATE INDEX idx_ceremonies_chain_id ON ceremonies(chain_id);
CREATE INDEX idx_ceremonies_threshold_address ON ceremonies(threshold_address);

-- Ceremony parties table: tracks which parties participate in ceremony
CREATE TABLE IF NOT EXISTS ceremony_parties (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  ceremony_id UUID NOT NULL REFERENCES ceremonies(id) ON DELETE CASCADE,
  party_id INTEGER NOT NULL,  -- 0-indexed party number
  party_endpoint VARCHAR NOT NULL,  -- DNS/IP for party service
  status VARCHAR NOT NULL DEFAULT 'pending',  -- pending, joined, committed, completed, failed
  public_key VARCHAR,  -- party's secp256k1 public key
  key_share_sealed_at TIMESTAMP,  -- when key share was sealed in Vault
  joined_at TIMESTAMP,
  failed_at TIMESTAMP,
  error_message TEXT,
  metadata JSONB,  -- DKG round data, commitments
  CONSTRAINT party_unique UNIQUE(ceremony_id, party_id),
  CONSTRAINT party_status CHECK (status IN ('pending', 'joined', 'committed', 'completed', 'failed'))
);

CREATE INDEX idx_ceremony_parties_ceremony_id ON ceremony_parties(ceremony_id);
CREATE INDEX idx_ceremony_parties_party_id ON ceremony_parties(ceremony_id, party_id);
CREATE INDEX idx_ceremony_parties_status ON ceremony_parties(status);

-- Ceremony rounds table: tracks DKG rounds (phases 1-7 per TSS-Lib)
CREATE TABLE IF NOT EXISTS ceremony_rounds (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  ceremony_id UUID NOT NULL REFERENCES ceremonies(id) ON DELETE CASCADE,
  round_number INTEGER NOT NULL,
  status VARCHAR NOT NULL DEFAULT 'pending',  -- pending, in_progress, completed, failed
  started_at TIMESTAMP,
  completed_at TIMESTAMP,
  failed_at TIMESTAMP,
  error_message TEXT,
  CONSTRAINT round_unique UNIQUE(ceremony_id, round_number),
  CONSTRAINT round_status CHECK (status IN ('pending', 'in_progress', 'completed', 'failed'))
);

CREATE INDEX idx_ceremony_rounds_ceremony_id ON ceremony_rounds(ceremony_id);
CREATE INDEX idx_ceremony_rounds_status ON ceremony_rounds(status);

-- Ceremony messages table: audit log of DKG messages (optional, for debugging)
CREATE TABLE IF NOT EXISTS ceremony_messages (
  id BIGSERIAL PRIMARY KEY,
  ceremony_id UUID NOT NULL REFERENCES ceremonies(id) ON DELETE CASCADE,
  from_party_id INTEGER,
  to_party_id INTEGER,
  round_number INTEGER NOT NULL,
  message_type VARCHAR NOT NULL,  -- "commitment", "share", "proof", etc.
  received_at TIMESTAMP DEFAULT NOW(),
  hash VARCHAR  -- hash of message for verification
);

CREATE INDEX idx_ceremony_messages_ceremony_id ON ceremony_messages(ceremony_id);
CREATE INDEX idx_ceremony_messages_round ON ceremony_messages(ceremony_id, round_number);

-- Multi-chain transactions table: extends signing.transactions for threshold signing
CREATE TABLE IF NOT EXISTS threshold_transactions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  ceremony_id UUID NOT NULL REFERENCES ceremonies(id),
  chain_id VARCHAR NOT NULL,  -- "ethereum", "bitcoin", "solana", "cosmos-hub"
  message_hash VARCHAR NOT NULL,  -- hash of transaction to sign
  message_bytes BYTEA,  -- transaction bytes (RLP, script, instruction, etc.)
  signature_hex VARCHAR,  -- resulting signature (65 bytes hex)
  raw_tx BYTEA,  -- signed transaction (ready to broadcast)
  status VARCHAR NOT NULL DEFAULT 'pending',  -- pending, signing, signed, broadcast, confirmed, failed
  signing_started_at TIMESTAMP,
  signed_at TIMESTAMP,
  broadcast_at TIMESTAMP,
  confirmed_at TIMESTAMP,
  failed_at TIMESTAMP,
  broadcast_tx_hash VARCHAR,  -- on-chain transaction hash
  confirmation_count INTEGER DEFAULT 0,  -- number of confirmations
  error_message TEXT,
  metadata JSONB,  -- chain-specific data (nonce, fee, etc.)
  created_at TIMESTAMP DEFAULT NOW(),
  CONSTRAINT tx_status CHECK (status IN ('pending', 'signing', 'signed', 'broadcast', 'confirmed', 'failed'))
);

CREATE INDEX idx_threshold_transactions_customer_id ON threshold_transactions(customer_id);
CREATE INDEX idx_threshold_transactions_ceremony_id ON threshold_transactions(ceremony_id);
CREATE INDEX idx_threshold_transactions_chain_id ON threshold_transactions(chain_id);
CREATE INDEX idx_threshold_transactions_status ON threshold_transactions(status);
CREATE INDEX idx_threshold_transactions_broadcast_tx_hash ON threshold_transactions(broadcast_tx_hash);

-- Key shares rotation table: track key share version/rotation
CREATE TABLE IF NOT EXISTS key_share_rotations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  old_ceremony_id UUID REFERENCES ceremonies(id),  -- previous ceremony
  new_ceremony_id UUID NOT NULL REFERENCES ceremonies(id),  -- new ceremony
  initiated_at TIMESTAMP DEFAULT NOW(),
  completed_at TIMESTAMP,
  status VARCHAR NOT NULL DEFAULT 'in_progress',  -- in_progress, completed, failed
  CONSTRAINT rotation_status CHECK (status IN ('in_progress', 'completed', 'failed'))
);

CREATE INDEX idx_key_share_rotations_customer_id ON key_share_rotations(customer_id);
CREATE INDEX idx_key_share_rotations_status ON key_share_rotations(status);

-- Customer chain configuration: which chains customer is configured for
CREATE TABLE IF NOT EXISTS customer_chains (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  chain_id VARCHAR NOT NULL,  -- "ethereum", "bitcoin", "solana", "cosmos-hub"
  active_ceremony_id UUID REFERENCES ceremonies(id),  -- current ceremony for this chain
  rpc_endpoint VARCHAR,  -- optional custom RPC endpoint
  broadcast_enabled BOOLEAN DEFAULT TRUE,  -- whether to auto-broadcast
  created_at TIMESTAMP DEFAULT NOW(),
  CONSTRAINT customer_chain_unique UNIQUE(customer_id, chain_id)
);

CREATE INDEX idx_customer_chains_customer_id ON customer_chains(customer_id);
CREATE INDEX idx_customer_chains_chain_id ON customer_chains(chain_id);

-- Audit log updates (PostgreSQL audit.events already exists)
-- Add these new event types to the logging system:
-- - CEREMONY_INITIATED
-- - CEREMONY_ROUND_STARTED / COMPLETED / FAILED
-- - CEREMONY_PARTY_JOINED / FAILED
-- - CEREMONY_COMPLETED / FAILED
-- - KEY_SHARE_SEALED
-- - THRESHOLD_SIGN_INITIATED / COMPLETED / FAILED
-- - MULTI_CHAIN_BROADCAST_INITIATED / SUCCESS / FAILED

-- Constraints and foreign keys
ALTER TABLE threshold_transactions
  ADD CONSTRAINT fk_ceremony FOREIGN KEY (ceremony_id) REFERENCES ceremonies(id) ON DELETE RESTRICT;

ALTER TABLE customer_chains
  ADD CONSTRAINT fk_active_ceremony FOREIGN KEY (active_ceremony_id) REFERENCES ceremonies(id) ON DELETE SET NULL;
