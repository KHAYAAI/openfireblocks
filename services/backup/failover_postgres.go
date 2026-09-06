package backup

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Real cross-region Postgres failover: promoting a streaming-replication
// standby to primary. This replaces failoverPostgres's previous honest-but-
// unimplemented `return "failed"`.
//
// What promotion actually involves, and what this does:
//
//  1. Connect to the standby and confirm it IS a standby
//     (pg_is_in_recovery() must be true). Promoting something that is
//     already a primary is a no-op at best and, if the operator has the
//     wrong host, a split-brain at worst.
//  2. Measure replication lag and refuse to promote past the plan's RPO.
//     Promotion discards whatever the standby has not received, so this is
//     the step that decides how much data the failover loses. A caller can
//     override with force for a genuine "primary is gone, take the loss"
//     incident.
//  3. Call pg_promote() and wait for the server to leave recovery.
//  4. Verify it is writable afterwards, rather than trusting the call.
//
// Postgres 12+ only: pg_promote() replaces the older trigger-file mechanism.
type PostgresFailover struct {
	// StandbyDSN points at the standby to be promoted (the secondary
	// region's replica -- see infrastructure/terraform/modules/rds-replica).
	StandbyDSN string

	// MaxLag is the replication lag beyond which promotion is refused
	// unless forced. Zero means "use the DR plan's RPO".
	MaxLag time.Duration

	// PromotionTimeout bounds the wait for the standby to leave recovery.
	// Zero means 60s.
	PromotionTimeout time.Duration

	// Force skips the lag check. For the case where the primary is already
	// gone and the data loss is being accepted deliberately.
	Force bool

	// openDB is swappable for tests; nil means database/sql with lib/pq.
	openDB func(dsn string) (*sql.DB, error)
}

// PostgresFailoverResult reports what actually happened, including the
// measured lag at promotion time -- which is the real RPO for this
// failover, as opposed to the RPO the plan aspires to.
type PostgresFailoverResult struct {
	Promoted        bool          `json:"promoted"`
	LagAtPromotion  time.Duration `json:"lag_at_promotion"`
	PromotionTime   time.Duration `json:"promotion_time"`
	WasInRecovery   bool          `json:"was_in_recovery"`
	WritableAfter   bool          `json:"writable_after"`
	SkippedForceOff bool          `json:"skipped_force_off,omitempty"`
	Message         string        `json:"message,omitempty"`
}

func (p *PostgresFailover) open(dsn string) (*sql.DB, error) {
	if p.openDB != nil {
		return p.openDB(dsn)
	}
	return sql.Open("postgres", dsn)
}

// Promote performs the promotion. It returns an error rather than a status
// string so the caller can distinguish "refused for a good reason" (lag
// beyond RPO) from "tried and failed".
func (p *PostgresFailover) Promote(ctx context.Context, rpo time.Duration) (*PostgresFailoverResult, error) {
	if p.StandbyDSN == "" {
		return nil, fmt.Errorf("no standby DSN configured (set STANDBY_DATABASE_URL); cannot promote a replica that has not been identified")
	}

	db, err := p.open(p.StandbyDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open standby connection: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("standby unreachable: %w", err)
	}

	result := &PostgresFailoverResult{}

	// Step 1: it must actually be a standby.
	var inRecovery bool
	if err := db.QueryRowContext(ctx, `SELECT pg_is_in_recovery()`).Scan(&inRecovery); err != nil {
		return nil, fmt.Errorf("failed to check recovery state: %w", err)
	}
	result.WasInRecovery = inRecovery
	if !inRecovery {
		// Already a primary. Report it as a non-error no-op: re-running a
		// failover that already succeeded should be safe.
		result.Promoted = false
		result.WritableAfter = true
		result.Message = "target is already a primary (not in recovery); nothing to promote"
		return result, nil
	}

	// Step 2: how far behind is it? This is the data-loss decision.
	lag, err := p.replicationLag(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("failed to measure replication lag: %w", err)
	}
	result.LagAtPromotion = lag

	if refusal := p.refuseForLag(lag, rpo); refusal != nil {
		return result, refusal
	}

	// Step 3: promote.
	start := time.Now()
	if _, err := db.ExecContext(ctx, `SELECT pg_promote(wait := false)`); err != nil {
		return result, fmt.Errorf("pg_promote failed: %w", err)
	}

	timeout := p.PromotionTimeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := db.QueryRowContext(ctx, `SELECT pg_is_in_recovery()`).Scan(&inRecovery); err != nil {
			// Connections are commonly reset during promotion; keep trying
			// until the deadline rather than treating it as fatal.
			if time.Now().After(deadline) {
				return result, fmt.Errorf("timed out confirming promotion: %w", err)
			}
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if !inRecovery {
			break
		}
		if time.Now().After(deadline) {
			return result, fmt.Errorf("standby was still in recovery %s after pg_promote", timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
	result.PromotionTime = time.Since(start)
	result.Promoted = true

	// Step 4: verify it can actually take writes, rather than assuming.
	var readOnly string
	if err := db.QueryRowContext(ctx, `SHOW transaction_read_only`).Scan(&readOnly); err != nil {
		return result, fmt.Errorf("promoted, but failed to confirm write availability: %w", err)
	}
	result.WritableAfter = readOnly == "off"
	if !result.WritableAfter {
		return result, fmt.Errorf("promoted, but the server is still read-only (transaction_read_only=%s)", readOnly)
	}

	return result, nil
}

// refuseForLag is the data-loss decision, split out from Promote so it can
// be tested without a database: promotion discards whatever the standby has
// not received, so this is the check that decides whether a failover stays
// inside the plan's RPO. Returns nil when promotion may proceed.
func (p *PostgresFailover) refuseForLag(lag, rpo time.Duration) error {
	if p.Force {
		return nil
	}
	maxLag := p.MaxLag
	if maxLag == 0 {
		maxLag = rpo
	}
	if maxLag > 0 && lag > maxLag {
		return fmt.Errorf(
			"refusing to promote: replication lag %s exceeds the %s RPO -- promoting now would lose more data than the plan allows; set force to accept the loss",
			lag, maxLag)
	}
	return nil
}

// replicationLag reports how far behind the standby is. Prefers the
// timestamp-based measure (how old the last replayed transaction is), which
// is what an RPO is actually expressed in; falls back to reporting zero
// when the standby is fully caught up and has no lag timestamp to report.
func (p *PostgresFailover) replicationLag(ctx context.Context, db *sql.DB) (time.Duration, error) {
	var lagSeconds sql.NullFloat64
	err := db.QueryRowContext(ctx, `
		SELECT CASE
		         WHEN pg_last_wal_receive_lsn() IS NOT NULL
		          AND pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0
		         ELSE EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))
		       END
	`).Scan(&lagSeconds)
	if err != nil {
		return 0, err
	}
	if !lagSeconds.Valid || lagSeconds.Float64 < 0 {
		// No replayed transaction yet (a freshly built standby that has
		// received nothing). Not an error, and not evidence of lag.
		return 0, nil
	}
	return time.Duration(lagSeconds.Float64 * float64(time.Second)), nil
}
