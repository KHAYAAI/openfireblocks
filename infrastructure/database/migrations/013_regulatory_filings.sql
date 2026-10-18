-- Tracks SAR (Suspicious Activity Report, FinCEN Form 111) and CTR
-- (Currency Transaction Report, FinCEN Form 104) candidates/filings.
--
-- What this table is NOT: a substitute for legal/compliance sign-off on
-- filing obligations. The $10,000 CTR threshold and 15/30-day filing
-- deadlines encoded in services/compliance/regulatory_reporting.go are the
-- well-known FinCEN defaults for US currency transactions -- this platform
-- may also owe filings under other regimes (state MSB rules, non-US
-- equivalents) that are not modeled here at all. See
-- docs/security/what-claude-cannot-build.md section 1. This table and its
-- generator produce a real, correctly-shaped draft from real transaction
-- data; a human compliance officer reviews and actually files it.

CREATE TABLE regulatory_filings (
  filing_id               VARCHAR(255) PRIMARY KEY,
  filing_type             VARCHAR(10) NOT NULL CHECK (filing_type IN ('SAR', 'CTR')),
  customer_id             UUID NOT NULL REFERENCES customers(customer_id),
  related_transaction_ids JSONB NOT NULL DEFAULT '[]'::JSONB,
  chain                   VARCHAR(50) NOT NULL,
  aggregate_amount_native VARCHAR(255) NOT NULL, -- sum in the chain's native unit (wei, sats, ...), always computable
  aggregate_amount_usd    DOUBLE PRECISION,        -- NULL unless a price oracle was available at evaluation time
  usd_conversion_rate     DOUBLE PRECISION,        -- the rate used, for audit -- NULL alongside aggregate_amount_usd
  threshold_usd           DOUBLE PRECISION NOT NULL DEFAULT 10000, -- FinCEN CTR default; confirm per jurisdiction before relying on it
  detection_method        VARCHAR(50) NOT NULL, -- 'ctr_threshold', 'structuring_heuristic', 'manual'
  narrative                TEXT, -- required before a SAR can move past 'draft'
  status                   VARCHAR(50) NOT NULL DEFAULT 'draft'
                              CHECK (status IN ('draft', 'pending_review', 'ready_to_file', 'filed', 'not_required')),
  detected_at              TIMESTAMP NOT NULL DEFAULT NOW(),
  filing_deadline          TIMESTAMP NOT NULL, -- detected_at + 15 days (CTR) or + 30 days (SAR), per FinCEN defaults
  filed_at                 TIMESTAMP,
  filed_by                 VARCHAR(255),
  confirmation_number      VARCHAR(255), -- FinCEN BSA E-Filing confirmation, once actually filed out-of-band
  created_at               TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at                TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_regulatory_filings_customer ON regulatory_filings(customer_id);
CREATE INDEX idx_regulatory_filings_status ON regulatory_filings(status);
CREATE INDEX idx_regulatory_filings_type ON regulatory_filings(filing_type);
CREATE INDEX idx_regulatory_filings_deadline ON regulatory_filings(filing_deadline);

-- RLS here isn't tenant isolation (there's no per-customer session that
-- should see even its own rows -- SAR confidentiality is a legal
-- requirement: a customer must never learn a SAR exists about them, not
-- even their own). It's a hard deny-all for the `app` role as
-- defense-in-depth, in case this table is ever queried through a
-- customer-facing path by mistake. services/compliance (connected as
-- app_admin, which has BYPASSRLS) is the only intended reader/writer.
ALTER TABLE regulatory_filings ENABLE ROW LEVEL SECURITY;
ALTER TABLE regulatory_filings FORCE ROW LEVEL SECURITY;
CREATE POLICY deny_all ON regulatory_filings USING (false) WITH CHECK (false);
