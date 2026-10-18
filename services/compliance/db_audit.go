package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// -- Security audits (security_audits table, migration 010) --
//
// SecurityAudit has no CreatedAt/UpdatedAt fields (audit.go), so those
// columns are left to their DB defaults and never read back into the
// struct -- there's nowhere on the struct to put them.

func (p *PostgresDB) CreateSecurityAudit(ctx context.Context, audit *SecurityAudit) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO security_audits (audit_id, type, scope, status, scheduled_start, scheduled_end, actual_start, actual_end, auditor, critical_count, high_count, medium_count, low_count, score, report_path, certificate_path, notes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, audit.ID, audit.Type, audit.Scope, audit.Status, nullableTime(audit.ScheduledStart), nullableTime(audit.ScheduledEnd), nullableTime(audit.ActualStart), nullableTime(audit.ActualEnd), audit.Auditor, audit.CriticalCount, audit.HighCount, audit.MediumCount, audit.LowCount, audit.Score, audit.ReportPath, audit.CertificatePath, audit.Notes)
	if err != nil {
		return fmt.Errorf("failed to insert security audit: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetSecurityAudit(ctx context.Context, auditID string) (*SecurityAudit, error) {
	var a SecurityAudit
	var scheduledStart, scheduledEnd, actualStart, actualEnd sql.NullTime
	var auditor, reportPath, certificatePath, notes sql.NullString
	row := p.db.QueryRowContext(ctx, `
		SELECT audit_id, type, scope, status, scheduled_start, scheduled_end, actual_start, actual_end, auditor, critical_count, high_count, medium_count, low_count, score, report_path, certificate_path, notes
		FROM security_audits WHERE audit_id = $1
	`, auditID)
	if err := row.Scan(&a.ID, &a.Type, &a.Scope, &a.Status, &scheduledStart, &scheduledEnd, &actualStart, &actualEnd, &auditor, &a.CriticalCount, &a.HighCount, &a.MediumCount, &a.LowCount, &a.Score, &reportPath, &certificatePath, &notes); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("security audit %s not found", auditID)
		}
		return nil, fmt.Errorf("failed to query security audit: %w", err)
	}
	a.ScheduledStart, a.ScheduledEnd, a.ActualStart, a.ActualEnd = scheduledStart.Time, scheduledEnd.Time, actualStart.Time, actualEnd.Time
	a.Auditor, a.ReportPath, a.CertificatePath, a.Notes = auditor.String, reportPath.String, certificatePath.String, notes.String
	return &a, nil
}

