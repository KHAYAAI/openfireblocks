package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

type PostgresDB struct {
	db *sql.DB
}

func NewPostgresDB() (*PostgresDB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:dev-only@localhost:5432/openfireblocks?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}
	log.Printf("policy service connected to PostgreSQL")
	return &PostgresDB{db: db}, nil
}

func (p *PostgresDB) Close() error { return p.db.Close() }

// policyRules is what's actually stored in policies.rules (JSONB): the
// table has no dedicated columns for a policy's rule list, approval config,
// or rate limit, so all three are packed into that one column together.
type policyRules struct {
	Rules     []*PolicyRule    `json:"rules"`
	Approvals *ApprovalConfig  `json:"approvals,omitempty"`
	RateLimit *RateLimitConfig `json:"rate_limit,omitempty"`
}

func (p *PostgresDB) CreatePolicy(ctx context.Context, policy *Policy) error {
	rules, err := json.Marshal(policyRules{Rules: policy.Rules, Approvals: policy.Approvals, RateLimit: policy.RateLimit})
	if err != nil {
		return fmt.Errorf("failed to marshal policy rules: %w", err)
	}

	// customer_id isn't part of the Policy API surface -- it's derived from
	// the key the policy is scoped to, since every key belongs to exactly
	// one customer and duplicating that onto every policy risks it drifting
	// out of sync with the key's actual owner.
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO policies (policy_id, customer_id, key_id, name, description, rules, status, created_at, updated_at)
		SELECT $1::uuid, key_pairs.customer_id, $2::uuid, $3, $4, $5, $6, $7, $8
		FROM key_pairs WHERE key_pairs.key_id = $2::uuid
	`, policy.PolicyID, policy.KeyID, policy.Name, policy.Description, rules, policy.Status, policy.CreatedAt, policy.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert policy: %w", err)
	}
	return nil
}

func (p *PostgresDB) GetPolicy(ctx context.Context, policyID string) (*Policy, error) {
	var policy Policy
	var rulesRaw []byte
	row := p.db.QueryRowContext(ctx, `
		SELECT policy_id, key_id, name, COALESCE(description, ''), status, rules, created_at, updated_at
		FROM policies WHERE policy_id = $1::uuid
	`, policyID)
	if err := row.Scan(&policy.PolicyID, &policy.KeyID, &policy.Name, &policy.Description, &policy.Status, &rulesRaw, &policy.CreatedAt, &policy.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("policy %s not found", policyID)
		}
		return nil, fmt.Errorf("failed to query policy: %w", err)
	}
	var pr policyRules
	if err := json.Unmarshal(rulesRaw, &pr); err != nil {
		return nil, fmt.Errorf("failed to unmarshal policy rules: %w", err)
	}
	policy.Rules = pr.Rules
	policy.Approvals = pr.Approvals
	policy.RateLimit = pr.RateLimit
	return &policy, nil
}

func (p *PostgresDB) ListPoliciesByKey(ctx context.Context, keyID string) ([]*Policy, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT policy_id, key_id, name, COALESCE(description, ''), status, rules, created_at, updated_at
		FROM policies WHERE key_id = $1::uuid
		ORDER BY created_at DESC
	`, keyID)
	if err != nil {
		return nil, fmt.Errorf("failed to query policies: %w", err)
	}
	defer rows.Close()

	var policies []*Policy
	for rows.Next() {
		var policy Policy
		var rulesRaw []byte
		if err := rows.Scan(&policy.PolicyID, &policy.KeyID, &policy.Name, &policy.Description, &policy.Status, &rulesRaw, &policy.CreatedAt, &policy.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan policy row: %w", err)
		}
		var pr policyRules
		if err := json.Unmarshal(rulesRaw, &pr); err != nil {
			return nil, fmt.Errorf("failed to unmarshal policy rules: %w", err)
		}
		policy.Rules = pr.Rules
		policy.Approvals = pr.Approvals
		policy.RateLimit = pr.RateLimit
		policies = append(policies, &policy)
	}
	return policies, rows.Err()
}

func (p *PostgresDB) UpdatePolicy(ctx context.Context, policy *Policy) error {
	rules, err := json.Marshal(policyRules{Rules: policy.Rules, Approvals: policy.Approvals, RateLimit: policy.RateLimit})
	if err != nil {
		return fmt.Errorf("failed to marshal policy rules: %w", err)
	}
	result, err := p.db.ExecContext(ctx, `
		UPDATE policies SET name = $2, description = $3, rules = $4, status = $5, updated_at = $6
		WHERE policy_id = $1::uuid
	`, policy.PolicyID, policy.Name, policy.Description, rules, policy.Status, policy.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to update policy: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("policy %s not found", policy.PolicyID)
	}
	return nil
}
