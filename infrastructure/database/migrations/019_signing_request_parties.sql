-- Which parties actually produced a signature.
--
-- signing_requests has recorded what was signed since the first migration.
-- It has never recorded who signed it, because until recently the committee
-- was a fixed prefix of the party list and there was nothing to record --
-- a 2-of-3 key always used parties 1 and 2.
--
-- That is no longer true. The committee is chosen from the parties that are
-- reachable when the request arrives, which is what makes a k-of-n key
-- survive losing a party (see infrastructure/kind/node-failure-drill.sh).
-- So "which parties signed this" is now a real question with a per-request
-- answer, and it is the question an auditor asks first: a threshold
-- signature is only meaningful if you can say which shares combined to
-- produce it.
--
-- Nullable, because rows written before this column existed genuinely do
-- not know. Backfilling a plausible-looking committee would be inventing
-- audit data, which is worse than admitting the gap.
ALTER TABLE signing_requests
  ADD COLUMN IF NOT EXISTS signing_parties INTEGER[];

COMMENT ON COLUMN signing_requests.signing_parties IS
  'Party ids that formed the signing committee. NULL for requests recorded before the column existed.';
