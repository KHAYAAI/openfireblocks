-- services/webhooks: customer-configured webhook endpoints and delivery logs.
CREATE TABLE webhooks (
  webhook_id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id         UUID NOT NULL REFERENCES customers(customer_id),
  url                 VARCHAR(2048) NOT NULL,
  secret              VARCHAR(255) NOT NULL,
  events              TEXT[] NOT NULL DEFAULT '{}',
  is_active           BOOLEAN NOT NULL DEFAULT TRUE,
  max_retries         INT NOT NULL DEFAULT 3,
  backoff_seconds     INT NOT NULL DEFAULT 60,
  exponential_backoff BOOLEAN NOT NULL DEFAULT TRUE,
  custom_headers      JSONB,
  created_at          TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhooks_customer ON webhooks(customer_id);
CREATE INDEX idx_webhooks_active ON webhooks(is_active);

CREATE TABLE webhook_deliveries (
  delivery_id     UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  webhook_id      UUID NOT NULL REFERENCES webhooks(webhook_id),
  event_id        UUID NOT NULL,
  event_type      VARCHAR(100) NOT NULL,
  attempt         INT NOT NULL DEFAULT 1,
  status_code     INT,
  response_time_ms INT,
  success         BOOLEAN NOT NULL DEFAULT FALSE,
  error_message   TEXT,
  next_retry_at   TIMESTAMP,
  created_at      TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id);
CREATE INDEX idx_webhook_deliveries_created ON webhook_deliveries(created_at);
