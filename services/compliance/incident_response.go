package main

import (
	"context"
	"fmt"
	"time"
)

// IncidentSeverity represents the severity of a security incident
type IncidentSeverity string

const (
	IncidentSeverityCritical IncidentSeverity = "critical"
	IncidentSeverityHigh     IncidentSeverity = "high"
	IncidentSeverityMedium   IncidentSeverity = "medium"
	IncidentSeverityLow      IncidentSeverity = "low"
)

// IncidentStatus represents the status of an incident
type IncidentStatus string

const (
	IncidentStatusReported      IncidentStatus = "reported"
	IncidentStatusAcknowledged  IncidentStatus = "acknowledged"
	IncidentStatusInvestigating IncidentStatus = "investigating"
	IncidentStatusContained     IncidentStatus = "contained"
	IncidentStatusResolved      IncidentStatus = "resolved"
	IncidentStatusClosed        IncidentStatus = "closed"
)

// IncidentType represents the type of incident
type IncidentType string

const (
	IncidentTypeUnauthorizedAccess IncidentType = "unauthorized_access"
	IncidentTypeDataBreach         IncidentType = "data_breach"
	IncidentTypeMalware            IncidentType = "malware"
	IncidentTypeDDoS               IncidentType = "ddos"
	IncidentTypeSystemFailure      IncidentType = "system_failure"
	IncidentTypeConfigError        IncidentType = "configuration_error"
	IncidentTypeThirdPartyBreach   IncidentType = "third_party_breach"
	IncidentTypeFalsePositive      IncidentType = "false_positive"
)

// SecurityIncident represents a security incident
type SecurityIncident struct {
	ID                    string                `json:"id"`
	Title                 string                `json:"title"`
	Description           string                `json:"description"`
	Type                  IncidentType          `json:"type"`
	Severity              IncidentSeverity      `json:"severity"`
	Status                IncidentStatus        `json:"status"`
	ReportedAt            time.Time             `json:"reported_at"`
	DiscoveredAt          time.Time             `json:"discovered_at"`
	AcknowledgedAt        time.Time             `json:"acknowledged_at,omitempty"`
	ContainedAt           time.Time             `json:"contained_at,omitempty"`
	ResolvedAt            time.Time             `json:"resolved_at,omitempty"`
	ClosedAt              time.Time             `json:"closed_at,omitempty"`
	ReportedBy            string                `json:"reported_by"`
	AssignedTo            string                `json:"assigned_to"`
	AffectedSystems       []string              `json:"affected_systems"`
	AffectedCustomers     []string              `json:"affected_customers"`
	ImpactedDataTypes     []string              `json:"impacted_data_types"`
	DataExposureCount     int64                 `json:"data_exposure_count,omitempty"`
	RootCause             string                `json:"root_cause"`
	RemediationSteps      []string              `json:"remediation_steps"`
	MTTR                  time.Duration         `json:"mttr"` // Mean Time To Recovery
	MTTD                  time.Duration         `json:"mttd"` // Mean Time To Detect
	Timeline              []IncidentTimeline    `json:"timeline"`
	Evidence              []string              `json:"evidence"`
	NotificationsRequired bool                  `json:"notifications_required"`
	NotificationsPending  []PendingNotification `json:"notifications_pending"`
	PostIncidentReview    *PostIncidentReview   `json:"post_incident_review,omitempty"`
}

// IncidentTimeline represents an event in an incident's timeline
type IncidentTimeline struct {
	Timestamp time.Time `json:"timestamp"`
	Event     string    `json:"event"`
	UpdatedBy string    `json:"updated_by"`
	Details   string    `json:"details"`
}

// PendingNotification represents a pending notification about an incident
type PendingNotification struct {
	ID               string    `json:"id"`
	Recipient        string    `json:"recipient"`         // Email or contact info
	RecipientType    string    `json:"recipient_type"`    // customer, regulator, law_enforcement
	NotificationType string    `json:"notification_type"` // GDPR, breach_notification, etc.
	ScheduledAt      time.Time `json:"scheduled_at"`
	SentAt           time.Time `json:"sent_at,omitempty"`
	Status           string    `json:"status"` // pending, sent, failed
}

// PostIncidentReview represents a post-incident review
type PostIncidentReview struct {
	ConductedAt       time.Time `json:"conducted_at"`
	ConductedBy       []string  `json:"conducted_by"`
	RootCauseAnalysis string    `json:"root_cause_analysis"`
	LessonsLearned    []string  `json:"lessons_learned"`
	ImprovementItems  []string  `json:"improvement_items"`
	FollowUpRequired  bool      `json:"follow_up_required"`
}