func (p *PostgresDB) UpdateSecurityAudit(ctx context.Context, audit *SecurityAudit) error {
	res, err := p.db.ExecContext(ctx, `
		UPDATE security_audits SET type = $2, scope = $3, status = $4, scheduled_start = $5, scheduled_end = $6, actual_start = $7, actual_end = $8, auditor = $9, critical_count = $10, high_count = $11, medium_count = $12, low_count = $13, score = $14, report_path = $15, certificate_path = $16, notes = $17, updated_at = NOW()
		WHERE audit_id = $1
	`, audit.ID, audit.Type, audit.Scope, audit.Status, nullableTime(audit.ScheduledStart), nullableTime(audit.ScheduledEnd), nullableTime(audit.ActualStart), nullableTime(audit.ActualEnd), audit.Auditor, audit.CriticalCount, audit.HighCount, audit.MediumCount, audit.LowCount, audit.Score, audit.ReportPath, audit.CertificatePath, audit.Notes)
	if err != nil {
		return fmt.Errorf("failed to update security audit: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("security audit %s not found", audit.ID)
	}
	return nil
}

func (p *PostgresDB) ListSecurityAuditsByStatus(ctx context.Context, status string) ([]SecurityAudit, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT audit_id, type, scope, status, scheduled_start, scheduled_end, actual_start, actual_end, auditor, critical_count, high_count, medium_count, low_count, score, report_path, certificate_path, notes
		FROM security_audits WHERE status = $1 ORDER BY scheduled_start DESC
	`, status)
	if err != nil {
		return nil, fmt.Errorf("failed to query security audits by status: %w", err)
	}
	defer rows.Close()

	var audits []SecurityAudit
	for rows.Next() {
		var a SecurityAudit
		var scheduledStart, scheduledEnd, actualStart, actualEnd sql.NullTime
		var auditor, reportPath, certificatePath, notes sql.NullString
		if err := rows.Scan(&a.ID, &a.Type, &a.Scope, &a.Status, &scheduledStart, &scheduledEnd, &actualStart, &actualEnd, &auditor, &a.CriticalCount, &a.HighCount, &a.MediumCount, &a.LowCount, &a.Score, &reportPath, &certificatePath, &notes); err != nil {
			return nil, fmt.Errorf("failed to scan security audit row: %w", err)
		}
		a.ScheduledStart, a.ScheduledEnd, a.ActualStart, a.ActualEnd = scheduledStart.Time, scheduledEnd.Time, actualStart.Time, actualEnd.Time
		a.Auditor, a.ReportPath, a.CertificatePath, a.Notes = auditor.String, reportPath.String, certificatePath.String, notes.String
		audits = append(audits, a)
	}
	return audits, rows.Err()
}

// -- Audit findings (audit_findings table) --

func (p *PostgresDB) CreateAuditFinding(ctx context.Context, f *AuditFinding) error {
	evidence, err := json.Marshal(f.Evidence)
	if err != nil {
		return fmt.Errorf("failed to marshal finding evidence: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO audit_findings (finding_id, audit_id, control_id, title, description, severity, category, evidence, root_cause, recommendation, remediation_plan, status, due_date, resolved_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`, f.ID, f.AuditID, f.ControlID, f.Title, f.Description, f.Severity, f.Category, evidence, f.RootCause, f.Recommendation, f.RemediationPlan, f.Status, nullableTime(f.DueDate), nullableTime(f.ResolvedAt), nullableTime(f.CreatedAt), nullableTime(f.UpdatedAt))
	if err != nil {
		return fmt.Errorf("failed to insert audit finding: %w", err)
	}
	return nil
}

