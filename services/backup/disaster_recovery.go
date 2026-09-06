package backup

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// FailoverStatus represents the status of a failover operation
type FailoverStatus string

const (
	FailoverStatusReady      FailoverStatus = "ready"
	FailoverStatusInitiated  FailoverStatus = "initiated"
	FailoverStatusInProgress FailoverStatus = "in_progress"
	FailoverStatusCompleted  FailoverStatus = "completed"
	FailoverStatusFailed     FailoverStatus = "failed"
	FailoverStatusRolledBack FailoverStatus = "rolled_back"
)

// DisasterRecoveryPlan defines a DR plan
type DisasterRecoveryPlan struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Description     string        `json:"description"`
	PrimaryRegion   string        `json:"primary_region"`
	SecondaryRegion string        `json:"secondary_region"`
	RTO             time.Duration `json:"rto"` // Recovery Time Objective
	RPO             time.Duration `json:"rpo"` // Recovery Point Objective
	BackupFrequency time.Duration `json:"backup_frequency"`
	TestFrequency   time.Duration `json:"test_frequency"`
	LastTest        time.Time     `json:"last_test"`
	NextTest        time.Time     `json:"next_test"`
	Status          string        `json:"status"` // active, testing, failed
	Components      []string      `json:"components"`
}

// FailoverOperation tracks a failover operation
type FailoverOperation struct {
	ID           string                     `json:"id"`
	Status       FailoverStatus             `json:"status"`
	TriggeredAt  time.Time                  `json:"triggered_at"`
	CompletedAt  time.Time                  `json:"completed_at"`
	Duration     time.Duration              `json:"duration"`
	SourceRegion string                     `json:"source_region"`
	TargetRegion string                     `json:"target_region"`
	RTO          time.Duration              `json:"rto"`
	RPO          time.Duration              `json:"rpo"`
	DataLoss     int64                      `json:"data_loss"` // Bytes of data lost
	Components   map[string]ComponentStatus `json:"components"`
	RollbackPlan *FailoverOperation         `json:"rollback_plan,omitempty"`
	ErrorMessage string                     `json:"error_message,omitempty"`
}

// ComponentStatus tracks the status of a component during failover
type ComponentStatus struct {
	Name         string        `json:"name"`
	Status       string        `json:"status"` // healthy, degraded, failed
	FailoverTime time.Duration `json:"failover_time"`
	ErrorMessage string        `json:"error_message,omitempty"`
}

// DisasterRecoveryCoordinator manages DR operations
type DisasterRecoveryCoordinator struct {
	backupManager *BackupManager
	plans         map[string]*DisasterRecoveryPlan
	operations    map[string]*FailoverOperation
	mu            sync.RWMutex

	// postgresFailover promotes the secondary region's streaming-replication
	// standby. Nil when no standby has been configured, in which case the
	// postgres component reports failed with a message saying so rather than
	// pretending to have failed over.
	postgresFailover *PostgresFailover

	// componentErrors records why each component ended where it did, so a
	// failed failover explains itself instead of just saying "failed".
	componentErrors map[string]string
}

// SetPostgresFailover wires in a real standby promoter. See
// PostgresFailover -- without this, the postgres component of a failover
// cannot succeed, by design.
func (d *DisasterRecoveryCoordinator) SetPostgresFailover(pf *PostgresFailover) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.postgresFailover = pf
}

// NewDisasterRecoveryCoordinator creates a new DR coordinator
func NewDisasterRecoveryCoordinator(backupManager *BackupManager) *DisasterRecoveryCoordinator {
	return &DisasterRecoveryCoordinator{
		backupManager:   backupManager,
		plans:           make(map[string]*DisasterRecoveryPlan),
		operations:      make(map[string]*FailoverOperation),
		componentErrors: make(map[string]string),
	}
}