// IncidentResponsePlan defines an IR plan
type IncidentResponsePlan struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	EstimatedRTO     time.Duration `json:"estimated_rto"`
	EstimatedRPO     time.Duration `json:"estimated_rpo"`
	EscalationPath   []string      `json:"escalation_path"`
	NotificationList []string      `json:"notification_list"`
	RegulatoryReqs   []string      `json:"regulatory_requirements"`
	IncludedSystems  []string      `json:"included_systems"`
	LastTestedAt     time.Time     `json:"last_tested_at"`
	TestFrequency    time.Duration `json:"test_frequency"`
}

// IncidentManager manages security incidents. Backed by PostgreSQL
// (security_incidents / incident_response_plans, migration 010) rather than
// in-memory maps -- see AuditManager's doc comment in audit.go for why: this
// is SOC 2/breach-notification evidence, and it has to survive a process
// restart and be visible to every instance of the service, not just the one
// that happened to handle the report.
type IncidentManager struct {
	db *PostgresDB
}

// NewIncidentManager creates a new incident manager
func NewIncidentManager(db *PostgresDB) *IncidentManager {
	return &IncidentManager{db: db}
}

// ReportIncident creates a new security incident report
func (i *IncidentManager) ReportIncident(ctx context.Context, incident *SecurityIncident) (*SecurityIncident, error) {
	if incident.ID == "" {
		incident.ID = fmt.Sprintf("incident-%d", time.Now().UnixNano())
	}

	incident.Status = IncidentStatusReported
	incident.ReportedAt = time.Now()

	// Add initial timeline entry
	incident.Timeline = append(incident.Timeline, IncidentTimeline{
		Timestamp: incident.ReportedAt,
		Event:     "Incident reported",
		Details:   "Initial incident report received",
	})

	if err := i.db.CreateSecurityIncident(ctx, incident); err != nil {
		return nil, fmt.Errorf("failed to report incident: %w", err)
	}
	return incident, nil
}

// AcknowledgeIncident acknowledges receipt of an incident
func (i *IncidentManager) AcknowledgeIncident(ctx context.Context, incidentID string, acknowledgedBy string) error {
	incident, err := i.db.GetSecurityIncident(ctx, incidentID)
	if err != nil {
		return fmt.Errorf("incident not found: %s", incidentID)
	}

	incident.Status = IncidentStatusAcknowledged
	incident.AcknowledgedAt = time.Now()
	incident.Timeline = append(incident.Timeline, IncidentTimeline{
		Timestamp: incident.AcknowledgedAt,
		Event:     "Incident acknowledged",
		UpdatedBy: acknowledgedBy,
	})

	return i.db.UpdateSecurityIncident(ctx, incident)
}

// UpdateIncidentStatus updates the status of an incident
func (i *IncidentManager) UpdateIncidentStatus(ctx context.Context, incidentID string, newStatus IncidentStatus, details string, updatedBy string) error {
	incident, err := i.db.GetSecurityIncident(ctx, incidentID)
	if err != nil {
		return fmt.Errorf("incident not found: %s", incidentID)
	}

	incident.Status = newStatus

	switch newStatus {
	case IncidentStatusInvestigating:
		incident.Timeline = append(incident.Timeline, IncidentTimeline{
			Timestamp: time.Now(),
			Event:     "Investigation started",
			UpdatedBy: updatedBy,
			Details:   details,
		})
	case IncidentStatusContained:
		incident.ContainedAt = time.Now()
		incident.Timeline = append(incident.Timeline, IncidentTimeline{
			Timestamp: incident.ContainedAt,
			Event:     "Incident contained",
			UpdatedBy: updatedBy,
			Details:   details,
		})
	case IncidentStatusResolved:
		incident.ResolvedAt = time.Now()
		if !incident.DiscoveredAt.IsZero() {
			incident.MTTR = incident.ResolvedAt.Sub(incident.DiscoveredAt)
		}
		incident.Timeline = append(incident.Timeline, IncidentTimeline{
			Timestamp: incident.ResolvedAt,
			Event:     "Incident resolved",
			UpdatedBy: updatedBy,
			Details:   details,
		})
	case IncidentStatusClosed:
		incident.ClosedAt = time.Now()
		incident.Timeline = append(incident.Timeline, IncidentTimeline{
			Timestamp: incident.ClosedAt,
			Event:     "Incident closed",
			UpdatedBy: updatedBy,
			Details:   details,
		})
	}

	return i.db.UpdateSecurityIncident(ctx, incident)
}

// GetIncident retrieves an incident
func (i *IncidentManager) GetIncident(ctx context.Context, incidentID string) (*SecurityIncident, error) {
	return i.db.GetSecurityIncident(ctx, incidentID)
}

// ListIncidents lists incidents by status
func (i *IncidentManager) ListIncidents(ctx context.Context, status IncidentStatus) ([]SecurityIncident, error) {
	return i.db.ListSecurityIncidentsByStatus(ctx, string(status))
}

