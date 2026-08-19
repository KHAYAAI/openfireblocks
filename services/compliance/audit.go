package main

import (
	"context"
	"fmt"
	"time"
)

// AuditScope defines the scope of an audit
type AuditScope string

const (
	AuditScopeInternal  AuditScope = "internal"
	AuditScopeExternal  AuditScope = "external"
	AuditScopeAssurance AuditScope = "assurance"
)

// AuditType defines the type of audit
type AuditType string

const (
	AuditTypeSOC2          AuditType = "soc2"
	AuditTypeISO27001      AuditType = "iso27001"
	AuditTypeGDPR          AuditType = "gdpr"
	AuditTypePCI           AuditType = "pci_dss"
	AuditTypeComprehensive AuditType = "comprehensive"
)

// AuditStatus represents the status of an audit
type AuditStatus string

const (
	AuditStatusPlanned     AuditStatus = "planned"
	AuditStatusInProgress  AuditStatus = "in_progress"
	AuditStatusCompleted   AuditStatus = "completed"
	AuditStatusRemediation AuditStatus = "remediation"
	AuditStatusCertified   AuditStatus = "certified"
)

// AuditFinding represents a finding from an audit
type AuditFinding struct {
	ID              string    `json:"id"`
	AuditID         string    `json:"audit_id"`
	ControlID       string    `json:"control_id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Severity        string    `json:"severity"` // critical, high, medium, low
	Category        string    `json:"category"`
	Evidence        []string  `json:"evidence"`
	RootCause       string    `json:"root_cause"`
	Recommendation  string    `json:"recommendation"`
	RemediationPlan string    `json:"remediation_plan"`
	Status          string    `json:"status"` // open, acknowledged, remediation_in_progress, remediated, waived
	DueDate         time.Time `json:"due_date"`
	ResolvedAt      time.Time `json:"resolved_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SecurityAudit represents a security audit engagement
type SecurityAudit struct {
	ID              string         `json:"id"`
	Type            AuditType      `json:"type"`
	Scope           AuditScope     `json:"scope"`
	Status          AuditStatus    `json:"status"`
	ScheduledStart  time.Time      `json:"scheduled_start"`
	ScheduledEnd    time.Time      `json:"scheduled_end"`
	ActualStart     time.Time      `json:"actual_start"`
	ActualEnd       time.Time      `json:"actual_end,omitempty"`
	Auditor         string         `json:"auditor"` // Internal team or external firm
	Findings        []AuditFinding `json:"findings"`
	CriticalCount   int            `json:"critical_count"`
	HighCount       int            `json:"high_count"`
	MediumCount     int            `json:"medium_count"`
	LowCount        int            `json:"low_count"`
	Score           float64        `json:"score"` // 0-100
	ReportPath      string         `json:"report_path"`
	CertificatePath string         `json:"certificate_path,omitempty"`
	Notes           string         `json:"notes"`
}

// AuditChecklist defines a checklist for audit execution
type AuditChecklist struct {
	ID           string    `json:"id"`
	AuditID      string    `json:"audit_id"`
	ControlID    string    `json:"control_id"`
	ControlName  string    `json:"control_name"`
	TestMethod   string    `json:"test_method"` // walkthrough, testing, observation
	TestingSteps []string  `json:"testing_steps"`
	Evidence     []string  `json:"evidence"`
	Status       string    `json:"status"` // not_started, in_progress, complete
	Result       string    `json:"result"` // pass, fail, n/a
	Notes        string    `json:"notes"`
	TestedAt     time.Time `json:"tested_at"`
	TestedBy     string    `json:"tested_by"`
}

// AuditManager manages security audits
type AuditManager struct {
	audits     map[string]*SecurityAudit
	findings   map[string]*AuditFinding
	checklists map[string]*AuditChecklist
}

// NewAuditManager creates a new audit manager
func NewAuditManager() *AuditManager {
	return &AuditManager{
		audits:     make(map[string]*SecurityAudit),
		findings:   make(map[string]*AuditFinding),
		checklists: make(map[string]*AuditChecklist),
	}
}

// PlanAudit schedules a security audit
func (a *AuditManager) PlanAudit(ctx context.Context, auditType AuditType, scope AuditScope, startDate, endDate time.Time) (*SecurityAudit, error) {
	audit := &SecurityAudit{
		ID:             fmt.Sprintf("audit-%d", time.Now().UnixNano()),
		Type:           auditType,
		Scope:          scope,
		Status:         AuditStatusPlanned,
		ScheduledStart: startDate,
		ScheduledEnd:   endDate,
		Findings:       []AuditFinding{},
	}

	a.audits[audit.ID] = audit
	return audit, nil
}

// StartAudit marks an audit as in progress
func (a *AuditManager) StartAudit(ctx context.Context, auditID string) (*SecurityAudit, error) {
	audit, exists := a.audits[auditID]
	if !exists {
		return nil, fmt.Errorf("audit not found: %s", auditID)
	}

	audit.Status = AuditStatusInProgress
	audit.ActualStart = time.Now()
	return audit, nil
}

// AddFinding records a finding in an audit
func (a *AuditManager) AddFinding(ctx context.Context, finding *AuditFinding) error {
	if finding.ID == "" {
		finding.ID = fmt.Sprintf("finding-%d", time.Now().UnixNano())
	}

	finding.CreatedAt = time.Now()
	finding.UpdatedAt = time.Now()
	finding.Status = "open"

	a.findings[finding.ID] = finding

	// Update audit finding counts
	if audit, exists := a.audits[finding.AuditID]; exists {
		audit.Findings = append(audit.Findings, *finding)

		switch finding.Severity {
		case "critical":
			audit.CriticalCount++
		case "high":
			audit.HighCount++
		case "medium":
			audit.MediumCount++
		case "low":
			audit.LowCount++
		}

		// Calculate audit score (100 - (critical*40 + high*20 + medium*10 + low*5))
		audit.Score = 100 - float64(audit.CriticalCount*40+audit.HighCount*20+audit.MediumCount*10+audit.LowCount*5)
		if audit.Score < 0 {
			audit.Score = 0
		}
	}

	return nil
}

// GetFinding retrieves a specific finding
func (a *AuditManager) GetFinding(ctx context.Context, findingID string) (*AuditFinding, error) {
	finding, exists := a.findings[findingID]
	if !exists {
		return nil, fmt.Errorf("finding not found: %s", findingID)
	}
	return finding, nil
}

// UpdateFindingStatus updates the status of a finding
func (a *AuditManager) UpdateFindingStatus(ctx context.Context, findingID, status string, notes string) error {
	finding, exists := a.findings[findingID]
	if !exists {
		return fmt.Errorf("finding not found: %s", findingID)
	}

	finding.Status = status
	finding.UpdatedAt = time.Now()

	if status == "remediated" {
		finding.ResolvedAt = time.Now()
	}

	return nil
}

// CreateAuditChecklist creates a checklist for a control
func (a *AuditManager) CreateAuditChecklist(ctx context.Context, auditID, controlID string, controlName string) (*AuditChecklist, error) {
	checklist := &AuditChecklist{
		ID:          fmt.Sprintf("checklist-%d", time.Now().UnixNano()),
		AuditID:     auditID,
		ControlID:   controlID,
		ControlName: controlName,
		Status:      "not_started",
	}

	a.checklists[checklist.ID] = checklist
	return checklist, nil
}

// CompleteChecklistItem marks a checklist item as complete
func (a *AuditManager) CompleteChecklistItem(ctx context.Context, checklistID, result, notes string) error {
	checklist, exists := a.checklists[checklistID]
	if !exists {
		return fmt.Errorf("checklist not found: %s", checklistID)
	}

	checklist.Status = "complete"
	checklist.Result = result
	checklist.Notes = notes
	checklist.TestedAt = time.Now()

	return nil
}

// CompleteAudit marks an audit as complete and generates report
func (a *AuditManager) CompleteAudit(ctx context.Context, auditID string) (*SecurityAudit, error) {
	audit, exists := a.audits[auditID]
	if !exists {
		return nil, fmt.Errorf("audit not found: %s", auditID)
	}

	audit.Status = AuditStatusCompleted
	audit.ActualEnd = time.Now()

	// Determine if remediation is needed
	if audit.CriticalCount > 0 || audit.HighCount > 0 {
		audit.Status = AuditStatusRemediation
	} else if audit.Score >= 90 {
		audit.Status = AuditStatusCertified
	}

	return audit, nil
}

// GetAudit retrieves an audit
func (a *AuditManager) GetAudit(ctx context.Context, auditID string) (*SecurityAudit, error) {
	audit, exists := a.audits[auditID]
	if !exists {
		return nil, fmt.Errorf("audit not found: %s", auditID)
	}
	return audit, nil
}

// ListOpenFindings lists all open findings across audits
func (a *AuditManager) ListOpenFindings(ctx context.Context) ([]AuditFinding, error) {
	var openFindings []AuditFinding
	for _, finding := range a.findings {
		if finding.Status != "remediated" && finding.Status != "waived" {
			openFindings = append(openFindings, *finding)
		}
	}
	return openFindings, nil
}

// GetAuditsByStatus retrieves audits by status
func (a *AuditManager) GetAuditsByStatus(ctx context.Context, status AuditStatus) ([]SecurityAudit, error) {
	var result []SecurityAudit
	for _, audit := range a.audits {
		if audit.Status == status {
			result = append(result, *audit)
		}
	}
	return result, nil
}

// GenerateAuditReport generates a report for an audit
func (a *AuditManager) GenerateAuditReport(ctx context.Context, auditID string) (string, error) {
	audit, err := a.GetAudit(ctx, auditID)
	if err != nil {
		return "", err
	}

	report := fmt.Sprintf(`
=== SECURITY AUDIT REPORT ===

Audit ID: %s
Type: %v
Scope: %v
Status: %v

Audit Period: %s to %s
Auditor: %s

FINDINGS SUMMARY
================
Critical: %d
High: %d
Medium: %d
Low: %d

Audit Score: %.1f/100

FINDINGS DETAILS
================
`,
		audit.ID,
		audit.Type,
		audit.Scope,
		audit.Status,
		audit.ScheduledStart.Format("2006-01-02"),
		audit.ScheduledEnd.Format("2006-01-02"),
		audit.Auditor,
		audit.CriticalCount,
		audit.HighCount,
		audit.MediumCount,
		audit.LowCount,
		audit.Score,
	)

	for i, finding := range audit.Findings {
		report += fmt.Sprintf(`
Finding %d: %s
Control: %s
Severity: %s
Description: %s
Recommendation: %s
Status: %s

`,
			i+1,
			finding.Title,
			finding.ControlID,
			finding.Severity,
			finding.Description,
			finding.Recommendation,
			finding.Status,
		)
	}

	return report, nil
}

// StandardAuditChecklists provides standard audit checklists for common frameworks
func StandardAuditChecklists(auditType AuditType) map[string]string {
	switch auditType {
	case AuditTypeSOC2:
		return map[string]string{
			"CC1": "Organization demonstrates commitment to competence",
			"CC2": "Board exercises oversight of information security",
			"CC3": "Management establishes structure, authority, responsibility",
			"CC4": "Organization commits to competence",
			"CC5": "Organization holds individuals accountable",
			"CC6": "Organization specifies objectives for relevant parties",
			"CC7": "Organization obtains or generates information",
			"CC8": "Organization obtains relevant information about external parties",
			"CC9": "Organization removes, resolves, mitigates, or accepts risks",
			"A1":  "System monitoring and alerting",
			"A2":  "System capacity planning",
			"C1":  "Data encryption in transit",
			"C2":  "Data encryption at rest",
			"C3":  "Access controls",
			"C4":  "Data segregation",
			"S1":  "Logical access controls",
			"S2":  "Prior to issuing system credentials",
			"S3":  "Credentials are protected",
			"S4":  "System access termination",
			"S5":  "Invalid access attempts",
		}
	case AuditTypeISO27001:
		return map[string]string{
			"A.5.1":  "Information security policies",
			"A.5.2":  "Segregation of duties",
			"A.5.3":  "Acceptance of risk",
			"A.6.1":  "Personnel screening",
			"A.6.2":  "Personnel awareness",
			"A.6.3":  "Responsibilities",
			"A.7.1":  "Asset management",
			"A.7.2":  "Information classification",
			"A.7.3":  "Media handling",
			"A.8.1":  "Access control policy",
			"A.8.2":  "User access management",
			"A.8.3":  "Access information",
			"A.9.1":  "Cryptography policy",
			"A.9.2":  "Key management",
			"A.10.1": "Physical perimeter",
			"A.10.2": "Physical entry",
			"A.11.1": "Change management",
			"A.11.2": "Capacity planning",
			"A.11.3": "Logging",
			"A.12.1": "Network security",
			"A.12.2": "Network access control",
			"A.13.1": "Information security requirements",
			"A.14.1": "Supplier requirements",
			"A.15.1": "Incident procedures",
			"A.16.1": "Business continuity",
			"A.17.1": "Compliance",
		}
	default:
		return make(map[string]string)
	}
}