// CreateDRPlan creates a new disaster recovery plan
func (d *DisasterRecoveryCoordinator) CreateDRPlan(ctx context.Context, plan *DisasterRecoveryPlan) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if plan.ID == "" {
		plan.ID = fmt.Sprintf("drplan-%d", time.Now().UnixNano())
	}

	if plan.RTO == 0 {
		plan.RTO = 4 * time.Hour // Default RTO
	}
	if plan.RPO == 0 {
		plan.RPO = 1 * time.Hour // Default RPO
	}
	if plan.BackupFrequency == 0 {
		plan.BackupFrequency = 15 * time.Minute // Backup every 15 minutes
	}
	if plan.TestFrequency == 0 {
		plan.TestFrequency = 7 * 24 * time.Hour // Test weekly
	}

	plan.Status = "active"
	plan.NextTest = time.Now().Add(plan.TestFrequency)

	d.plans[plan.ID] = plan
	return nil
}

// GetDRPlan retrieves a DR plan
func (d *DisasterRecoveryCoordinator) GetDRPlan(ctx context.Context, planID string) (*DisasterRecoveryPlan, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	plan, exists := d.plans[planID]
	if !exists {
		return nil, fmt.Errorf("DR plan not found: %s", planID)
	}
	return plan, nil
}

// InitiateFailover initiates a failover to the secondary region
func (d *DisasterRecoveryCoordinator) InitiateFailover(ctx context.Context, planID string) (*FailoverOperation, error) {
	d.mu.Lock()
	plan, exists := d.plans[planID]
	d.mu.Unlock()

	if !exists {
		return nil, fmt.Errorf("DR plan not found: %s", planID)
	}

	operation := &FailoverOperation{
		ID:           fmt.Sprintf("failover-%d", time.Now().UnixNano()),
		Status:       FailoverStatusInitiated,
		TriggeredAt:  time.Now(),
		SourceRegion: plan.PrimaryRegion,
		TargetRegion: plan.SecondaryRegion,
		RTO:          plan.RTO,
		RPO:          plan.RPO,
		Components:   make(map[string]ComponentStatus),
	}

	d.mu.Lock()
	d.operations[operation.ID] = operation
	d.mu.Unlock()

	// Start failover in background
	go d.executeFailover(ctx, operation, plan)

	return operation, nil
}

// executeFailover executes the failover operation
func (d *DisasterRecoveryCoordinator) executeFailover(ctx context.Context, operation *FailoverOperation, plan *DisasterRecoveryPlan) {
	operation.Status = FailoverStatusInProgress

	startTime := time.Now()
	deadline := startTime.Add(operation.RTO)

	for _, component := range plan.Components {
		if time.Now().After(deadline) {
			operation.ErrorMessage = "Failover exceeded RTO"
			operation.Status = FailoverStatusFailed
			return
		}

		componentStart := time.Now()
		status := d.failoverComponent(ctx, component, plan.RPO)
		componentDuration := time.Since(componentStart)

		operation.Components[component] = ComponentStatus{
			Name:         component,
			Status:       status,
			FailoverTime: componentDuration,
			ErrorMessage: d.componentError(component),
		}

		if status == "failed" {
			operation.ErrorMessage = fmt.Sprintf("Component %s failed to failover: %s", component, d.componentError(component))
			operation.Status = FailoverStatusFailed

			// Attempt rollback
			d.rollbackFailover(ctx, operation)
			return
		}
	}

	// Calculate actual recovery time and data loss
	operation.CompletedAt = time.Now()
	operation.Duration = operation.CompletedAt.Sub(operation.TriggeredAt)
	operation.DataLoss = int64(operation.Duration.Seconds()) * 1000 * 1024 // Approximate

	operation.Status = FailoverStatusCompleted
}

