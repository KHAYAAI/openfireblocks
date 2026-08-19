package main

import (
	"context"
	"fmt"
	"time"
)

// ComplianceMetric represents a measured compliance metric
type ComplianceMetric struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"` // SOC2, ISO27001, GDPR, etc.
	Value       float64   `json:"value"`
	Unit        string    `json:"unit"`
	Threshold   float64   `json:"threshold"`
	Status      string    `json:"status"` // pass, warning, fail
	MeasuredAt  time.Time `json:"measured_at"`
	Description string    `json:"description"`
	Target      float64   `json:"target"`
}

// ComplianceAlert represents a compliance alert
type ComplianceAlert struct {
	ID                string    `json:"id"`
	MetricID          string    `json:"metric_id"`
	Severity          string    `json:"severity"` // critical, high, medium, low
	Message           string    `json:"message"`
	RecommendedAction string    `json:"recommended_action"`
	CreatedAt         time.Time `json:"created_at"`
	ResolvedAt        time.Time `json:"resolved_at,omitempty"`
	Status            string    `json:"status"` // open, acknowledged, resolved
}

// ComplianceDashboard aggregates compliance metrics and status
type ComplianceDashboard struct {
	ID              string             `json:"id"`
	Timestamp       time.Time          `json:"timestamp"`
	Metrics         []ComplianceMetric `json:"metrics"`
	Alerts          []ComplianceAlert  `json:"alerts"`
	OverallStatus   string             `json:"overall_status"`   // compliant, warning, non_compliant
	ComplianceScore float64            `json:"compliance_score"` // 0-100
	LastUpdatedAt   time.Time          `json:"last_updated_at"`
}

// ComplianceMonitor monitors compliance metrics
type ComplianceMonitor struct {
	metrics map[string]ComplianceMetric
	alerts  map[string]ComplianceAlert
}

// NewComplianceMonitor creates a new compliance monitor
func NewComplianceMonitor() *ComplianceMonitor {
	return &ComplianceMonitor{
		metrics: make(map[string]ComplianceMetric),
		alerts:  make(map[string]ComplianceAlert),
	}
}

// RecordMetric records a compliance metric
func (c *ComplianceMonitor) RecordMetric(ctx context.Context, metric ComplianceMetric) error {
	if metric.ID == "" {
		metric.ID = fmt.Sprintf("metric-%d", time.Now().UnixNano())
	}

	metric.MeasuredAt = time.Now()

	// Determine status based on threshold
	if metric.Value >= metric.Threshold {
		metric.Status = "pass"
	} else if metric.Value >= (metric.Threshold * 0.9) {
		metric.Status = "warning"
	} else {
		metric.Status = "fail"
		// Create alert for failed metric
		c.createAlert(ctx, metric)
	}

	c.metrics[metric.ID] = metric
	return nil
}

// createAlert creates a compliance alert
func (c *ComplianceMonitor) createAlert(ctx context.Context, metric ComplianceMetric) {
	alert := ComplianceAlert{
		ID:        fmt.Sprintf("alert-%d", time.Now().UnixNano()),
		MetricID:  metric.ID,
		Status:    "open",
		CreatedAt: time.Now(),
	}

	// Determine severity based on gap
	gap := metric.Threshold - metric.Value
	if gap > (metric.Threshold * 0.5) {
		alert.Severity = "critical"
	} else if gap > (metric.Threshold * 0.2) {
		alert.Severity = "high"
	} else {
		alert.Severity = "medium"
	}

	alert.Message = fmt.Sprintf("Metric %s below threshold: %.2f < %.2f %s",
		metric.Name, metric.Value, metric.Threshold, metric.Unit)

	c.alerts[alert.ID] = alert
}

// GetMetric retrieves a specific metric
func (c *ComplianceMonitor) GetMetric(ctx context.Context, metricID string) (*ComplianceMetric, error) {
	metric, exists := c.metrics[metricID]
	if !exists {
		return nil, fmt.Errorf("metric not found: %s", metricID)
	}
	return &metric, nil
}

// GetMetricsByCategory retrieves metrics by category
func (c *ComplianceMonitor) GetMetricsByCategory(ctx context.Context, category string) ([]ComplianceMetric, error) {
	var result []ComplianceMetric
	for _, metric := range c.metrics {
		if metric.Category == category {
			result = append(result, metric)
		}
	}
	return result, nil
}

// GetAlerts retrieves open alerts
func (c *ComplianceMonitor) GetAlerts(ctx context.Context) ([]ComplianceAlert, error) {
	var alerts []ComplianceAlert
	for _, alert := range c.alerts {
		if alert.Status == "open" || alert.Status == "acknowledged" {
			alerts = append(alerts, alert)
		}
	}
	return alerts, nil
}

// AcknowledgeAlert acknowledges an alert
func (c *ComplianceMonitor) AcknowledgeAlert(ctx context.Context, alertID string) error {
	alert, exists := c.alerts[alertID]
	if !exists {
		return fmt.Errorf("alert not found: %s", alertID)
	}
	alert.Status = "acknowledged"
	c.alerts[alertID] = alert
	return nil
}

