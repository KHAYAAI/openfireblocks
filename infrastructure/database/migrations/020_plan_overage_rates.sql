-- What a customer pays for going past their plan's limits.
--
-- plans has carried signing_limit and key_limit since billing was added,
-- and nothing has ever been able to charge for exceeding them: the limits
-- were compared against usage, a line was logged, and the request was
-- served anyway. So the limits were advisory and the platform had exactly
-- one price per plan however much a customer used it.
--
-- Two rates rather than one, because the two units cost very different
-- amounts to serve. A signature is a threshold ceremony across a committee
-- -- seconds of coordinated work between processes on separate nodes. A
-- key is a distributed key generation, which is far more expensive again
-- and happens once, after which the key costs nothing to keep.
--
-- Zero means no overage charge, which is the honest default for plans
-- created before this column existed: they were sold without an overage
-- rate, and inventing one retroactively would bill people for something
-- they never agreed to.
ALTER TABLE plans
  ADD COLUMN IF NOT EXISTS overage_signing_cents INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS overage_key_cents INT NOT NULL DEFAULT 0;

COMMENT ON COLUMN plans.overage_signing_cents IS
  'Cents charged per signature beyond signing_limit. 0 disables overage billing.';
COMMENT ON COLUMN plans.overage_key_cents IS
  'Cents charged per key beyond key_limit. 0 disables overage billing.';

-- One invoice per subscription per billing period.
--
-- Invoice generation has to be safe to retry: it will be driven by a
-- schedule, and a schedule that fires twice -- a retry, an operator, an
-- overlapping run -- must not produce two invoices for the same month and
-- charge the customer twice. Enforced here rather than in the service
-- because a uniqueness rule that lives in application code is a race
-- condition with extra steps.
ALTER TABLE invoices
  ADD COLUMN IF NOT EXISTS period_start TIMESTAMP,
  ADD COLUMN IF NOT EXISTS period_end TIMESTAMP;

CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_subscription_period
  ON invoices (subscription_id, period_start)
  WHERE period_start IS NOT NULL;
