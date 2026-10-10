-- Tenant API keys get a lifecycle: expiry and rotation.
--
-- A tenant's API key used to be a permanent credential. customers held exactly one
-- api_key_hash with no expiry and no rotation path, so a key captured out of band
-- (from a browser's localStorage, a leaked config, a log) authenticated indefinitely and
-- the only remedy was suspending the whole tenant through an admin-only route.
--
-- NULL api_key_expires_at keeps today's behaviour (no expiry) for every existing key;
-- an operator opts in per key at rotation, or deployment-wide with API_KEY_TTL_DAYS.
ALTER TABLE customers
  ADD COLUMN IF NOT EXISTS api_key_expires_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS api_key_rotated_at TIMESTAMPTZ;
