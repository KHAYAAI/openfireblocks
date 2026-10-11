-- Access reviews: a person with the authority to do so looks at who can do what in the
-- organisation, and signs that they did.
--
-- SOC 2 CC6.2/CC6.3 and ISO 27001 A.5.18 ask for exactly this, periodically, with a record. The
-- record is only worth something if it shows what the reviewer actually saw, so the snapshot of
-- members, roles, agent keys and the API key is taken by the platform at the moment of signing
-- (not supplied by the reviewer), stored with the review, and never changed afterwards.
CREATE TABLE IF NOT EXISTS access_reviews (
  review_id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id    UUID NOT NULL REFERENCES customers(customer_id),
  reviewed_by    UUID NOT NULL REFERENCES users(id),
  reviewer_email TEXT NOT NULL,
  reviewer_role  VARCHAR(20) NOT NULL,
  -- How the reviewer proved presence: a fresh one-time code, or a recent SSO sign-in.
  verified_by    VARCHAR(10) NOT NULL CHECK (verified_by IN ('totp', 'sso')),
  outcome        VARCHAR(20) NOT NULL CHECK (outcome IN ('no_changes', 'changes_made', 'changes_required')),
  notes          TEXT CHECK (notes IS NULL OR length(notes) <= 4000),
  snapshot       JSONB NOT NULL,
  -- The flags the platform raised on that snapshot (no MFA, stale sign-in, agent with no
  -- recipient list, old API key, ...), so a later reader sees what was on the reviewer's screen.
  findings       JSONB NOT NULL DEFAULT '[]'::jsonb,
  -- SHA-256 of the canonical snapshot, so an exported copy can be checked against the record.
  snapshot_sha256 CHAR(64) NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_access_reviews_customer ON access_reviews (customer_id, created_at DESC);

-- A review is evidence. It is never edited and never deleted.
CREATE OR REPLACE FUNCTION access_reviews_guard() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'access reviews are retained as recorded: they cannot be changed or deleted' USING ERRCODE = 'OFB06';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS access_reviews_guard ON access_reviews;
CREATE TRIGGER access_reviews_guard BEFORE UPDATE OR DELETE ON access_reviews
  FOR EACH ROW EXECUTE FUNCTION access_reviews_guard();

ALTER TABLE access_reviews ENABLE ROW LEVEL SECURITY;
ALTER TABLE access_reviews FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON access_reviews;
CREATE POLICY tenant_isolation ON access_reviews
  USING (customer_id = current_customer_id())
  WITH CHECK (customer_id = current_customer_id());

-- Read-only for the evidence collector and operators; nothing but the gateway writes reviews.
GRANT SELECT ON access_reviews TO app_admin;