func scanAuditFinding(row interface {
	Scan(dest ...interface{}) error
}) (*AuditFinding, error) {
	var f AuditFinding
	var evidenceRaw []byte
	var controlID, category, rootCause, recommendation, remediationPlan sql.NullString
	var dueDate, resolvedAt, createdAt, updatedAt sql.NullTime
	if err := row.Scan(&f.ID, &f.AuditID, &controlID, &f.Title, &f.Description, &f.Severity, &category, &evidenceRaw, &rootCause, &recommendation, &remediationPlan, &f.Status, &dueDate, &resolvedAt, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	f.ControlID, f.Category, f.RootCause, f.Recommendation, f.RemediationPlan = controlID.String, category.String, rootCause.String, recommendation.String, remediationPlan.String
	f.DueDate, f.ResolvedAt, f.CreatedAt, f.UpdatedAt = dueDate.Time, resolvedAt.Time, createdAt.Time, updatedAt.Time
	if len(evidenceRaw) > 0 {
		_ = json.Unmarshal(evidenceRaw, &f.Evidence)
	}
	return &f, nil
}

const auditFindingColumns = `finding_id, audit_id, control_id, title, description, severity, category, evidence, root_cause, recommendation, remediation_plan, status, due_date, resolved_at, created_at, updated_at`

func (p *PostgresDB) GetAuditFinding(ctx context.Context, findingID string) (*AuditFinding, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings WHERE finding_id = $1`, findingID)
	f, err := scanAuditFinding(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("audit finding %s not found", findingID)
		}
		return nil, fmt.Errorf("failed to query audit finding: %w", err)
	}
	return f, nil
}

func (p *PostgresDB) UpdateAuditFinding(ctx context.Context, f *AuditFinding) error {
	evidence, err := json.Marshal(f.Evidence)
	if err != nil {
		return fmt.Errorf("failed to marshal finding evidence: %w", err)
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE audit_findings SET control_id = $2, title = $3, description = $4, severity = $5, category = $6, evidence = $7, root_cause = $8, recommendation = $9, remediation_plan = $10, status = $11, due_date = $12, resolved_at = $13, updated_at = $14
		WHERE finding_id = $1
	`, f.ID, f.ControlID, f.Title, f.Description, f.Severity, f.Category, evidence, f.RootCause, f.Recommendation, f.RemediationPlan, f.Status, nullableTime(f.DueDate), nullableTime(f.ResolvedAt), nullableTime(f.UpdatedAt))
	if err != nil {
		return fmt.Errorf("failed to update audit finding: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("audit finding %s not found", f.ID)
	}
	return nil
}

func (p *PostgresDB) ListAuditFindingsByAudit(ctx context.Context, auditID string) ([]AuditFinding, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings WHERE audit_id = $1 ORDER BY created_at`, auditID)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit findings: %w", err)
	}
	defer rows.Close()
	var findings []AuditFinding
	for rows.Next() {
		f, err := scanAuditFinding(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan audit finding row: %w", err)
		}
		findings = append(findings, *f)
	}
	return findings, rows.Err()
}

func (p *PostgresDB) ListOpenAuditFindings(ctx context.Context) ([]AuditFinding, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings WHERE status = 'open' ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query open audit findings: %w", err)
	}
	defer rows.Close()
	var findings []AuditFinding
	for rows.Next() {
		f, err := scanAuditFinding(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan audit finding row: %w", err)
		}
		findings = append(findings, *f)
	}
	return findings, rows.Err()
}

// -- Audit checklists (audit_checklists table) --

func (p *PostgresDB) CreateAuditChecklist(ctx context.Context, c *AuditChecklist) error {
	testingSteps, err := json.Marshal(c.TestingSteps)
	if err != nil {
		return fmt.Errorf("failed to marshal checklist testing steps: %w", err)
	}
	evidence, err := json.Marshal(c.Evidence)
	if err != nil {
		return fmt.Errorf("failed to marshal checklist evidence: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO audit_checklists (checklist_id, audit_id, control_id, control_name, test_method, testing_steps, evidence, status, result, notes, tested_at, tested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, c.ID, c.AuditID, c.ControlID, c.ControlName, c.TestMethod, testingSteps, evidence, c.Status, c.Result, c.Notes, nullableTime(c.TestedAt), c.TestedBy)
	if err != nil {
		return fmt.Errorf("failed to insert audit checklist: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetAuditChecklist(ctx context.Context, checklistID string) (*AuditChecklist, error) {
	var c AuditChecklist
	var testingStepsRaw, evidenceRaw []byte
	var controlID, controlName, testMethod, result, notes, testedBy sql.NullString
	var testedAt sql.NullTime
	row := p.db.QueryRowContext(ctx, `
		SELECT checklist_id, audit_id, control_id, control_name, test_method, testing_steps, evidence, status, result, notes, tested_at, tested_by
		FROM audit_checklists WHERE checklist_id = $1
	`, checklistID)
	if err := row.Scan(&c.ID, &c.AuditID, &controlID, &controlName, &testMethod, &testingStepsRaw, &evidenceRaw, &c.Status, &result, &notes, &testedAt, &testedBy); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("audit checklist %s not found", checklistID)
		}
		return nil, fmt.Errorf("failed to query audit checklist: %w", err)
	}
	c.ControlID, c.ControlName, c.TestMethod, c.Result, c.Notes, c.TestedBy = controlID.String, controlName.String, testMethod.String, result.String, notes.String, testedBy.String
	c.TestedAt = testedAt.Time
	if len(testingStepsRaw) > 0 {
		_ = json.Unmarshal(testingStepsRaw, &c.TestingSteps)
	}
	if len(evidenceRaw) > 0 {
		_ = json.Unmarshal(evidenceRaw, &c.Evidence)
	}
	return &c, nil
}

func (p *PostgresDB) UpdateAuditChecklist(ctx context.Context, c *AuditChecklist) error {
	testingSteps, err := json.Marshal(c.TestingSteps)
	if err != nil {
		return fmt.Errorf("failed to marshal checklist testing steps: %w", err)
	}
	evidence, err := json.Marshal(c.Evidence)
	if err != nil {
		return fmt.Errorf("failed to marshal checklist evidence: %w", err)
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE audit_checklists SET control_id = $2, control_name = $3, test_method = $4, testing_steps = $5, evidence = $6, status = $7, result = $8, notes = $9, tested_at = $10, tested_by = $11
		WHERE checklist_id = $1
	`, c.ID, c.ControlID, c.ControlName, c.TestMethod, testingSteps, evidence, c.Status, c.Result, c.Notes, nullableTime(c.TestedAt), c.TestedBy)
	if err != nil {
		return fmt.Errorf("failed to update audit checklist: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("audit checklist %s not found", c.ID)
	}
	return nil
}
