-- Denormalized "current" KYC status per customer. The durable event history
-- lives in compliance_checks (one row per verification attempt); this is
-- just the fast-lookup current state so callers don't have to re-derive
-- "most recent kyc_verification_onfido row" on every request.

ALTER TABLE customers
  ADD COLUMN kyc_status VARCHAR(50) NOT NULL DEFAULT 'not_started'
    CHECK (kyc_status IN ('not_started', 'under_review', 'approved', 'rejected')),
  ADD COLUMN kyc_verified_at TIMESTAMP;

CREATE INDEX idx_customers_kyc_status ON customers(kyc_status);
