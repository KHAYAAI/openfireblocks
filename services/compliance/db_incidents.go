package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// -- Security incidents (security_incidents table, migration 010) --

func (p *PostgresDB) CreateSecurityIncident(ctx context.Context, inc *SecurityIncident) error {
	affectedSystems, err := json.Marshal(inc.AffectedSystems)
	if err != nil {
		return fmt.Errorf("failed to marshal affected systems: %w", err)
	}
	affectedCustomers, err := json.Marshal(inc.AffectedCustomers)
	if err != nil {
		return fmt.Errorf("failed to marshal affected customers: %w", err)
	}
	impactedDataTypes, err := json.Marshal(inc.ImpactedDataTypes)
	if err != nil {
		return fmt.Errorf("failed to marshal impacted data types: %w", err)
	}
	remediationSteps, err := json.Marshal(inc.RemediationSteps)
	if err != nil {
		return fmt.Errorf("failed to marshal remediation steps: %w", err)
	}
	timeline, err := json.Marshal(inc.Timeline)
	if err != nil {
		return fmt.Errorf("failed to marshal timeline: %w", err)
	}
	evidence, err := json.Marshal(inc.Evidence)
	if err != nil {
		return fmt.Errorf("failed to marshal evidence: %w", err)
	}
	notificationsPending, err := json.Marshal(inc.NotificationsPending)
	if err != nil {
		return fmt.Errorf("failed to marshal notifications pending: %w", err)
	}
	postIncidentReview, err := marshalPostIncidentReview(inc.PostIncidentReview)
	if err != nil {
		return fmt.Errorf("failed to marshal post-incident review: %w", err)
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO security_incidents (
			incident_id, title, description, type, severity, status,
			reported_at, discovered_at, acknowledged_at, contained_at, resolved_at, closed_at,
			reported_by, assigned_to, affected_systems, affected_customers, impacted_data_types,
			data_exposure_count, root_cause, remediation_steps, mttr_ns, mttd_ns, timeline,
			evidence, notifications_required, notifications_pending, post_incident_review
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
			$18, $19, $20, $21, $22, $23, $24, $25, $26, $27
		)
	`, inc.ID, inc.Title, inc.Description, inc.Type, inc.Severity, inc.Status,
		nullableTime(inc.ReportedAt), nullableTime(inc.DiscoveredAt), nullableTime(inc.AcknowledgedAt), nullableTime(inc.ContainedAt), nullableTime(inc.ResolvedAt), nullableTime(inc.ClosedAt),
		inc.ReportedBy, inc.AssignedTo, affectedSystems, affectedCustomers, impactedDataTypes,
		nullableInt64(inc.DataExposureCount), inc.RootCause, remediationSteps, int64(inc.MTTR), int64(inc.MTTD), timeline,
		evidence, inc.NotificationsRequired, notificationsPending, postIncidentReview,
	)
	if err != nil {
		return fmt.Errorf("failed to insert security incident: %w", err)
	}
	return nil
}

const securityIncidentColumns = `incident_id, title, description, type, severity, status,
	reported_at, discovered_at, acknowledged_at, contained_at, resolved_at, closed_at,
	reported_by, assigned_to, affected_systems, affected_customers, impacted_data_types,
	data_exposure_count, root_cause, remediation_steps, mttr_ns, mttd_ns, timeline,
	evidence, notifications_required, notifications_pending, post_incident_review`

func scanSecurityIncident(row interface {
	Scan(dest ...interface{}) error
}) (*SecurityIncident, error) {
	var inc SecurityIncident
	var reportedAt, discoveredAt, acknowledgedAt, containedAt, resolvedAt, closedAt sql.NullTime
	var reportedBy, assignedTo, rootCause sql.NullString
	var affectedSystemsRaw, affectedCustomersRaw, impactedDataTypesRaw, remediationStepsRaw, timelineRaw, evidenceRaw, notificationsPendingRaw, postIncidentReviewRaw []byte
	var dataExposureCount sql.NullInt64
	var mttrNs, mttdNs int64

	if err := row.Scan(
		&inc.ID, &inc.Title, &inc.Description, &inc.Type, &inc.Severity, &inc.Status,
		&reportedAt, &discoveredAt, &acknowledgedAt, &containedAt, &resolvedAt, &closedAt,
		&reportedBy, &assignedTo, &affectedSystemsRaw, &affectedCustomersRaw, &impactedDataTypesRaw,
		&dataExposureCount, &rootCause, &remediationStepsRaw, &mttrNs, &mttdNs, &timelineRaw,
		&evidenceRaw, &inc.NotificationsRequired, &notificationsPendingRaw, &postIncidentReviewRaw,
	); err != nil {
		return nil, err
	}

	inc.ReportedAt, inc.DiscoveredAt, inc.AcknowledgedAt, inc.ContainedAt, inc.ResolvedAt, inc.ClosedAt =
		reportedAt.Time, discoveredAt.Time, acknowledgedAt.Time, containedAt.Time, resolvedAt.Time, closedAt.Time
	inc.ReportedBy, inc.AssignedTo, inc.RootCause = reportedBy.String, assignedTo.String, rootCause.String
	inc.DataExposureCount = dataExposureCount.Int64
	inc.MTTR = timeDurationFromNs(mttrNs)
	inc.MTTD = timeDurationFromNs(mttdNs)

	if len(affectedSystemsRaw) > 0 {
		_ = json.Unmarshal(affectedSystemsRaw, &inc.AffectedSystems)
	}
	if len(affectedCustomersRaw) > 0 {
		_ = json.Unmarshal(affectedCustomersRaw, &inc.AffectedCustomers)
	}
	if len(impactedDataTypesRaw) > 0 {
		_ = json.Unmarshal(impactedDataTypesRaw, &inc.ImpactedDataTypes)
	}
	if len(remediationStepsRaw) > 0 {
		_ = json.Unmarshal(remediationStepsRaw, &inc.RemediationSteps)
	}
	if len(timelineRaw) > 0 {
		_ = json.Unmarshal(timelineRaw, &inc.Timeline)
	}
	if len(evidenceRaw) > 0 {
		_ = json.Unmarshal(evidenceRaw, &inc.Evidence)
	}
	if len(notificationsPendingRaw) > 0 {
		_ = json.Unmarshal(notificationsPendingRaw, &inc.NotificationsPending)
	}
	if len(postIncidentReviewRaw) > 0 {
		var review PostIncidentReview
		if err := json.Unmarshal(postIncidentReviewRaw, &review); err == nil {
			inc.PostIncidentReview = &review
		}
	}

	return &inc, nil
}

func (p *PostgresDB) GetSecurityIncident(ctx context.Context, incidentID string) (*SecurityIncident, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+securityIncidentColumns+` FROM security_incidents WHERE incident_id = $1`, incidentID)
	inc, err := scanSecurityIncident(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("security incident %s not found", incidentID)
		}
		return nil, fmt.Errorf("failed to query security incident: %w", err)
	}
	return inc, nil
}

func (p *PostgresDB) UpdateSecurityIncident(ctx context.Context, inc *SecurityIncident) error {
	affectedSystems, err := json.Marshal(inc.AffectedSystems)
	if err != nil {
		return fmt.Errorf("failed to marshal affected systems: %w", err)
	}
	affectedCustomers, err := json.Marshal(inc.AffectedCustomers)
	if err != nil {
		return fmt.Errorf("failed to marshal affected customers: %w", err)
	}
	impactedDataTypes, err := json.Marshal(inc.ImpactedDataTypes)
	if err != nil {
		return fmt.Errorf("failed to marshal impacted data types: %w", err)
	}
	remediationSteps, err := json.Marshal(inc.RemediationSteps)
	if err != nil {
		return fmt.Errorf("failed to marshal remediation steps: %w", err)
	}
	timeline, err := json.Marshal(inc.Timeline)
	if err != nil {
		return fmt.Errorf("failed to marshal timeline: %w", err)
	}
	evidence, err := json.Marshal(inc.Evidence)
	if err != nil {
		return fmt.Errorf("failed to marshal evidence: %w", err)
	}
	notificationsPending, err := json.Marshal(inc.NotificationsPending)
	if err != nil {
		return fmt.Errorf("failed to marshal notifications pending: %w", err)
	}
	postIncidentReview, err := marshalPostIncidentReview(inc.PostIncidentReview)
	if err != nil {
		return fmt.Errorf("failed to marshal post-incident review: %w", err)
	}

	res, err := p.db.ExecContext(ctx, `
		UPDATE security_incidents SET
			title = $2, description = $3, type = $4, severity = $5, status = $6,
			reported_at = $7, discovered_at = $8, acknowledged_at = $9, contained_at = $10, resolved_at = $11, closed_at = $12,
			reported_by = $13, assigned_to = $14, affected_systems = $15, affected_customers = $16, impacted_data_types = $17,
			data_exposure_count = $18, root_cause = $19, remediation_steps = $20, mttr_ns = $21, mttd_ns = $22, timeline = $23,
			evidence = $24, notifications_required = $25, notifications_pending = $26, post_incident_review = $27
		WHERE incident_id = $1
	`, inc.ID, inc.Title, inc.Description, inc.Type, inc.Severity, inc.Status,
		nullableTime(inc.ReportedAt), nullableTime(inc.DiscoveredAt), nullableTime(inc.AcknowledgedAt), nullableTime(inc.ContainedAt), nullableTime(inc.ResolvedAt), nullableTime(inc.ClosedAt),
		inc.ReportedBy, inc.AssignedTo, affectedSystems, affectedCustomers, impactedDataTypes,
		nullableInt64(inc.DataExposureCount), inc.RootCause, remediationSteps, int64(inc.MTTR), int64(inc.MTTD), timeline,
		evidence, inc.NotificationsRequired, notificationsPending, postIncidentReview,
	)
	if err != nil {
		return fmt.Errorf("failed to update security incident: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("security incident %s not found", inc.ID)
	}
	return nil
}

func (p *PostgresDB) ListSecurityIncidentsByStatus(ctx context.Context, status string) ([]SecurityIncident, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+securityIncidentColumns+` FROM security_incidents WHERE status = $1 ORDER BY reported_at DESC`, status)
	if err != nil {
		return nil, fmt.Errorf("failed to query security incidents by status: %w", err)
	}
	defer rows.Close()
	var incidents []SecurityIncident
	for rows.Next() {
		inc, err := scanSecurityIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan security incident row: %w", err)
		}
		incidents = append(incidents, *inc)
	}
	return incidents, rows.Err()
}

// ListSecurityIncidents lists every incident, for metrics aggregation
// (CalculateMTTD/CalculateMTTR/GetIncidentMetrics) and for reporting.go's
// GetIncidents, which needs a date range rather than a status filter.
func (p *PostgresDB) ListSecurityIncidents(ctx context.Context) ([]SecurityIncident, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT `+securityIncidentColumns+` FROM security_incidents ORDER BY reported_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query security incidents: %w", err)
	}
	defer rows.Close()
	var incidents []SecurityIncident
	for rows.Next() {
		inc, err := scanSecurityIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan security incident row: %w", err)
		}
		incidents = append(incidents, *inc)
	}
	return incidents, rows.Err()
}

// -- Incident response plans (incident_response_plans table) --

func (p *PostgresDB) CreateIncidentResponsePlan(ctx context.Context, plan *IncidentResponsePlan) error {
	escalationPath, err := json.Marshal(plan.EscalationPath)
	if err != nil {
		return fmt.Errorf("failed to marshal escalation path: %w", err)
	}
	notificationList, err := json.Marshal(plan.NotificationList)
	if err != nil {
		return fmt.Errorf("failed to marshal notification list: %w", err)
	}
	regulatoryReqs, err := json.Marshal(plan.RegulatoryReqs)
	if err != nil {
		return fmt.Errorf("failed to marshal regulatory requirements: %w", err)
	}
	includedSystems, err := json.Marshal(plan.IncludedSystems)
	if err != nil {
		return fmt.Errorf("failed to marshal included systems: %w", err)
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO incident_response_plans (plan_id, name, estimated_rto_ns, estimated_rpo_ns, escalation_path, notification_list, regulatory_reqs, included_systems, last_tested_at, test_frequency_ns)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, plan.ID, plan.Name, int64(plan.EstimatedRTO), int64(plan.EstimatedRPO), escalationPath, notificationList, regulatoryReqs, includedSystems, nullableTime(plan.LastTestedAt), int64(plan.TestFrequency))
	if err != nil {
		return fmt.Errorf("failed to insert incident response plan: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetIncidentResponsePlan(ctx context.Context, planID string) (*IncidentResponsePlan, error) {
	var plan IncidentResponsePlan
	var escalationPathRaw, notificationListRaw, regulatoryReqsRaw, includedSystemsRaw []byte
	var lastTestedAt sql.NullTime
	var rtoNs, rpoNs, testFreqNs int64
	row := p.db.QueryRowContext(ctx, `
		SELECT plan_id, name, estimated_rto_ns, estimated_rpo_ns, escalation_path, notification_list, regulatory_reqs, included_systems, last_tested_at, test_frequency_ns
		FROM incident_response_plans WHERE plan_id = $1
	`, planID)
	if err := row.Scan(&plan.ID, &plan.Name, &rtoNs, &rpoNs, &escalationPathRaw, &notificationListRaw, &regulatoryReqsRaw, &includedSystemsRaw, &lastTestedAt, &testFreqNs); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("incident response plan %s not found", planID)
		}
		return nil, fmt.Errorf("failed to query incident response plan: %w", err)
	}
	plan.EstimatedRTO = timeDurationFromNs(rtoNs)
	plan.EstimatedRPO = timeDurationFromNs(rpoNs)
	plan.TestFrequency = timeDurationFromNs(testFreqNs)
	plan.LastTestedAt = lastTestedAt.Time
	if len(escalationPathRaw) > 0 {
		_ = json.Unmarshal(escalationPathRaw, &plan.EscalationPath)
	}
	if len(notificationListRaw) > 0 {
		_ = json.Unmarshal(notificationListRaw, &plan.NotificationList)
	}
	if len(regulatoryReqsRaw) > 0 {
		_ = json.Unmarshal(regulatoryReqsRaw, &plan.RegulatoryReqs)
	}
	if len(includedSystemsRaw) > 0 {
		_ = json.Unmarshal(includedSystemsRaw, &plan.IncludedSystems)
	}
	return &plan, nil
}

func nullableInt64(v int64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func marshalPostIncidentReview(v *PostIncidentReview) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
