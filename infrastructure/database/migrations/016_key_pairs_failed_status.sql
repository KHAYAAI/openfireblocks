-- Lets a key whose provisioning never started be recorded as failed, and
-- lets the customer retry under the same name.
--
-- Found by running the real customer path against a real cluster.
-- KeysService.createKey inserts the key_pairs and dkg_ceremonies rows and
-- then starts the Temporal workflow. When that start fails -- Temporal
-- unreachable, say -- the ceremony is correctly marked 'failed', but the
-- key_pairs row was left at 'pending_dkg' with no workflow behind it, and
-- two things followed:
--
--   1. The key read as perpetually "provisioning" and nothing would ever
--      move it, because the workflow that would have moved it was never
--      started.
--   2. UNIQUE (customer_id, name) meant the dead row permanently consumed
--      the name. A customer retrying the same request got a raw 500
--      carrying the Postgres constraint text
--      ("duplicate key value violates unique constraint
--      key_pairs_customer_id_name_key") -- the one failure mode most
--      likely to follow a transient outage was itself unrecoverable.
--
-- Rather than deleting the failed attempt (which would erase an honest
-- record of a ceremony that was requested), this adds a terminal 'failed'
-- status and narrows the uniqueness rule to exclude it. The audit trail
-- keeps the attempt; the name is released.

ALTER TABLE key_pairs DROP CONSTRAINT IF EXISTS key_pairs_status_check;
ALTER TABLE key_pairs ADD CONSTRAINT key_pairs_status_check
  CHECK (status IN ('pending_dkg', 'active', 'inactive', 'compromised', 'failed'));

-- A partial unique index rather than a plain constraint: names must stay
-- unique among keys that exist as far as the customer is concerned, while
-- any number of failed attempts may share a name.
ALTER TABLE key_pairs DROP CONSTRAINT IF EXISTS key_pairs_customer_id_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS key_pairs_customer_name_live_key
  ON key_pairs (customer_id, name)
  WHERE status <> 'failed';