// failoverComponent performs failover for a specific component
func (d *DisasterRecoveryCoordinator) failoverComponent(ctx context.Context, component string, rpo time.Duration) string {
	// Component failover logic varies by component type
	switch component {
	case "postgres":
		return d.failoverPostgres(ctx, rpo)
	case "vault":
		return d.failoverVault(ctx)
	case "api-gateway":
		return d.failoverAPIGateway(ctx)
	case "temporal":
		return d.failoverTemporal(ctx)
	default:
		return "unknown"
	}
}

// Failover component implementations.
//
// History worth keeping in view: all four of these once unconditionally
// returned "healthy" with no logic at all, which meant the one moment this
// code's correctness actually matters (a live incident) was exactly when it
// would report full success regardless of whether anything happened. They
// were then changed to return "failed" honestly. postgres is now genuinely
// implemented; the rest still report failed, with a message naming what
// they would need, because a DR coordinator that lies is worse than one
// that admits it cannot act.

// failoverPostgres promotes the secondary region's streaming-replication
// standby to primary. This is the component that holds customer data, so it
// is the one that determines whether a failover is real.
func (d *DisasterRecoveryCoordinator) failoverPostgres(ctx context.Context, rpo time.Duration) string {
	d.mu.RLock()
	pf := d.postgresFailover
	d.mu.RUnlock()

	if pf == nil {
		d.recordComponentError("postgres", "no standby configured: set STANDBY_DATABASE_URL so the coordinator knows which replica to promote")
		return "failed"
	}

	result, err := pf.Promote(ctx, rpo)
	if err != nil {
		d.recordComponentError("postgres", err.Error())
		return "failed"
	}
	if !result.WritableAfter {
		d.recordComponentError("postgres", "promotion completed but the server is not accepting writes")
		return "failed"
	}

	if result.Promoted {
		d.recordComponentError("postgres", fmt.Sprintf(
			"promoted in %s; replication lag at promotion was %s (this is the failover's real RPO)",
			result.PromotionTime, result.LagAtPromotion))
	} else {
		d.recordComponentError("postgres", result.Message)
	}
	return "healthy"
}

func (d *DisasterRecoveryCoordinator) failoverVault(ctx context.Context) string {
	// Promoting a secondary Vault cluster is a Vault Enterprise DR
	// replication operation; on OSS the secondary is a separate cluster
	// restored from backup and unsealed by an operator. Either path needs
	// credentials and a target cluster this service is not configured with.
	d.recordComponentError("vault", "not implemented: promoting a Vault secondary requires either Enterprise DR replication or an operator-driven restore-and-unseal of the secondary cluster; neither is wired up")
	return "failed"
}

func (d *DisasterRecoveryCoordinator) failoverAPIGateway(ctx context.Context) string {
	// Routing traffic to the secondary region means updating a DNS record
	// or load balancer target group (Route53 / ALB in this platform's
	// Terraform), which needs cloud credentials this service does not hold.
	d.recordComponentError("api-gateway", "not implemented: shifting traffic requires DNS or load-balancer updates (Route53/ALB) and cloud credentials this service does not hold")
	return "failed"
}

func (d *DisasterRecoveryCoordinator) failoverTemporal(ctx context.Context) string {
	// Temporal follows Postgres: once the database is promoted, the
	// frontend and workers need to be pointed at it and restarted, which is
	// an orchestration-layer action (a Helm/Kubernetes rollout), not
	// something this process can perform against itself.
	d.recordComponentError("temporal", "not implemented: after the database is promoted, Temporal's frontend and workers must be repointed and restarted by the orchestration layer (a Kubernetes rollout), which this service cannot perform")
	return "failed"
}

// recordComponentError stores the reason a component ended where it did, so
// InitiateFailover's result explains itself.
func (d *DisasterRecoveryCoordinator) recordComponentError(component, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.componentErrors == nil {
		d.componentErrors = make(map[string]string)
	}
	d.componentErrors[component] = message
}

func (d *DisasterRecoveryCoordinator) componentError(component string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.componentErrors[component]
}

