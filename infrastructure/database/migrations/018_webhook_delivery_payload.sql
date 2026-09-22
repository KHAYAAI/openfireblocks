-- Stores the event payload alongside each delivery attempt, so a failed
-- delivery can actually be retried.
--
-- webhook_deliveries recorded only the *outcome* of an attempt -- status
-- code, timing, error -- and nothing about what was sent. That made retry
-- impossible to implement: RetryWebhookDelivery could find the row, confirm
-- it had failed, confirm it was under the retry limit, and then had nothing
-- to resend. It returned "not implemented" rather than pretending.
--
-- The payload is the exact bytes that were signed and sent, not a
-- reconstruction. A retry has to deliver a byte-identical body or its
-- X-Webhook-Signature will not match what the receiver computes, and the
-- receiver will reject a retry that is materially the same event. Storing
-- the event and re-marshalling it would risk a different key order.
ALTER TABLE webhook_deliveries
  ADD COLUMN IF NOT EXISTS payload TEXT;

COMMENT ON COLUMN webhook_deliveries.payload IS
  'The exact JSON body that was sent and signed for this attempt. Required '
  'for retry: the HMAC signature is over these bytes, so a retry must send '
  'them verbatim rather than re-marshalling the event.';

-- Finds deliveries that are due for a retry.
--
-- Partial index: the overwhelming majority of rows are successful
-- deliveries with next_retry_at NULL, and a sweeper only ever cares about
-- the failed minority that are actually scheduled.
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_due_for_retry
  ON webhook_deliveries (next_retry_at)
  WHERE success = FALSE AND next_retry_at IS NOT NULL;
