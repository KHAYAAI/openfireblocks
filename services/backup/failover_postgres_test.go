package backup

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The RPO check is the step that decides how much data a failover loses, so
// it is tested directly rather than through a live standby: real lag on an
// idle replica is legitimately zero, which makes a database-backed test of
// the refusal path non-deterministic.
func TestPostgresFailover_RefuseForLag(t *testing.T) {
	tests := []struct {
		name       string
		failover   PostgresFailover
		lag        time.Duration
		rpo        time.Duration
		wantRefuse bool
	}{
		{
			name:       "lag within the plan's RPO proceeds",
			lag:        30 * time.Second,
			rpo:        time.Minute,
			wantRefuse: false,
		},
		{
			name:       "lag beyond the plan's RPO is refused",
			lag:        90 * time.Second,
			rpo:        time.Minute,
			wantRefuse: true,
		},
		{
			name:       "lag exactly at the RPO proceeds",
			lag:        time.Minute,
			rpo:        time.Minute,
			wantRefuse: false,
		},
		{
			name:       "an explicit MaxLag overrides the plan's RPO",
			failover:   PostgresFailover{MaxLag: 10 * time.Second},
			lag:        30 * time.Second,
			rpo:        time.Hour,
			wantRefuse: true,
		},
		{
			name:       "force accepts the data loss deliberately",
			failover:   PostgresFailover{Force: true},
			lag:        24 * time.Hour,
			rpo:        time.Minute,
			wantRefuse: false,
		},
		{
			name:       "no RPO and no MaxLag means no lag ceiling to enforce",
			lag:        time.Hour,
			rpo:        0,
			wantRefuse: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.failover.refuseForLag(tc.lag, tc.rpo)
			if tc.wantRefuse && err == nil {
				t.Fatalf("expected refusal for lag %s against RPO %s", tc.lag, tc.rpo)
			}
			if !tc.wantRefuse && err != nil {
				t.Fatalf("expected promotion to proceed, got refusal: %v", err)
			}
			// A refusal must say why, in terms an operator mid-incident can
			// act on -- including that force is the deliberate override.
			if tc.wantRefuse && !strings.Contains(err.Error(), "force") {
				t.Errorf("refusal should mention the force override, got: %v", err)
			}
		})
	}
}

// A coordinator with no standby configured must say so rather than
// silently reporting a failover it never performed.
func TestPostgresFailover_RequiresStandbyDSN(t *testing.T) {
	pf := &PostgresFailover{}
	_, err := pf.Promote(context.Background(), time.Minute)
	if err == nil {
		t.Fatal("expected an error when no standby DSN is configured")
	}
	if !strings.Contains(err.Error(), "STANDBY_DATABASE_URL") {
		t.Errorf("error should name the setting that fixes it, got: %v", err)
	}
}