// rollbackFailover rolls back a failed failover.
//
// Used to have a loop over operation.Components that did nothing (the body
// was just a comment, "Iterate components in reverse") and then
// unconditionally set operation.Status = FailoverStatusRolledBack -- so a
// caller checking that status would see "rolled back" whether or not any
// component was actually reverted. map iteration order in Go is also
// randomized, so "in reverse" over a map (Components is
// map[string]ComponentStatus) wasn't well-defined to begin with even before
// the loop was made to do nothing.
//
// Component failover isn't wired up to real infrastructure yet (see
// failoverPostgres etc. above), so there's nothing to actually reverse
// here either. Marked failed rather than claiming a rollback that can't
// happen.
func (d *DisasterRecoveryCoordinator) rollbackFailover(ctx context.Context, operation *FailoverOperation) {
	operation.ErrorMessage = operation.ErrorMessage + "; rollback not implemented: component failover has no real backend to roll back"
	operation.Status = FailoverStatusFailed
}

// TestDisasterRecovery performs a DR test without actual failover
func (d *DisasterRecoveryCoordinator) TestDisasterRecovery(ctx context.Context, planID string) (*FailoverOperation, error) {
	plan, err := d.GetDRPlan(ctx, planID)
	if err != nil {
		return nil, err
	}

	operation := &FailoverOperation{
		ID:           fmt.Sprintf("drtest-%d", time.Now().UnixNano()),
		Status:       FailoverStatusInProgress,
		TriggeredAt:  time.Now(),
		SourceRegion: plan.PrimaryRegion,
		TargetRegion: plan.SecondaryRegion,
		Components:   make(map[string]ComponentStatus),
	}

	// Test restore from backup
	restorePoints, err := d.backupManager.ListRestorePoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list restore points: %w", err)
	}

	if len(restorePoints) == 0 {
		return nil, fmt.Errorf("no restore points available for testing")
	}

	// Use most recent restore point
	latestPoint := restorePoints[0]

	// Test restore (would be done in isolated environment in production)
	if err := d.backupManager.RestoreFromPoint(ctx, latestPoint); err != nil {
		operation.Status = FailoverStatusFailed
		operation.ErrorMessage = fmt.Sprintf("Restore test failed: %v", err)
		return operation, err
	}

	operation.Status = FailoverStatusCompleted
	operation.CompletedAt = time.Now()
	operation.Duration = operation.CompletedAt.Sub(operation.TriggeredAt)

	// Update plan's last test
	plan.LastTest = time.Now()
	plan.NextTest = time.Now().Add(plan.TestFrequency)

	return operation, nil
}

// GetFailoverOperation retrieves a failover operation
func (d *DisasterRecoveryCoordinator) GetFailoverOperation(ctx context.Context, operationID string) (*FailoverOperation, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	operation, exists := d.operations[operationID]
	if !exists {
		return nil, fmt.Errorf("failover operation not found: %s", operationID)
	}
	return operation, nil
}

// HealthCheck performs a health check of DR readiness
func (d *DisasterRecoveryCoordinator) HealthCheck(ctx context.Context, planID string) (string, error) {
	plan, err := d.GetDRPlan(ctx, planID)
	if err != nil {
		return "unknown", err
	}

	// Check if backups are recent enough for RPO
	restorePoints, err := d.backupManager.ListRestorePoints(ctx)
	if err != nil {
		return "degraded", fmt.Errorf("cannot verify backup status: %w", err)
	}

	if len(restorePoints) == 0 {
		return "failed", fmt.Errorf("no backups available")
	}

	latestBackup := restorePoints[0]
	if time.Since(latestBackup.Timestamp) > plan.RPO {
		return "degraded", fmt.Errorf("latest backup exceeds RPO")
	}

	// Check if last test passed
	if plan.LastTest.IsZero() {
		return "untested", nil
	}

	if time.Since(plan.LastTest) > plan.TestFrequency {
		return "warning", fmt.Errorf("DR plan not recently tested")
	}

	return "healthy", nil
}
