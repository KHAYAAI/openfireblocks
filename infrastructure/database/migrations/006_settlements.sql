-- Settlement records (services/settlement). Distinct from signing_requests:
-- a signing_request is "was this transaction signed"; a settlement is "was
-- the already-signed transaction broadcast and confirmed on-chain."
CREATE TABLE settlements (
  settlement_id     UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  signing_id        UUID REFERENCES signing_requests(request_id),
  customer_id       UUID REFERENCES customers(customer_id),
  blockchain        VARCHAR(50) NOT NULL,
  transaction_hash  VARCHAR(255),
  status            VARCHAR(50) NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'broadcasted', 'confirmed', 'failed', 'timeout')),
  gas_used          BIGINT,
  confirmation_time_seconds BIGINT,
  broadcasted_at    TIMESTAMP,
  confirmed_at      TIMESTAMP,
  error_message     TEXT,
  created_at        TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_settlements_customer ON settlements(customer_id);
CREATE INDEX idx_settlements_signing ON settlements(signing_id);
CREATE INDEX idx_settlements_status ON settlements(status);
