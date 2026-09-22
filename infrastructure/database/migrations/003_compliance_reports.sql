-- Generated compliance/audit reports (services/compliance/reporting.go).
-- Without this table, GenerateAuditReport/GenerateComplianceReport would
-- compute a real report and then have nowhere to put it -- "real" data
-- that vanishes on process restart is no more useful than the mock data
-- it replaced.

CREATE TABLE compliance_reports (
  report_id    UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  customer_id  UUID REFERENCES customers(customer_id), -- NULL for platform-wide reports
  report_type  VARCHAR(50) NOT NULL, -- audit, compliance, risk, kyc
  period       VARCHAR(50) NOT NULL, -- monthly, quarterly, yearly, custom
  start_date   TIMESTAMP NOT NULL,
  end_date     TIMESTAMP NOT NULL,
  status       VARCHAR(50) NOT NULL DEFAULT 'generating', -- generating, completed, failed
  data         JSONB,
  file_url     VARCHAR(2048),
  generated_at TIMESTAMP NOT NULL DEFAULT NOW(),
  expires_at   TIMESTAMP
);

CREATE INDEX idx_compliance_reports_customer ON compliance_reports(customer_id);
CREATE INDEX idx_compliance_reports_type ON compliance_reports(report_type);
CREATE INDEX idx_compliance_reports_generated ON compliance_reports(generated_at);