// ResolveAlert resolves an alert
func (c *ComplianceMonitor) ResolveAlert(ctx context.Context, alertID string) error {
	alert, exists := c.alerts[alertID]
	if !exists {
		return fmt.Errorf("alert not found: %s", alertID)
	}
	alert.Status = "resolved"
	alert.ResolvedAt = time.Now()
	c.alerts[alertID] = alert
	return nil
}

// GenerateDashboard generates a compliance dashboard
func (c *ComplianceMonitor) GenerateDashboard(ctx context.Context) (*ComplianceDashboard, error) {
	dashboard := &ComplianceDashboard{
		ID:            fmt.Sprintf("dashboard-%d", time.Now().UnixNano()),
		Timestamp:     time.Now(),
		Metrics:       []ComplianceMetric{},
		Alerts:        []ComplianceAlert{},
		LastUpdatedAt: time.Now(),
	}

	// Collect all metrics
	for _, metric := range c.metrics {
		dashboard.Metrics = append(dashboard.Metrics, metric)
	}

	// Collect open alerts
	openAlerts, _ := c.GetAlerts(ctx)
	dashboard.Alerts = openAlerts

	// Calculate overall compliance score
	if len(dashboard.Metrics) > 0 {
		var passCount int
		for _, metric := range dashboard.Metrics {
			if metric.Status == "pass" {
				passCount++
			}
		}
		dashboard.ComplianceScore = (float64(passCount) / float64(len(dashboard.Metrics))) * 100
	}

	// Determine overall status
	if len(openAlerts) > 0 {
		// Check for critical alerts
		for _, alert := range openAlerts {
			if alert.Severity == "critical" {
				dashboard.OverallStatus = "non_compliant"
				return dashboard, nil
			}
		}
		dashboard.OverallStatus = "warning"
	} else {
		dashboard.OverallStatus = "compliant"
	}

	return dashboard, nil
}

// Common Compliance Metrics

// NewSOC2Metrics creates standard SOC 2 compliance metrics
func NewSOC2Metrics() []ComplianceMetric {
	return []ComplianceMetric{
		// Common Criteria metrics
		{
			Name:        "Access Control Compliance",
			Category:    "SOC2_CC",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of access control requirements implemented",
		},
		{
			Name:        "Encryption Coverage",
			Category:    "SOC2_C",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of data encrypted in transit and at rest",
		},
		{
			Name:        "System Uptime",
			Category:    "SOC2_A",
			Threshold:   99.95,
			Target:      99.95,
			Unit:        "%",
			Description: "System availability SLA target",
		},
		{
			Name:        "Mean Time to Recovery",
			Category:    "SOC2_A",
			Threshold:   240, // 4 hours in minutes
			Target:      240,
			Unit:        "minutes",
			Description: "Target recovery time objective",
		},
		{
			Name:        "Incident Response Time",
			Category:    "SOC2_CC",
			Threshold:   60, // 1 hour in minutes
			Target:      60,
			Unit:        "minutes",
			Description: "Time to respond to critical security incidents",
		},
		{
			Name:        "Patch Management Compliance",
			Category:    "SOC2_CC",
			Threshold:   99,
			Target:      99,
			Unit:        "%",
			Description: "Percentage of systems with current patches",
		},
		{
			Name:        "Training Completion Rate",
			Category:    "SOC2_CC",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of personnel completing mandatory security training",
		},
		{
			Name:        "MFA Adoption",
			Category:    "SOC2_S",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of users with MFA enabled",
		},
	}
}

// NewISO27001Metrics creates standard ISO 27001 ISMS metrics
func NewISO27001Metrics() []ComplianceMetric {
	return []ComplianceMetric{
		{
			Name:        "Control Implementation Rate",
			Category:    "ISO27001",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of 114 ISO 27001 controls implemented",
		},
		{
			Name:        "Risk Assessment Completion",
			Category:    "ISO27001",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Annual risk assessment completion",
		},
		{
			Name:        "Audit Finding Remediation",
			Category:    "ISO27001",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of audit findings remediated on time",
		},
		{
			Name:        "Management Review Completion",
			Category:    "ISO27001",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Quarterly management review completion",
		},
		{
			Name:        "Incident Investigation Completion",
			Category:    "ISO27001",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of incidents with completed root cause analysis",
		},
	}
}

// NewGDPRMetrics creates GDPR compliance metrics
func NewGDPRMetrics() []ComplianceMetric {
	return []ComplianceMetric{
		{
			Name:        "Data Subject Access Requests Processing",
			Category:    "GDPR",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of DSARs fulfilled within 30 days",
		},
		{
			Name:        "Data Breach Notification Time",
			Category:    "GDPR",
			Threshold:   72, // 72 hours max
			Target:      72,
			Unit:        "hours",
			Description: "Time to notify supervisory authority of breach",
		},
		{
			Name:        "Privacy Impact Assessment Coverage",
			Category:    "GDPR",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of high-risk processing with completed PIA",
		},
		{
			Name:        "Consent Management",
			Category:    "GDPR",
			Threshold:   100,
			Target:      100,
			Unit:        "%",
			Description: "Percentage of processing with documented consent",
		},
	}
}
