-- Makes opaque-digest signing an explicit, per-tenant opt-in.
--
-- There are two threshold-signing routes and they do not offer the same
-- guarantee.
--
--   POST /keys/:keyId/transactions  builds and hashes the transaction
--   itself, so the transaction the policy engine evaluated and the bytes
--   that get signed are provably the same object.
--
--   POST /keys/:keyId/sign          signs a digest the caller supplies. A
--   digest is opaque, so the to/value/chainId that policy evaluates are a
--   *claim*. A caller who declares one transaction and submits the digest
--   of another gets an approval for a transaction nobody reviewed.
--
-- The second is genuinely useful -- signing things that are not Ethereum
-- transactions has no other route -- but it should not be the default any
-- customer silently gets. Off unless deliberately granted, so the strong
-- route is what a new tenant can reach and the weak one is a decision
-- somebody made on the record.
--
-- Existing customers default to false as well: this deployment has never
-- carried production traffic, so there is no established usage to break,
-- and defaulting to true "for compatibility" would grant every current and
-- future tenant the weaker guarantee to protect callers that do not exist.
ALTER TABLE customers
  ADD COLUMN IF NOT EXISTS raw_digest_signing_enabled BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN customers.raw_digest_signing_enabled IS
  'Allows POST /keys/:keyId/sign, which signs a caller-supplied opaque digest. '
  'Policy cannot verify what such a digest commits to, so this is off by '
  'default and granting it is a deliberate per-tenant decision. Prefer '
  'POST /keys/:keyId/transactions, where the gateway builds and hashes the '
  'transaction it signs.';