// CalculateMTTD calculates Mean Time To Detect across all recorded incidents
func (i *IncidentManager) CalculateMTTD(ctx context.Context) time.Duration {
	incidents, err := i.db.ListSecurityIncidents(ctx)
	if err != nil {
		return 0
	}

	var totalDetectionTime time.Duration
	var count int
	for _, incident := range incidents {
		if !incident.DiscoveredAt.IsZero() && !incident.ReportedAt.IsZero() {
			totalDetectionTime += incident.ReportedAt.Sub(incident.DiscoveredAt)
			count++
		}
	}

	if count == 0 {
		return 0
	}
	return totalDetectionTime / time.Duration(count)
}

// CalculateMTTR calculates Mean Time To Recovery across all recorded incidents
func (i *IncidentManager) CalculateMTTR(ctx context.Context) time.Duration {
	incidents, err := i.db.ListSecurityIncidents(ctx)
	if err != nil {
		return 0
	}

	var totalRecoveryTime time.Duration
	var count int
	for _, incident := range incidents {
		if incident.MTTR > 0 {
			totalRecoveryTime += incident.MTTR
			count++
		}
	}

	if count == 0 {
		return 0
	}
	return totalRecoveryTime / time.Duration(count)
}

// NotifyStakeholders sends notifications about the incident
func (i *IncidentManager) NotifyStakeholders(ctx context.Context, incidentID string, recipients []string) error {
	incident, err := i.GetIncident(ctx, incidentID)
	if err != nil {
		return err
	}

	for _, recipient := range recipients {
		notification := PendingNotification{
			ID:          fmt.Sprintf("notif-%d", time.Now().UnixNano()),
			Recipient:   recipient,
			ScheduledAt: time.Now(),
			Status:      "pending",
		}

		if incident.Severity == IncidentSeverityCritical {
			notification.RecipientType = "regulator"
			notification.NotificationType = "GDPR_breach_notification"
		} else {
			notification.RecipientType = "customer"
			notification.NotificationType = "incident_notification"
		}

		incident.NotificationsPending = append(incident.NotificationsPending, notification)
	}

	incident.NotificationsRequired = len(recipients) > 0

	return i.db.UpdateSecurityIncident(ctx, incident)
}

// ConductPostIncidentReview conducts a post-incident review
func (i *IncidentManager) ConductPostIncidentReview(ctx context.Context, incidentID string, conductedBy []string) error {
	incident, err := i.GetIncident(ctx, incidentID)
	if err != nil {
		return err
	}

	incident.PostIncidentReview = &PostIncidentReview{
		ConductedAt: time.Now(),
		ConductedBy: conductedBy,
	}

	incident.Timeline = append(incident.Timeline, IncidentTimeline{
		Timestamp: time.Now(),
		Event:     "Post-incident review conducted",
		UpdatedBy: conductedBy[0],
	})

	return i.db.UpdateSecurityIncident(ctx, incident)
}

// CreateIncidentResponsePlan creates a response plan
func (i *IncidentManager) CreateIncidentResponsePlan(ctx context.Context, plan *IncidentResponsePlan) error {
	if plan.ID == "" {
		plan.ID = fmt.Sprintf("irplan-%d", time.Now().UnixNano())
	}

	return i.db.CreateIncidentResponsePlan(ctx, plan)
}

// GetIncidentResponsePlan retrieves a response plan
func (i *IncidentManager) GetIncidentResponsePlan(ctx context.Context, planID string) (*IncidentResponsePlan, error) {
	return i.db.GetIncidentResponsePlan(ctx, planID)
}

// IncidentMetrics provides incident metrics
type IncidentMetrics struct {
	TotalIncidents          int           `json:"total_incidents"`
	CriticalIncidents       int           `json:"critical_incidents"`
	AverageMTTD             time.Duration `json:"average_mttd"`
	AverageMTTR             time.Duration `json:"average_mttr"`
	IncidentsInThreshold    int           `json:"incidents_in_threshold"`
	IncidentsOutOfThreshold int           `json:"incidents_out_of_threshold"`
}

// GetIncidentMetrics returns incident metrics
func (i *IncidentManager) GetIncidentMetrics(ctx context.Context, mttdTarget, mttrTarget time.Duration) *IncidentMetrics {
	incidents, err := i.db.ListSecurityIncidents(ctx)
	if err != nil {
		return &IncidentMetrics{}
	}

	metrics := &IncidentMetrics{
		TotalIncidents: len(incidents),
		AverageMTTD:    i.CalculateMTTD(ctx),
		AverageMTTR:    i.CalculateMTTR(ctx),
	}

	for _, incident := range incidents {
		if incident.Severity == IncidentSeverityCritical {
			metrics.CriticalIncidents++
		}

		if incident.MTTD <= mttdTarget && incident.MTTR <= mttrTarget {
			metrics.IncidentsInThreshold++
		} else {
			metrics.IncidentsOutOfThreshold++
		}
	}

	return metrics
}
