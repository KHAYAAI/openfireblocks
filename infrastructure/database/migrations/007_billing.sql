-- services/billing: subscription plans, active subscriptions, invoices, and
-- per-period usage metrics. None of these existed in the schema at all.
CREATE TABLE plans (
  plan_id       UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name          VARCHAR(255) NOT NULL,
  description   TEXT,
  price_cents   INT NOT NULL,
  currency      VARCHAR(10) NOT NULL DEFAULT 'usd',
  billing_cycle VARCHAR(20) NOT NULL DEFAULT 'monthly' CHECK (billing_cycle IN ('monthly', 'yearly')),
  signing_limit INT NOT NULL DEFAULT 0,
  key_limit     INT NOT NULL DEFAULT 0,
  support_level VARCHAR(20) NOT NULL DEFAULT 'basic',
  features      JSONB NOT NULL DEFAULT '[]'::JSONB,
  created_at    TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE subscriptions (
  subscription_id      UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id          UUID NOT NULL REFERENCES customers(customer_id),
  plan_id              UUID NOT NULL REFERENCES plans(plan_id),
  status               VARCHAR(50) NOT NULL DEFAULT 'active'
                          CHECK (status IN ('active', 'paused', 'canceled', 'past_due', 'scheduled_for_cancellation')),
  current_period_start TIMESTAMP NOT NULL,
  current_period_end   TIMESTAMP NOT NULL,
  canceled_at          TIMESTAMP,
  trial_ends_at        TIMESTAMP,
  auto_renew           BOOLEAN NOT NULL DEFAULT TRUE,
  payment_method       VARCHAR(255),
  created_at           TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at           TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_subscriptions_customer ON subscriptions(customer_id);
CREATE INDEX idx_subscriptions_status ON subscriptions(status);

CREATE TABLE invoices (
  invoice_id      UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  subscription_id UUID NOT NULL REFERENCES subscriptions(subscription_id),
  customer_id     UUID NOT NULL REFERENCES customers(customer_id),
  amount_cents    INT NOT NULL,
  currency        VARCHAR(10) NOT NULL DEFAULT 'usd',
  status          VARCHAR(50) NOT NULL DEFAULT 'unpaid' CHECK (status IN ('paid', 'unpaid', 'overdue')),
  due_date        TIMESTAMP NOT NULL,
  paid_at         TIMESTAMP,
  line_items      JSONB NOT NULL DEFAULT '[]'::JSONB,
  created_at      TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_invoices_customer ON invoices(customer_id);
CREATE INDEX idx_invoices_subscription ON invoices(subscription_id);

CREATE TABLE usage_metrics (
  metrics_id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  subscription_id    UUID NOT NULL REFERENCES subscriptions(subscription_id),
  customer_id        UUID NOT NULL REFERENCES customers(customer_id),
  period_start       TIMESTAMP NOT NULL,
  period_end         TIMESTAMP NOT NULL,
  signing_requests   INT NOT NULL DEFAULT 0,
  key_operations     INT NOT NULL DEFAULT 0,
  api_requests       INT NOT NULL DEFAULT 0,
  data_transfer_gb   DOUBLE PRECISION NOT NULL DEFAULT 0,
  available_signings INT NOT NULL DEFAULT 0,
  available_keys     INT NOT NULL DEFAULT 0,
  created_at         TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_usage_metrics_subscription ON usage_metrics(subscription_id);
CREATE INDEX idx_usage_metrics_created ON usage_metrics(created_at);
