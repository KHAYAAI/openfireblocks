-- Persists what AuditManager, IncidentManager, and ComplianceMonitor
-- (services/compliance/audit.go, incident_response.go, monitoring.go) used
-- to hold only in Go maps. None of that state survived a process restart or
-- was visible to any other instance of the service -- for a SOC 2 evidence
-- trail specifically, "the audit findings existed until someone redeployed"
-- is not durable evidence.
--
-- Timeline/NotificationsPending/PostIncidentReview on incidents, and
-- Evidence/TestingSteps on audit records, are kept as JSONB rather than
-- fully normalized into their own tables -- they're always read/written as
-- a whole with their parent record, never queried independently, so a
-- normalized join would add complexity without adding a real capability.

CREATE TABLE security_audits (
  audit_id          VARCHAR(255) PRIMARY KEY,
  type              VARCHAR(50) NOT NULL,
  scope             VARCHAR(50) NOT NULL,
  status            VARCHAR(50) NOT NULL DEFAULT 'planned',
  scheduled_start   TIMESTAMP,
  scheduled_end     TIMESTAMP,
  actual_start      TIMESTAMP,
  actual_end        TIMESTAMP,
  auditor           VARCHAR(255),
  critical_count    INT NOT NULL DEFAULT 0,
  high_count        INT NOT NULL DEFAULT 0,
  medium_count      INT NOT NULL DEFAULT 0,
  low_count         INT NOT NULL DEFAULT 0,
  score             DOUBLE PRECISION NOT NULL DEFAULT 100,
  report_path       VARCHAR(2048),
  certificate_path  VARCHAR(2048),
  notes             TEXT,
  created_at        TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_security_audits_status ON security_audits(status);
CREATE INDEX idx_security_audits_type ON security_audits(type);

CREATE TABLE audit_findings (
  finding_id        VARCHAR(255) PRIMARY KEY,
  audit_id          VARCHAR(255) NOT NULL REFERENCES security_audits(audit_id) ON DELETE CASCADE,
  control_id        VARCHAR(100),
  title             VARCHAR(500) NOT NULL,
  description       TEXT,
  severity          VARCHAR(20) NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low')),
  category          VARCHAR(100),
  evidence          JSONB NOT NULL DEFAULT '[]'::JSONB,
  root_cause        TEXT,
  recommendation    TEXT,
  remediation_plan  TEXT,
  status            VARCHAR(50) NOT NULL DEFAULT 'open',
  due_date          TIMESTAMP,
  resolved_at       TIMESTAMP,
  created_at        TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_findings_audit ON audit_findings(audit_id);
CREATE INDEX idx_audit_findings_status ON audit_findings(status);
CREATE INDEX idx_audit_findings_severity ON audit_findings(severity);

CREATE TABLE audit_checklists (
  checklist_id    VARCHAR(255) PRIMARY KEY,
  audit_id        VARCHAR(255) NOT NULL REFERENCES security_audits(audit_id) ON DELETE CASCADE,
  control_id      VARCHAR(100),
  control_name    VARCHAR(500),
  test_method     VARCHAR(50),
  testing_steps   JSONB NOT NULL DEFAULT '[]'::JSONB,
  evidence        JSONB NOT NULL DEFAULT '[]'::JSONB,
  status          VARCHAR(50) NOT NULL DEFAULT 'not_started',
  result          VARCHAR(20),
  notes           TEXT,
  tested_at       TIMESTAMP,
  tested_by       VARCHAR(255)
);

CREATE INDEX idx_audit_checklists_audit ON audit_checklists(audit_id);

CREATE TABLE security_incidents (
  incident_id             VARCHAR(255) PRIMARY KEY,
  title                   VARCHAR(500) NOT NULL,
  description             TEXT,
  type                    VARCHAR(50) NOT NULL,
  severity                VARCHAR(20) NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low')),
  status                  VARCHAR(50) NOT NULL DEFAULT 'reported',
  reported_at             TIMESTAMP,
  discovered_at           TIMESTAMP,
  acknowledged_at         TIMESTAMP,
  contained_at            TIMESTAMP,
  resolved_at             TIMESTAMP,
  closed_at               TIMESTAMP,
  reported_by             VARCHAR(255),
  assigned_to             VARCHAR(255),
  affected_systems        JSONB NOT NULL DEFAULT '[]'::JSONB,
  affected_customers      JSONB NOT NULL DEFAULT '[]'::JSONB,
  impacted_data_types     JSONB NOT NULL DEFAULT '[]'::JSONB,
  data_exposure_count     BIGINT,
  root_cause              TEXT,
  remediation_steps       JSONB NOT NULL DEFAULT '[]'::JSONB,
  mttr_ns                 BIGINT, -- time.Duration, nanoseconds
  mttd_ns                 BIGINT,
  timeline                JSONB NOT NULL DEFAULT '[]'::JSONB,
  evidence                JSONB NOT NULL DEFAULT '[]'::JSONB,
  notifications_required  BOOLEAN NOT NULL DEFAULT FALSE,
  notifications_pending   JSONB NOT NULL DEFAULT '[]'::JSONB,
  post_incident_review    JSONB
);

CREATE INDEX idx_security_incidents_status ON security_incidents(status);
CREATE INDEX idx_security_incidents_severity ON security_incidents(severity);
CREATE INDEX idx_security_incidents_reported_at ON security_incidents(reported_at);

CREATE TABLE incident_response_plans (
  plan_id             VARCHAR(255) PRIMARY KEY,
  name                VARCHAR(500) NOT NULL,
  estimated_rto_ns    BIGINT,
  estimated_rpo_ns    BIGINT,
  escalation_path     JSONB NOT NULL DEFAULT '[]'::JSONB,
  notification_list   JSONB NOT NULL DEFAULT '[]'::JSONB,
  regulatory_reqs     JSONB NOT NULL DEFAULT '[]'::JSONB,
  included_systems    JSONB NOT NULL DEFAULT '[]'::JSONB,
  last_tested_at      TIMESTAMP,
  test_frequency_ns   BIGINT
);

CREATE TABLE compliance_metrics (
  metric_id     VARCHAR(255) PRIMARY KEY,
  name          VARCHAR(500) NOT NULL,
  category      VARCHAR(100) NOT NULL,
  value         DOUBLE PRECISION NOT NULL,
  unit          VARCHAR(50),
  threshold     DOUBLE PRECISION,
  status        VARCHAR(20) NOT NULL,
  measured_at   TIMESTAMP NOT NULL DEFAULT NOW(),
  description   TEXT,
  target        DOUBLE PRECISION
);

CREATE INDEX idx_compliance_metrics_category ON compliance_metrics(category);
CREATE INDEX idx_compliance_metrics_measured_at ON compliance_metrics(measured_at);

CREATE TABLE compliance_alerts (
  alert_id            VARCHAR(255) PRIMARY KEY,
  metric_id           VARCHAR(255) REFERENCES compliance_metrics(metric_id) ON DELETE SET NULL,
  severity            VARCHAR(20) NOT NULL,
  message             TEXT,
  recommended_action  TEXT,
  created_at          TIMESTAMP NOT NULL DEFAULT NOW(),
  resolved_at         TIMESTAMP,
  status              VARCHAR(20) NOT NULL DEFAULT 'open'
);

CREATE INDEX idx_compliance_alerts_status ON compliance_alerts(status);
